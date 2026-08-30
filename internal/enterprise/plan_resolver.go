package enterprise

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// A managed gateway never sees tenant config: the controlplane middleware that
// populates it runs in the services pod, and the gateway's own
// LocalTenantMiddleware deliberately injects nothing in managed mode. So every
// authenticated cloud tenant resolved "managed-bypass" and ran with no plan
// limits at all.
//
// This file closes that gap without touching tenant.Config. Injecting a
// partial Config on the gateway would change behaviour for every existing
// ConfigFromContext consumer, including sandbox ownership checks, and tenant
// scoping is not something to alter as a side effect of a billing fix. Instead
// the plan tier is resolved lazily, here, at the point entitlements are
// computed.
//
// Resolution runs only where entitlements are actually checked (resource
// creation, storage, channels), not on the inference hot path.

// PlanTierResolver maps a verified tenant id to its plan tier. Returning
// ok=false means "unknown", which preserves the previous bypass behaviour
// rather than guessing a tier.
type PlanTierResolver interface {
	PlanTier(ctx context.Context, tenantID string) (tier string, ok bool)
}

var (
	planResolver atomic.Value // holds PlanTierResolver

	// shadowEnforcement reports plan limits without applying them. It defaults
	// to ON: cloud tenants have been running unlimited, so switching straight
	// to enforcement would break live workloads that are currently legal.
	// Flip it only after reviewing what shadow mode reports.
	shadowEnforcement atomic.Bool
)

func init() { shadowEnforcement.Store(true) }

// SetPlanTierResolver installs the resolver used for managed tenants. Passing
// nil clears it, which returns the gateway to plain bypass.
func SetPlanTierResolver(r PlanTierResolver) {
	if r == nil {
		planResolver = atomic.Value{}
		return
	}
	planResolver.Store(r)
}

// SetShadowEnforcement toggles observe-only mode. True (the default) resolves
// and reports plan limits without applying them.
func SetShadowEnforcement(v bool) { shadowEnforcement.Store(v) }

// ShadowEnforcement reports whether plan limits are observed but not applied.
func ShadowEnforcement() bool { return shadowEnforcement.Load() }

func currentPlanResolver() PlanTierResolver {
	r, _ := planResolver.Load().(PlanTierResolver)
	return r
}

// resolveManagedPlanTier returns the plan tier for the verified tenant on this
// request, if a resolver is installed and knows one.
func resolveManagedPlanTier(ctx context.Context) (string, bool) {
	r := currentPlanResolver()
	if r == nil {
		return "", false
	}
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return "", false
	}
	return r.PlanTier(ctx, tenantID)
}

// shadowLimiter records, once per tenant and limit type, that a plan limit
// would have applied. Once-per-pair keeps a per-request code path from
// producing per-request log volume.
var shadowSeen sync.Map // string -> struct{}

func reportShadowLimits(tenantID, tier string, limits map[UsageType]int64) {
	for limitType, limit := range limits {
		if limit < 0 {
			continue // unlimited: nothing would have been denied
		}
		key := tenantID + "|" + string(limitType)
		if _, loaded := shadowSeen.LoadOrStore(key, struct{}{}); loaded {
			continue
		}
		logger.Infof(
			"entitlements shadow: tenant=%s tier=%s limit=%s value=%d would apply (not enforced)",
			tenantID, tier, limitType, limit,
		)
	}
}

// ResetShadowReportingForTest clears the once-per-pair dedupe.
func ResetShadowReportingForTest() { shadowSeen = sync.Map{} }

// --- Database-backed resolver -------------------------------------------

// dbPlanTierResolver reads plan_tier from the platform database's
// organizations table. The gateway already mounts EVS_PLATFORM_DSN, so no new
// credential is needed.
type dbPlanTierResolver struct {
	db    *sqlx.DB
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]planCacheEntry
}

type planCacheEntry struct {
	tier      string
	known     bool
	expiresAt time.Time
}

// NewDBPlanTierResolver builds a caching resolver over the platform database.
// ttl bounds how long a plan change takes to take effect; zero picks a
// conservative default.
func NewDBPlanTierResolver(db *sqlx.DB, ttl time.Duration) PlanTierResolver {
	if db == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &dbPlanTierResolver{db: db, ttl: ttl, cache: make(map[string]planCacheEntry)}
}

func (r *dbPlanTierResolver) PlanTier(ctx context.Context, tenantID string) (string, bool) {
	if entry, ok := r.lookup(tenantID); ok {
		return entry.tier, entry.known
	}

	var tier *string
	// The tenant id on a managed gateway is the organization id (see
	// docs/design/sandbox-isolation notes); match on either to stay correct if
	// that mapping is ever split.
	const q = `SELECT plan_tier FROM organizations WHERE id::text = $1 LIMIT 1`
	err := r.db.QueryRowxContext(ctx, q, tenantID).Scan(&tier)
	if err != nil {
		// Unknown on error: fall back to bypass rather than inventing a tier.
		// Never fail a request because billing metadata was unavailable.
		logger.Warnf("entitlements: plan tier lookup failed for tenant %s: %v", tenantID, err)
		r.store(tenantID, "", false)
		return "", false
	}
	if tier == nil || *tier == "" {
		r.store(tenantID, "", false)
		return "", false
	}
	r.store(tenantID, *tier, true)
	return *tier, true
}

func (r *dbPlanTierResolver) lookup(tenantID string) (planCacheEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.cache[tenantID]
	if !ok || timeNow().After(entry.expiresAt) {
		return planCacheEntry{}, false
	}
	return entry, true
}

func (r *dbPlanTierResolver) store(tenantID, tier string, known bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[tenantID] = planCacheEntry{tier: tier, known: known, expiresAt: timeNow().Add(r.ttl)}
}

// timeNow is a seam for tests.
var timeNow = time.Now
