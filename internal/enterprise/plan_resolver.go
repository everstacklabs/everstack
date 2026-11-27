package enterprise

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/billingidentity"
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

	// enforcementPolicy is the per-tier enforcement state. The zero value is
	// all-shadow; a managed (Cloud) gateway replaces it at startup with
	// ManagedEnforcementPolicy, which enforces every tier unless
	// EVS_PLAN_LIMIT_ENFORCEMENT says otherwise. Self-hosted gateways never
	// consult it: their limits come from the licence or CE defaults.
	//
	// Owner decision 2026-10-06: Cloud plans include their limits, count
	// limits block at the cap, credit does not buy more objects, and orgs
	// already over a cap keep everything they have (only new creation is
	// refused). This supersedes the earlier "keep free in shadow until a
	// funding-band resolver exists" note: plan_tier is the subscription tier.
	enforcementPolicy = &tierEnforcementPolicy{enforce: map[string]bool{}}
)

// tierEnforcementPolicy resolves observe-only vs enforcing per plan tier.
// The zero value is all-shadow. Unknown or empty tiers never enforce
// (fail open + alert, per the enforcement-cluster decision log).
type tierEnforcementPolicy struct {
	mu             sync.RWMutex
	defaultEnforce bool
	enforce        map[string]bool // normalized tier -> enforce override
}

// knownEnforcementTiers bounds the per-tier surface: only catalog tiers can
// be flipped. Anything else in configuration is rejected, never guessed.
var knownEnforcementTiers = map[string]bool{
	"free": true, "basic": true, "pro": true, "enterprise": true,
}

func normalizeTier(tier string) string {
	return strings.ToLower(strings.TrimSpace(tier))
}

// SetShadowEnforcement toggles observe-only mode for ALL tiers. True (the
// default) resolves and reports plan limits without applying them. Kept for
// backward compatibility (startup wiring, tests); prefer SetTierEnforcement
// for rollout and SetEnforcementPolicy for configuration.
func SetShadowEnforcement(v bool) {
	enforcementPolicy.mu.Lock()
	defer enforcementPolicy.mu.Unlock()
	enforcementPolicy.defaultEnforce = !v
	enforcementPolicy.enforce = map[string]bool{}
	publishEnforcementModesLocked()
}

// ShadowEnforcement reports whether NO tier is currently enforced, i.e. the
// process is fully observe-only. With per-tier overrides in play this is
// false as soon as any tier enforces.
func ShadowEnforcement() bool {
	enforcementPolicy.mu.RLock()
	defer enforcementPolicy.mu.RUnlock()
	if enforcementPolicy.defaultEnforce {
		return false
	}
	for _, enforce := range enforcementPolicy.enforce {
		if enforce {
			return false
		}
	}
	return true
}

// SetTierEnforcement flips one tier between shadow (false) and enforcing
// (true). This is the per-tier kill switch and the rollout lever: unknown
// tier names are rejected. Safe to call at runtime.
func SetTierEnforcement(tier string, enforce bool) error {
	tier = normalizeTier(tier)
	if !knownEnforcementTiers[tier] {
		return fmt.Errorf("unknown plan tier %q (want free/basic/pro/enterprise)", tier)
	}
	enforcementPolicy.mu.Lock()
	defer enforcementPolicy.mu.Unlock()
	if enforcementPolicy.enforce == nil {
		enforcementPolicy.enforce = map[string]bool{}
	}
	enforcementPolicy.enforce[tier] = enforce
	publishEnforcementModesLocked()
	return nil
}

// TierEnforced reports whether the given tier's limits are applied rather
// than reported. Empty and unknown tiers never enforce.
func TierEnforced(tier string) bool {
	tier = normalizeTier(tier)
	if !knownEnforcementTiers[tier] {
		return false
	}
	enforcementPolicy.mu.RLock()
	defer enforcementPolicy.mu.RUnlock()
	if enforce, ok := enforcementPolicy.enforce[tier]; ok {
		return enforce
	}
	return enforcementPolicy.defaultEnforce
}

// SetEnforcementPolicy replaces the whole policy: the default plus per-tier
// overrides. Empty overrides mean the default applies to every tier.
func SetEnforcementPolicy(defaultEnforce bool, overrides map[string]bool) {
	enforcementPolicy.mu.Lock()
	defer enforcementPolicy.mu.Unlock()
	enforcementPolicy.defaultEnforce = defaultEnforce
	enforcementPolicy.enforce = map[string]bool{}
	for tier, enforce := range overrides {
		if knownEnforcementTiers[normalizeTier(tier)] {
			enforcementPolicy.enforce[normalizeTier(tier)] = enforce
		}
	}
	publishEnforcementModesLocked()
}

// ParseEnforcementPolicy parses an enforcement policy string with an
// all-shadow default. Managed gateways use ManagedEnforcementPolicy, which
// defaults to enforce. Syntax:
//   - "" (unset): all shadow (current default).
//   - "enforce": every tier enforces (legacy global flip, unchanged).
//   - "shadow": explicit all shadow.
//   - "tier=mode,..." e.g. "free=enforce,basic=enforce,pro=shadow":
//     per-tier rollout. Unknown tiers or modes are an error; callers must
//     fall back to all-shadow on error, never to enforcement.
func ParseEnforcementPolicy(s string) (defaultEnforce bool, overrides map[string]bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "shadow") {
		return false, nil, nil
	}
	if strings.EqualFold(s, "enforce") {
		return true, nil, nil
	}
	overrides = map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return false, nil, fmt.Errorf("invalid enforcement policy %q: want tier=enforce|shadow", part)
		}
		tier := normalizeTier(kv[0])
		if !knownEnforcementTiers[tier] {
			return false, nil, fmt.Errorf("unknown plan tier %q in enforcement policy", kv[0])
		}
		switch mode := normalizeTier(kv[1]); mode {
		case "enforce":
			overrides[tier] = true
		case "shadow":
			overrides[tier] = false
		default:
			return false, nil, fmt.Errorf("unknown enforcement mode %q for tier %q (want enforce|shadow)", kv[1], kv[0])
		}
	}
	return false, overrides, nil
}

// ManagedEnforcementPolicy parses EVS_PLAN_LIMIT_ENFORCEMENT for a managed
// (Cloud) gateway, where plan caps are enforced by default:
//   - "" (unset) or "enforce": every tier enforces.
//   - "shadow": the kill switch; every tier only reports.
//   - "tier=mode,...": per-tier exceptions on top of enforcement, e.g.
//     "pro=shadow" enforces every tier except pro.
//
// Errors are returned unchanged; the caller decides the fallback.
func ManagedEnforcementPolicy(s string) (defaultEnforce bool, overrides map[string]bool, err error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "", strings.EqualFold(s, "enforce"):
		return true, nil, nil
	case strings.EqualFold(s, "shadow"):
		return false, nil, nil
	}
	_, overrides, err = ParseEnforcementPolicy(s)
	if err != nil {
		return false, nil, err
	}
	return true, overrides, nil
}

// publishEnforcementModesLocked mirrors the policy into the
// entitlement_tier_enforced gauge. Caller holds the policy lock.
func publishEnforcementModesLocked() {
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		v := 0.0
		if enforcementPolicy.defaultEnforce {
			v = 1.0
		}
		if enforce, ok := enforcementPolicy.enforce[tier]; ok {
			v = 0.0
			if enforce {
				v = 1.0
			}
		}
		entitlementTierEnforced.WithLabelValues(tier).Set(v)
	}
}

// SetPlanTierResolver installs the resolver used for managed tenants. Passing
// nil clears it, which returns the gateway to plain bypass.
func SetPlanTierResolver(r PlanTierResolver) {
	if r == nil {
		planResolver = atomic.Value{}
		return
	}
	planResolver.Store(r)
}

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
		// EVS-6 shadow sink (Stage 0, slice 1): the Prometheus gauge feeds
		// the per-tier exposure table; the operational structured log flows
		// via OTEL to ClickHouse for per-tenant drill-down. Enforcement
		// behavior is unchanged — this only observes.
		entitlementShadowLimitedTenants.WithLabelValues(tier, string(limitType)).Inc()
		logger.WithCategory(logger.CategoryOperational).
			WithLogEvent("entitlements.shadow").
			WithFields(map[string]interface{}{
				"tenant":      tenantID,
				"tier":        tier,
				"limit_type":  string(limitType),
				"limit_value": limit,
			}).
			Info("entitlements shadow: plan limit would apply (not enforced)")
	}
}

// ResetShadowReportingForTest clears the once-per-pair dedupe.
func ResetShadowReportingForTest() { shadowSeen = sync.Map{} }

// --- Database-backed resolver -------------------------------------------

// dbPlanTierResolver resolves a tenant to its organization on the platform
// database (the gateway already mounts EVS_PLATFORM_DSN, so no new credential
// is needed). It answers both the plan tier and the organization scope a cap
// is counted over, from one cached lookup.
//
// The tenant id may be the organization id, a managed instance id or a legacy
// tenant_config instance id. Resolving only organizations.id, as this resolver
// once did, left every tenant that runs under an instance id unresolved, and
// an unresolved tenant runs with no limits at all.
type dbPlanTierResolver struct {
	db    *sqlx.DB
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]planCacheEntry
	// lastGood is the most recent successful answer per tenant. A failed
	// lookup (platform DB outage) serves it for up to staleScopeMaxAge, so a
	// paying organization is not dropped to Starter caps by an outage.
	lastGood map[string]staleScope
}

type staleScope struct {
	scope OrgScope
	at    time.Time
}

// staleScopeMaxAge bounds how long a last known good answer may stand in
// for a failed lookup.
const staleScopeMaxAge = 24 * time.Hour

type planCacheEntry struct {
	scope     OrgScope
	err       error
	expiresAt time.Time
}

// NewDBPlanTierResolver builds a caching resolver over the platform database.
// ttl bounds how long a plan or instance change takes to take effect; zero
// picks a conservative default. The result also implements OrgScopeResolver.
func NewDBPlanTierResolver(db *sqlx.DB, ttl time.Duration) PlanTierResolver {
	if db == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &dbPlanTierResolver{db: db, ttl: ttl, cache: make(map[string]planCacheEntry), lastGood: make(map[string]staleScope)}
}

// PlanTier returns the tenant's organization plan tier. Unknown, contested or
// unavailable tenants report ok=false, which keeps the managed bypass rather
// than inventing a tier. Never fail a request because billing metadata was
// unavailable.
func (r *dbPlanTierResolver) PlanTier(ctx context.Context, tenantID string) (string, bool) {
	scope, err := r.ResolveOrgScope(ctx, tenantID)
	if err != nil || scope.Tier == "" {
		return "", false
	}
	return scope.Tier, true
}

// ResolveOrgScope implements OrgScopeResolver with a per-tenant TTL cache.
// "No organization" and "contested" answers are cached so a missing
// organization costs one lookup per TTL; transient errors are not cached.
func (r *dbPlanTierResolver) ResolveOrgScope(ctx context.Context, tenantID string) (OrgScope, error) {
	tenantID = strings.ToLower(strings.TrimSpace(tenantID))
	if entry, ok := r.lookup(tenantID); ok {
		return entry.scope, entry.err
	}
	scope, err := resolveOrgScopeFromDB(ctx, r, tenantID)
	if err != nil && !errors.Is(err, billingidentity.ErrNoOrganization) && !errors.Is(err, billingidentity.ErrContestedTenant) {
		// A failed lookup (database down, cancelled caller) says nothing
		// about the tenant: never cache it, so the next request retries.
		// Serve the last known good answer if it is recent enough.
		if stale, ok := r.recentGood(tenantID); ok {
			logger.WithFields("tenant", tenantID, "age", timeNow().Sub(stale.at).Round(time.Second).String()).
				Warn("entitlements: organization lookup failed; using the last known good plan")
			return stale.scope, nil
		}
		return OrgScope{}, err
	}
	r.store(tenantID, scope, err)
	return scope, err
}

func (r *dbPlanTierResolver) recentGood(tenantID string) (staleScope, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stale, ok := r.lastGood[tenantID]
	if !ok || timeNow().Sub(stale.at) > staleScopeMaxAge {
		return staleScope{}, false
	}
	return stale, true
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

func (r *dbPlanTierResolver) store(tenantID string, scope OrgScope, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[tenantID] = planCacheEntry{scope: scope, err: err, expiresAt: timeNow().Add(r.ttl)}
	if err == nil {
		if r.lastGood == nil {
			r.lastGood = make(map[string]staleScope)
		}
		r.lastGood[tenantID] = staleScope{scope: scope, at: timeNow()}
	} else {
		// A definite "no organization" or "contested" answer supersedes any
		// earlier one: never serve a stale scope for it.
		delete(r.lastGood, tenantID)
	}
}

// timeNow is a seam for tests.
var timeNow = time.Now

// --- Enforcement classes ---------------------------------------------------

// Plan caps come in two classes with separate switches (owner decision
// 2026-10-06):
//
//   - creation caps (AGENTS, PERSISTENT_AGENTS, DATASET_ITEMS,
//     EVAL_RUNS_MONTHLY, ANNOTATION_QUEUES, CHANNELS, CHANNEL_BINDINGS,
//     HOSTED_SITES, HOSTING_STORAGE_BYTES) follow the tier policy above
//     (EVS_PLAN_LIMIT_ENFORCEMENT);
//   - activity caps, which gate work on objects a customer already has,
//     follow EVS_PLAN_LIMIT_ENFORCEMENT_ACTIVITY and default to shadow.
//
// The sandbox and browser concurrency hard gates predate this policy and
// are enforced by the sandbox manager and browser pool themselves; this
// switch never loosens them.
var activityUsageTypes = map[UsageType]bool{
	UsageTypeMessagesMonthly:     true,
	UsageTypeConcurrentRunning:   true,
	UsageTypeConcurrentSandboxes: true,
	UsageTypeConcurrentBrowsers:  true,
	UsageTypeSpawnDepth:          true,
}

// IsActivityUsageType reports whether t is an activity cap.
func IsActivityUsageType(t UsageType) bool { return activityUsageTypes[t] }

var activityEnforced atomic.Bool

// SetActivityEnforcement switches the activity class between shadow (false,
// the default) and enforcing. Safe to call at runtime.
func SetActivityEnforcement(enforce bool) { activityEnforced.Store(enforce) }

// ActivityEnforced reports whether activity caps are applied.
func ActivityEnforced() bool { return activityEnforced.Load() }

// ParseActivityEnforcement parses EVS_PLAN_LIMIT_ENFORCEMENT_ACTIVITY: ""
// or "shadow" report only (the default), "enforce" applies the activity caps
// for every tier whose creation caps are enforced. Anything else is an
// error; the caller keeps shadow.
func ParseActivityEnforcement(s string) (bool, error) {
	switch normalizeTier(s) {
	case "", "shadow":
		return false, nil
	case "enforce":
		return true, nil
	}
	return false, fmt.Errorf("invalid activity enforcement mode %q (want enforce|shadow)", s)
}

// enforcedClasses returns the limits that apply for an enforced tier: the
// activity caps are removed (and reported as shadow) unless the activity
// class is enforced too.
func enforcedClasses(tenantID, tier string, limits map[UsageType]int64) map[UsageType]int64 {
	if ActivityEnforced() {
		return limits
	}
	kept := make(map[UsageType]int64, len(limits))
	shadow := map[UsageType]int64{}
	for t, v := range limits {
		if activityUsageTypes[t] {
			shadow[t] = v
			continue
		}
		kept[t] = v
	}
	reportShadowLimits(tenantID, tier, shadow)
	return kept
}
