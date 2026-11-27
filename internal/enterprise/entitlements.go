package enterprise

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/plans"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// Entitlements is the resolved set of usage limits a request operates under.
// It is the code form of the resolution truth table in
// docs/design/editions-and-billing.md section 3:
//
//	dev build                  -> unlimited
//	managed tenant, plan known -> that plan's limits (from pkg/plans)
//	managed tenant, plan nil   -> unlimited (legacy bypass, incremental rollout)
//	managed tenant, tier resolved, shadow on  -> unlimited, limits logged
//	managed tenant, tier resolved, shadow off -> that plan's limits
//	active license             -> the license state's limits (grace included)
//	otherwise                  -> CE limits (= free plan)
//
// Feature gating intentionally stays with FeatureGateInterceptor and the
// license monitor (they carry per-tenant overrides the resolver cannot see);
// this type resolves LIMITS.
type Entitlements struct {
	// Tier is the effective plan tier ("free", "basic", "pro", "enterprise",
	// or "dev").
	Tier string
	// Source records which truth-table row applied: "dev", "managed-plan",
	// "managed-bypass", "license", or "ce". Useful in error messages and logs.
	Source string
	// Limits maps usage types to caps (-1 or absent = unlimited,
	// 0 = unavailable). A nil map means everything is unlimited.
	Limits map[UsageType]int64
}

// Limit returns the cap for a usage type. ok=false means unlimited.
func (e Entitlements) Limit(t UsageType) (limit int64, ok bool) {
	if e.Limits == nil {
		return -1, false
	}
	v, present := e.Limits[t]
	if !present || v < 0 {
		return -1, false
	}
	return v, true
}

// managedGateway records whether this process serves other people's tenants,
// i.e. a shared cloud gateway rather than someone's own install. Set once at
// startup from cmd/serve; a process is one or the other for its whole life.
//
// It is process state rather than a context value because the enforcement
// points that need it (entitlement resolution, licence middleware) run on
// contexts that may not descend from the one the startup flag was set on.
var managedGateway atomic.Bool

// SetManagedGateway marks this process as a managed (multi-tenant) gateway.
func SetManagedGateway(v bool) { managedGateway.Store(v) }

// ManagedGateway reports whether this process serves other people's tenants.
func ManagedGateway() bool { return managedGateway.Load() }

// ResolveEntitlements resolves the effective entitlements for a request.
// It records every outcome in the entitlementResolutions counter (EVS-6
// shadow sink: source × tier mix, no tenant labels) and delegates the
// truth-table logic to resolveEntitlements.
func ResolveEntitlements(ctx context.Context, monitor LicenseMonitor) Entitlements {
	ent := resolveEntitlements(ctx, monitor)
	ent = applyDetachedFloor(ctx, monitor, ent)
	observeEntitlementResolution(ent)
	return ent
}

// detachedFloorTypes are the caps that detached server paths (channel
// sessions, internal triggers) on a managed gateway enforced before plan
// enforcement existed, because those contexts fell through to Community
// Edition limits: agent storage uploads (STORAGE_BYTES) and channel-started
// persistent runs (CONCURRENT_RUNNING). They are the only CE caps those
// paths consulted.
var detachedFloorTypes = []UsageType{UsageTypeStorageBytes, UsageTypeConcurrentRunning}

// applyDetachedFloor keeps those caps for a server-resolved (not verified)
// tenant on a managed gateway: resolving the organization's plan may tighten
// them, never loosen them below what applied before.
func applyDetachedFloor(ctx context.Context, monitor LicenseMonitor, ent Entitlements) Entitlements {
	if IsDev() || !ManagedGateway() || contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return ent
	}
	if marked, _ := ctx.Value(serverResolvedTenantKey{}).(string); marked == "" || marked != contextkeys.GetTenantID(ctx) {
		return ent
	}
	legacy := selfHostedEntitlements(monitor)
	var limits map[UsageType]int64
	for _, t := range detachedFloorTypes {
		floor, capped := legacy.Limit(t)
		if !capped {
			continue
		}
		if current, has := ent.Limit(t); has && current <= floor {
			continue
		}
		if limits == nil {
			limits = make(map[UsageType]int64, len(ent.Limits)+len(detachedFloorTypes))
			for k, v := range ent.Limits {
				limits[k] = v
			}
		}
		limits[t] = floor
	}
	if limits != nil {
		ent.Limits = limits
	}
	return ent
}

func resolveEntitlements(ctx context.Context, monitor LicenseMonitor) Entitlements {
	if IsDev() {
		return Entitlements{Tier: "dev", Source: "dev"}
	}

	if tc := tenant.ConfigFromContext(ctx); tc != nil {
		if tc.PlanTier != nil && *tc.PlanTier != "" {
			if limits, ok := planLimitsForTier(*tc.PlanTier); ok {
				limits = enforcedClasses(tc.InstanceID, *tc.PlanTier, limits)
				return Entitlements{Tier: *tc.PlanTier, Source: "managed-plan", Limits: limits}
			}
			// Unknown tier string or unparseable plans data: fail open for
			// managed tenants (cloud availability beats gateway-local
			// enforcement; the cloud-side billing path still meters).
			logger.Warnf("entitlements: unknown managed plan tier %q, applying managed bypass", *tc.PlanTier)
		}
		return Entitlements{Tier: "", Source: "managed-bypass"}
	}

	// A managed gateway serving an identified tenant must never continue into
	// the self-hosted licensing path below. That path resolves the INSTANCE's
	// licence, and a shared cloud gateway has exactly one of those for every
	// tenant on it, so reaching it stamps whatever the instance is (in
	// practice: nothing, hence CE) across paying customers alike.
	//
	// This is the same truth-table row as the branch above, "managed tenant,
	// plan nil -> unlimited". It is reached instead of that branch only
	// because nothing populates tenant config on the gateway pod: the
	// controlplane TenantMiddleware that calls WithTenantConfig runs in the
	// services pod. Until a gateway-side resolver lands, a tenant bound to
	// verified API-key or session evidence on a managed gateway resolves the
	// documented managed-bypass rather than silently inheriting free-plan
	// limits.
	//
	// A bare tenant id is not authority. In particular, the legacy same-origin
	// compatibility path can copy x-tenant-id from the request while marking
	// the context authenticated. HasVerifiedTenantPrincipal additionally
	// requires validated API-key or user-session evidence, so spoofed headers
	// retain the stricter default.
	if ManagedGateway() && hasEntitlementPrincipal(ctx) {
		// A gateway-side resolver can now supply the plan tier the tenant
		// config never carried. Until enforcement is switched on, report what
		// would apply and keep returning unlimited, because these tenants have
		// been running without caps and abruptly capping them would break live
		// workloads that are currently legal.
		if currentPlanResolver() == nil {
			// No platform database to resolve against: nothing is known.
			return Entitlements{Tier: "", Source: "managed-bypass"}
		}
		source := "managed-plan"
		tier, ok := resolveManagedPlanTier(ctx)
		if !ok {
			// A verified tenant that maps to no live organization (a deleted
			// instance whose key still authenticates, an orphan or contested
			// id) or whose lookup failed gets Starter caps, never unlimited.
			tier, source = "free", "managed-unresolved"
		} else if managedTenantExempt(ctx) {
			// Internal organizations (the organization exempt flag, set
			// only by migration or ops tooling) run without plan caps.
			return Entitlements{Tier: tier, Source: "managed-exempt"}
		}
		if limits, found := planLimitsForTier(tier); found {
			if !TierEnforced(tier) {
				reportShadowLimits(contextkeys.GetTenantID(ctx), tier, limits)
				return Entitlements{Tier: tier, Source: "managed-shadow"}
			}
			limits = enforcedClasses(contextkeys.GetTenantID(ctx), tier, limits)
			return Entitlements{Tier: tier, Source: source, Limits: limits}
		}
		logger.Warnf("entitlements: resolved unknown plan tier %q for managed tenant, applying managed bypass", tier)
		return Entitlements{Tier: "", Source: "managed-bypass"}
	}

	return selfHostedEntitlements(monitor)
}

// selfHostedEntitlements is the licence-or-CE resolution every non-managed
// request gets (and every detached managed request got before plan
// enforcement).
func selfHostedEntitlements(monitor LicenseMonitor) Entitlements {
	if Edition() != "ce" && monitor != nil {
		if state := monitor.GetLicenseState(); state != nil && state.Active {
			if len(state.UsageLimits) == 0 {
				// Plan data missing on an active license: fail closed to CE
				// limits until a refresh delivers the real plan
				// (editions-and-billing.md, fail-open fix).
				return Entitlements{Tier: state.Tier, Source: "license-degraded-data", Limits: ceLimitsCopy()}
			}
			limits := make(map[UsageType]int64, len(state.UsageLimits))
			for _, ul := range state.UsageLimits {
				limits[ul.Type] = ul.Limit
			}
			return Entitlements{Tier: state.Tier, Source: "license", Limits: limits}
		}
	}

	return Entitlements{Tier: "free", Source: "ce", Limits: ceLimitsCopy()}
}

// managedDisplayOnlyUsageTypes are plan limits a managed (Cloud) gateway shows
// but never enforces through the entitlement path. Token and request volume
// are BYOK usage for most tenants and are display only by owner decision
// (2026-10-06); STORAGE_BYTES is a usage-type allowance whose overage model is
// pending review, and its existing checks count per tenant id rather than per
// organization. Self-hosted licences are unaffected: this applies only to
// limits resolved from a managed organization's plan.
var managedDisplayOnlyUsageTypes = map[UsageType]bool{
	UsageTypeTokens:       true,
	UsageTypeRequests:     true,
	UsageTypeStorageBytes: true,
}

// planLimitsForTier reads a managed tier's enforceable usage limits from the
// embedded canonical plans data.
func planLimitsForTier(tier string) (map[UsageType]int64, bool) {
	cfg, err := plans.Embedded()
	if err != nil || cfg == nil {
		return nil, false
	}
	plan, ok := cfg.GetPlan(tier)
	if !ok {
		return nil, false
	}
	limits := make(map[UsageType]int64, len(plan.UsageLimits))
	for _, ul := range plan.UsageLimits {
		if managedDisplayOnlyUsageTypes[UsageType(ul.Type)] {
			continue
		}
		if ul.IsAllowance() {
			// Cloud usage allowances are priced by the billing service's
			// usage-debit loop (usage beyond them is paid from credit), never
			// enforced as an entitlement cap.
			continue
		}
		limits[UsageType(ul.Type)] = ul.Value
	}
	return limits, true
}

func ceLimitsCopy() map[UsageType]int64 {
	limits := make(map[UsageType]int64, len(CEUsageLimits))
	for k, v := range CEUsageLimits {
		limits[k] = v
	}
	return limits
}

// ErrResourceLimit tags a genuine quota refusal from the cap checks: the plan
// does not include the resource, or the organization is at its cap. Those
// errors are *LimitError, written for end users. Any other error from the
// checks (for example a failed count query, which wraps raw driver text) is an
// internal failure and must not be shown to users.
var ErrResourceLimit = errors.New("resource limit reached")

// CheckResourceLimit is the legacy creation-time gate: countQuery binds its
// own args and counts only what they select. New callers use CheckLimit,
// which counts over the whole organization on a managed gateway.
//
// Known limitation (Phase 1 follow-up in editions-and-billing.md): the check
// is COUNT-then-write at the transport layer, so concurrent requests can race
// past the cap; ReserveLimitSlot closes that.
func CheckResourceLimit(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, limitType UsageType, countQuery string, args []interface{}, delta int64, resourceName string) error {
	ent := ResolveEntitlements(ctx, monitor)
	limit, capped := ent.Limit(limitType)
	if !capped {
		return nil
	}
	c := LimitCheck{Type: limitType, Resource: resourceName, Delta: delta}
	if limit == 0 {
		return unavailableError(ent, c)
	}
	if db == nil {
		return nil
	}
	return countAgainstLimit(ctx, db, ent, limit, c, countQuery, args)
}

// countAgainstLimit runs the count and compares it with the cap.
func countAgainstLimit(ctx context.Context, db *sqlx.DB, ent Entitlements, limit int64, c LimitCheck, query string, args []interface{}) error {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var count int64
	if err := db.GetContext(queryCtx, &count, query, args...); err != nil {
		return fmt.Errorf("failed to check %s limit: %w", c.Resource, err)
	}
	if count+c.delta() > limit {
		return capError(ent, c, limit, count)
	}
	return nil
}

// ResourceUnavailable reports whether a usage type is entirely unavailable
// (limit 0) under the current entitlements, without running a count query.
// For handlers that gate creation of a resource whose own cap lives on a
// different limit type, and that therefore have nothing to count here.
func ResourceUnavailable(ctx context.Context, monitor LicenseMonitor, limitType UsageType) bool {
	limit, capped := ResolveEntitlements(ctx, monitor).Limit(limitType)
	return capped && limit == 0
}
