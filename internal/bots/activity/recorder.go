// Package activity aggregates accepted bot calls without doing request-path I/O.
package activity

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const maxKeys = 10000

// Both the active window and retry backlog are bounded. Four surfaces and at
// most two UTC days per window keep the number of rows per key bounded too.
const maxRows = maxKeys * 8

type rowKey struct {
	tenant, key, day string
	surface          apikey.Surface
}
type row struct {
	calls  int64
	latest time.Time
}
type event struct {
	key, tenant string
	surface     apikey.Surface
	at          time.Time
}
type Recorder struct {
	db             *sqlx.DB
	events         chan event
	stop           chan context.Context
	done           chan struct{}
	once           sync.Once
	dropped        atomic.Uint64
	finalError     error // published when done closes
	lastFlushError atomic.Pointer[flushError]
}

func New(db *sqlx.DB) *Recorder {
	r := &Recorder{db: db, events: make(chan event, 16384), stop: make(chan context.Context, 1), done: make(chan struct{})}
	go r.run()
	return r
}

// Record never waits for a mutex, a database, or the background worker.
func (r *Recorder) Record(keyID, tenantID string, surface apikey.Surface) {
	if keyID == "" || tenantID == "" || !apikey.ValidSurface(surface) {
		return
	}
	select {
	case r.events <- event{keyID, tenantID, surface, time.Now().UTC()}:
	default:
		r.dropped.Add(1)
	}
}

// Stop drains accepted calls and retries the final flush within ctx. The
// worker owns all database writes, including the shutdown transaction.
func (r *Recorder) Stop(ctx context.Context) error {
	r.once.Do(func() { r.stop <- ctx })
	select {
	case <-r.done:
		return r.finalError
	case <-ctx.Done():
		select {
		case <-r.done:
			return r.finalError
		default:
		}
		if failure := r.lastFlushError.Load(); failure != nil {
			return errors.Join(failure.err, ctx.Err())
		}
		return ctx.Err()
	}
}
func (r *Recorder) run() {
	defer close(r.done)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	pending := map[rowKey]row{}
	keys := map[string]bool{}
	add := func(e event) {
		if !aggregate(pending, keys, e) {
			r.dropped.Add(1)
		}
	}
	flush := func(parent context.Context) error {
		if n := r.dropped.Swap(0); n > 0 {
			slog.Warn("bot activity overflow", "dropped_calls", n)
		}
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		if err := r.flush(ctx, pending); err != nil {
			r.lastFlushError.Store(&flushError{err})
			// Retrying a retained batch after an ambiguous commit (commit succeeded,
			// acknowledgement lost) can double count it. This is accepted for an
			// approximate call counter.
			slog.Warn("bot activity flush failed; retaining bounded backlog", "error", err, "rows", len(pending))
			return err
		}
		r.lastFlushError.Store(nil)
		clear(pending)
		clear(keys)
		return nil
	}
	for {
		select {
		case e := <-r.events:
			add(e)
		case <-ticker.C:
			_ = flush(context.Background())
		case shutdownCtx := <-r.stop:
			for {
				select {
				case e := <-r.events:
					add(e)
				default:
					r.finalError = retryFinalFlush(shutdownCtx, flush)
					return
				}
			}
		}
	}
}

func (r *Recorder) flush(ctx context.Context, rows map[rowKey]row) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Stable input order keeps concurrent upserts consistent across replicas.
	ordered := sortedKeys(rows)

	latest := map[string]time.Time{}
	for _, k := range ordered {
		if rows[k].latest.After(latest[k.key]) {
			latest[k.key] = rows[k].latest
		}
	}

	keyIDs, tenants, timestamps := []string{}, []string{}, []string{}
	rowIDs, rowTenants, days, surfaces, calls := []string{}, []string{}, []string{}, []string{}, []int64{}
	seen := map[string]bool{}
	for _, k := range ordered {
		if !seen[k.key] {
			keyIDs = append(keyIDs, k.key)
			tenants = append(tenants, k.tenant)
			timestamps = append(timestamps, latest[k.key].Format(time.RFC3339Nano))
			seen[k.key] = true
		}
		rowIDs = append(rowIDs, k.key)
		rowTenants = append(rowTenants, k.tenant)
		days = append(days, k.day)
		surfaces = append(surfaces, string(k.surface))
		calls = append(calls, rows[k].calls)
	}
	// Two set-based writes keep even a full window to two database round trips.
	_, err = tx.ExecContext(ctx, `UPDATE api_keys k SET last_used_at=v.latest
 FROM unnest($1::text[],$2::text[],$3::timestamptz[]) AS v(api_key_id,tenant_id,latest)
 WHERE k.id::text=v.api_key_id
 AND (k.instance_id=v.tenant_id OR (NULLIF(k.instance_id,'') IS NULL AND k.org_id=v.tenant_id))
 AND (k.last_used_at IS NULL OR k.last_used_at < v.latest - interval '60 seconds')`, pq.Array(keyIDs), pq.Array(tenants), pq.Array(timestamps))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO api_key_activity_daily (tenant_id,api_key_id,day,surface,calls)
 SELECT * FROM unnest($1::text[],$2::text[],$3::date[],$4::text[],$5::bigint[]) WHERE true
 ON CONFLICT (api_key_id,day,surface) DO UPDATE SET calls=api_key_activity_daily.calls+EXCLUDED.calls
 WHERE api_key_activity_daily.tenant_id=EXCLUDED.tenant_id`, pq.Array(rowTenants), pq.Array(rowIDs), pq.Array(days), pq.Array(surfaces), pq.Array(calls))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func aggregate(pending map[rowKey]row, keys map[string]bool, e event) bool {
	k := rowKey{e.tenant, e.key, e.at.Format("2006-01-02"), e.surface}
	if (!keys[e.key] && len(keys) >= maxKeys) || (pending[k].calls == 0 && len(pending) >= maxRows) {
		return false
	}
	keys[e.key] = true
	v := pending[k]
	v.calls++
	if e.at.After(v.latest) {
		v.latest = e.at
	}
	pending[k] = v
	return true
}

type flushError struct{ err error }

func retryFinalFlush(ctx context.Context, flush func(context.Context) error) error {
	for {
		err := flush(ctx)
		if err == nil {
			return nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}
