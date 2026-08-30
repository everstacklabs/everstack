package enterprise

import (
	"context"
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
func ResolveEntitlements(ctx context.Context, monitor LicenseMonitor) Entitlements {
	if IsDev() {
		return Entitlements{Tier: "dev", Source: "dev"}
	}

	if tc := tenant.ConfigFromContext(ctx); tc != nil {
		if tc.PlanTier != nil && *tc.PlanTier != "" {
			if limits, ok := planLimitsForTier(*tc.PlanTier); ok {
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
	if ManagedGateway() && contextkeys.HasVerifiedTenantPrincipal(ctx) {
		// A gateway-side resolver can now supply the plan tier the tenant
		// config never carried. Until enforcement is switched on, report what
		// would apply and keep returning unlimited, because these tenants have
		// been running without caps and abruptly capping them would break live
		// workloads that are currently legal.
		if tier, ok := resolveManagedPlanTier(ctx); ok {
			if limits, found := planLimitsForTier(tier); found {
				if ShadowEnforcement() {
					reportShadowLimits(contextkeys.GetTenantID(ctx), tier, limits)
					return Entitlements{Tier: tier, Source: "managed-shadow"}
				}
				return Entitlements{Tier: tier, Source: "managed-plan", Limits: limits}
			}
			logger.Warnf("entitlements: resolved unknown plan tier %q for managed tenant, applying managed bypass", tier)
		}
		return Entitlements{Tier: "", Source: "managed-bypass"}
	}

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

// planLimitsForTier reads a tier's usage limits from the embedded canonical
// plans data.
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

// CheckResourceLimit is the creation-time quota gate, resolved through
// ResolveEntitlements. countQuery MUST be tenant-scoped (WHERE tenant_id = ..)
// — in the cloud shared schema an unscoped COUNT aggregates every tenant's
// rows — and delta is how many resources the request creates (batch RPCs pass
// the batch size, not 1).
//
// Known limitation (Phase 1 follow-up in editions-and-billing.md): the check
// is COUNT-then-write at the transport layer, so concurrent requests can race
// past the cap; canonical enforcement belongs in the command/domain
// transaction.
func CheckResourceLimit(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, limitType UsageType, countQuery string, args []interface{}, delta int64, resourceName string) error {
	ent := ResolveEntitlements(ctx, monitor)
	limit, capped := ent.Limit(limitType)
	if !capped {
		return nil
	}
	if limit == 0 {
		if ent.Source == "ce" {
			return fmt.Errorf("%s is not available on the Community Edition — upgrade at https://everstack.ai/pricing", resourceName)
		}
		return fmt.Errorf("%s is not available on your current plan — upgrade at https://everstack.ai/pricing", resourceName)
	}
	if db == nil {
		return nil
	}
	if delta < 1 {
		delta = 1
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var count int64
	if err := db.GetContext(queryCtx, &count, countQuery, args...); err != nil {
		return fmt.Errorf("failed to check %s limit: %w", resourceName, err)
	}
	if count+delta > limit {
		return fmt.Errorf("%s limit reached: %d/%d — upgrade your plan for higher limits (https://everstack.ai/pricing)", resourceName, count, limit)
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
