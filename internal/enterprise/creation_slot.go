package enterprise

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ResourceSlot serializes capped-resource creation per (tenant, limit type).
//
// The transport-level CheckResourceLimit is COUNT-then-write: with async
// read-model projections there is no synchronous creation transaction to
// enforce inside, so two concurrent creates can both pass the count and land
// past the cap. ReserveResourceSlot closes that race at the RPC layer:
//
//	slot, err := enterprise.ReserveResourceSlot(ctx, db, monitor,
//	    enterprise.UsageTypeAgents, countQuery, args, 1, "agent", tenantID)
//	if err != nil { return resourceExhausted(err) }
//	defer slot.Release()
//	... dispatch the create command ...
//	slot.Confirm(ctx) // before returning success
//
// Mechanics: a dedicated pooled connection takes pg_advisory_lock on a key
// derived from (tenant, limit type), the count check runs under the lock, and
// Confirm holds the lock until the read model reflects the write (bounded
// wait) so the NEXT creator's count sees it even though projections are
// asynchronous. Advisory locks are cluster-wide, so this also serializes
// across gateway replicas sharing one Postgres.
//
// Failure posture: if the lock cannot be acquired (timeout, connection
// pressure) the reservation degrades to the plain unserialized check and
// logs — limits are nudges (D4 in docs/design/editions-and-billing.md);
// availability beats exactness here. A limit violation is always an error.
type ResourceSlot struct {
	conn       *sql.Conn
	key        int64
	db         *sqlx.DB
	countQuery string
	args       []interface{}
	before     int64
	delta      int64
	resource   string
	done       sync.Once
}

// slotLockTimeout bounds how long a creator waits for a concurrent creator of
// the same (tenant, resource) to finish.
const slotLockTimeout = 10 * time.Second

// slotBarrierTimeout bounds the Confirm read-your-write wait for the async
// projection to land. On timeout the lock is released anyway: the worst case
// is the pre-slot behavior (a racing creator sees a stale count), never a
// stuck creator.
const slotBarrierTimeout = 2 * time.Second

// ReserveResourceSlot resolves entitlements, serializes against concurrent
// creators of the same (tenant, limitType), and runs the quota check under
// the lock. A nil error means the caller may proceed with creation and MUST
// eventually call Release (defer) and SHOULD call Confirm on success.
//
// countQuery must be tenant-scoped and return a single COUNT(*). delta is how
// many resources the request creates (batch size, min 1). tenantKey scopes
// the lock; pass the tenant ID.
func ReserveResourceSlot(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, limitType UsageType, countQuery string, args []interface{}, delta int64, resourceName, tenantKey string) (*ResourceSlot, error) {
	ent := ResolveEntitlements(ctx, monitor)
	limit, capped := ent.Limit(limitType)
	if !capped {
		return &ResourceSlot{}, nil // unlimited: nothing to serialize
	}
	if limit == 0 {
		if ent.Source == "ce" {
			return nil, fmt.Errorf("%s is not available on the Community Edition — upgrade at https://everstack.ai/pricing", resourceName)
		}
		return nil, fmt.Errorf("%s is not available on your current plan — upgrade at https://everstack.ai/pricing", resourceName)
	}
	if db == nil {
		return &ResourceSlot{}, nil // no read model to count against
	}
	if delta < 1 {
		delta = 1
	}

	key := slotKey(tenantKey, limitType)

	lockCtx, cancel := context.WithTimeout(ctx, slotLockTimeout)
	defer cancel()

	conn, err := db.Conn(lockCtx)
	if err != nil {
		return degradedSlot(ctx, db, monitor, limitType, countQuery, args, delta, resourceName, err)
	}
	if _, err := conn.ExecContext(lockCtx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		_ = conn.Close()
		return degradedSlot(ctx, db, monitor, limitType, countQuery, args, delta, resourceName, err)
	}

	slot := &ResourceSlot{
		conn:       conn,
		key:        key,
		db:         db,
		countQuery: countQuery,
		args:       args,
		delta:      delta,
		resource:   resourceName,
	}

	countCtx, cancelCount := context.WithTimeout(ctx, 5*time.Second)
	defer cancelCount()
	var count int64
	if err := conn.QueryRowContext(countCtx, countQuery, args...).Scan(&count); err != nil {
		slot.Release()
		return nil, fmt.Errorf("failed to check %s limit: %w", resourceName, err)
	}
	if count+delta > limit {
		slot.Release()
		return nil, fmt.Errorf("%s limit reached: %d/%d — upgrade your plan for higher limits (https://everstack.ai/pricing)", resourceName, count, limit)
	}
	slot.before = count
	return slot, nil
}

// degradedSlot is the fallback when the advisory lock cannot be taken: run
// the plain (unserialized) check so limits still apply, and return a no-op
// slot.
func degradedSlot(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, limitType UsageType, countQuery string, args []interface{}, delta int64, resourceName string, cause error) (*ResourceSlot, error) {
	logger.WithError(cause).Warnf("resource_slot: could not serialize %s creation, falling back to unserialized check", resourceName)
	if err := CheckResourceLimit(ctx, db, monitor, limitType, countQuery, args, delta, resourceName); err != nil {
		return nil, err
	}
	return &ResourceSlot{}, nil
}

// Confirm waits (bounded) for the read model to reflect the creation, then
// releases the lock. Call it just before returning success so the next
// serialized creator counts this write. Safe to skip — Release alone keeps
// correctness of the lock lifecycle, at the cost of re-opening the projection
// lag window for the immediate next creator.
func (s *ResourceSlot) Confirm(ctx context.Context) {
	if s == nil || s.conn == nil {
		return
	}
	s.done.Do(func() {
		deadline := time.Now().Add(slotBarrierTimeout)
		for {
			pollCtx, cancel := context.WithTimeout(ctx, time.Second)
			var count int64
			err := s.conn.QueryRowContext(pollCtx, s.countQuery, s.args...).Scan(&count)
			cancel()
			if err == nil && count >= s.before+s.delta {
				break
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				logger.Debugf("resource_slot: %s read model did not reflect the write within %s, releasing anyway", s.resource, slotBarrierTimeout)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		s.unlockAndClose()
	})
}

// Release frees the lock without the read-your-write barrier. Idempotent and
// nil-safe; intended for defer so error paths always unlock.
func (s *ResourceSlot) Release() {
	if s == nil || s.conn == nil {
		return
	}
	s.done.Do(s.unlockAndClose)
}

func (s *ResourceSlot) unlockAndClose() {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, s.key); err != nil {
		// Session-level advisory locks survive returning the connection to
		// the pool. If we cannot unlock, poison the connection so the pool
		// discards it and the lock dies with the session.
		logger.WithError(err).Warnf("resource_slot: failed to unlock %s slot, discarding connection", s.resource)
		_ = s.conn.Raw(func(driverConn interface{}) error { return driver.ErrBadConn })
	}
	_ = s.conn.Close()
}

// slotKey derives a stable advisory-lock key from tenant + limit type.
func slotKey(tenantKey string, limitType UsageType) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("evs-resource-slot/"))
	_, _ = h.Write([]byte(tenantKey))
	_, _ = h.Write([]byte{'/'})
	_, _ = h.Write([]byte(limitType))
	return int64(h.Sum64())
}
