package enterprise

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// CE (Community Edition) usage limits — mirrors the Free tier in plans.json
// (pinned by ce_defaults_test.go). Enforced in CE builds and in EE builds
// without an active cloud license. Semantics: -1 = unlimited, 0 = resource
// not available on the free plan, > 0 = creation cap.
const (
	CEMaxAgents           = 3
	CEMaxPersistentAgents = 1
	CEMaxConcurrentAgents = 1
	CEMaxChannels         = -1 // self-hosted pays for its own sockets (ceDivergesFromFree)
	CEMaxChannelBindings  = -1
	CEMaxWorkflows        = 5 // no plans.json equivalent yet (Phase 1c)
	CEMaxSpendLimitRules  = 1 // no plans.json equivalent yet (Phase 1c)
	CESandboxMemoryMB     = 512
)

// ShouldEnforceCELimits returns true when CE usage limits should be enforced.
// This is the case when:
//   - The build is CE (untagged default), OR
//   - The build is EE but no active cloud license is present (self-hosted
//     without activation — falls back to CE limits).
//
// Dev builds (-tags dev) never enforce CE limits. Managed/cloud tenant
// requests never fall back to CE limits either: they are provisioned by the
// control plane and their plan limits are enforced by the cloud-side path,
// not by this gateway-local license machinery.
func ShouldEnforceCELimits(ctx context.Context, monitor LicenseMonitor) bool {
	if IsDev() {
		return false // dev mode: everything unlocked
	}
	if tenant.ConfigFromContext(ctx) != nil {
		return false // managed tenant: cloud plan enforcement, not CE fallback
	}
	// Same rule, reached the other way. On a managed gateway tenant config is
	// never populated, so an identified cloud tenant fell through to the CE
	// fallback below and paying customers were held to free-plan caps. This is
	// purely a loosening, so it applies in shadow mode too.
	if ManagedGateway() && contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return false
	}
	if Edition() == "ce" {
		return true
	}
	// EE build: enforce CE limits only when there's no active license
	if monitor == nil {
		return true
	}
	state := monitor.GetLicenseState()
	return state == nil || !state.Active
}

// CheckCELimit enforces a CE usage limit by counting rows against a threshold.
// The countQuery must return a single integer (COUNT(*)).
//
// Limit semantics are unified with CheckPlanLimit: -1 (or any negative) means
// unlimited, 0 means the resource is not available on the free plan, and a
// positive value is a creation cap.
//
// Pass a non-nil LicenseMonitor to skip enforcement when a cloud license is
// active. If monitor is nil, limits are always enforced.
func CheckCELimit(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, countQuery string, args []interface{}, limit int, resourceName string) error {
	if !ShouldEnforceCELimits(ctx, monitor) {
		return nil
	}
	return enforceCECap(ctx, db, countQuery, args, limit, resourceName)
}

// enforceCECap applies a CE cap unconditionally (no ShouldEnforceCELimits
// gate). Split out so CheckPlanLimit's missing-plan-data branch can fail
// closed even when the license is ACTIVE — routing that branch through
// CheckCELimit would hit the gate's "active license => skip" check and turn
// the fail-closed fallback into unlimited.
func enforceCECap(ctx context.Context, db *sqlx.DB, countQuery string, args []interface{}, limit int, resourceName string) error {
	if limit < 0 {
		return nil
	}
	if limit == 0 {
		return fmt.Errorf("%s is not available on the Community Edition — upgrade at https://everstack.ai/pricing", resourceName)
	}
	if db == nil {
		return nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var count int
	if err := db.GetContext(queryCtx, &count, countQuery, args...); err != nil {
		return fmt.Errorf("failed to check %s limit: %w", resourceName, err)
	}
	if count >= limit {
		return fmt.Errorf("%s limit reached: %d/%d — upgrade to Everstack Cloud for higher limits (https://everstack.ai/pricing)", resourceName, count, limit)
	}
	return nil
}

// CheckPlanLimit enforces a plan-tier usage limit by counting rows against
// the limit from the license state's UsageLimits array. For CE builds or
// EE without an active license, it falls back to the CE constant.
func CheckPlanLimit(ctx context.Context, db *sqlx.DB, monitor LicenseMonitor, limitType UsageType, countQuery string, args []interface{}, ceLimit int, resourceName string) error {
	// Managed/cloud tenants: the gateway-local license state is not per-tenant;
	// their plan limits are enforced by the cloud-side path (see
	// docs/design/editions-and-billing.md section 3).
	if tenant.ConfigFromContext(ctx) != nil {
		return nil
	}
	if monitor == nil {
		return CheckCELimit(ctx, db, monitor, countQuery, args, ceLimit, resourceName)
	}

	state := monitor.GetLicenseState()
	if state == nil || !state.Active {
		return CheckCELimit(ctx, db, monitor, countQuery, args, ceLimit, resourceName)
	}

	// Fail closed when limit data is missing entirely: an active license whose
	// state carries ZERO usage limits means the plan data never arrived (e.g.
	// license-service outage at startup, or an unknown tier string), not that
	// everything is unlimited. Enforce the CE cap directly (NOT via
	// CheckCELimit, whose gate would see the active license and skip) until a
	// refresh delivers the real plan. Dev builds stay unlimited.
	// A NON-empty list that merely omits this type is intentional: unlimited.
	if len(state.UsageLimits) == 0 {
		if IsDev() {
			return nil
		}
		return enforceCECap(ctx, db, countQuery, args, ceLimit, resourceName)
	}

	found := false
	limit := int64(-1)
	for _, ul := range state.UsageLimits {
		if ul.Type == limitType {
			limit = ul.Limit
			found = true
			break
		}
	}

	if !found || limit < 0 {
		return nil
	}
	if limit == 0 {
		return fmt.Errorf("%s is not available on your current plan — upgrade at https://everstack.ai/pricing", resourceName)
	}
	if db == nil {
		return nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var count int
	if err := db.GetContext(queryCtx, &count, countQuery, args...); err != nil {
		return fmt.Errorf("failed to check %s limit: %w", resourceName, err)
	}
	if int64(count) >= limit {
		return fmt.Errorf("%s limit reached: %d/%d — upgrade your plan for higher limits (https://everstack.ai/pricing)", resourceName, count, limit)
	}
	return nil
}
