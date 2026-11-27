package serve

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// managedSandboxConcurrencyScope widens a concurrency cap from one tenant id
// to its organization on a managed gateway. It reuses the entitlement
// resolver's cached organization lookup (same aliases, same refusal of
// contested or unknown ids) and reports ok=false when no organization scope
// applies, which keeps the per-tenant count.
func managedSandboxConcurrencyScope(ctx context.Context, tenantID string) (sandbox.TenantScope, bool) {
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	scope := enterprise.ResolveCountScope(lookupCtx, tenantID)
	if !strings.HasPrefix(scope.Key, "org:") {
		return sandbox.TenantScope{}, false
	}
	return sandbox.TenantScope{
		Key:        scope.Key,
		Aliases:    scope.Aliases,
		ShadowOnly: !orgConcurrencyEnforced(lookupCtx, tenantID),
	}, true
}

// orgConcurrencyEnforced decides whether organization-wide sandbox and
// browser counting refuses or only reports. CONCURRENT_SANDBOXES and
// CONCURRENT_BROWSERS are activity caps: they are enforced only when
// EVS_PLAN_LIMIT_ENFORCEMENT_ACTIVITY=enforce AND the organization's tier is
// enforced under EVS_PLAN_LIMIT_ENFORCEMENT. Otherwise the scope is shadow:
// refusals come from the pre-existing per-tenant hard gate, and the
// organization-wide result is logged.
//
// The activity switch is read here with the same values the entitlement
// layer uses ("" or "shadow" off, "enforce" on); anything else stays shadow.
func orgConcurrencyEnforced(ctx context.Context, tenantID string) bool {
	if strings.ToLower(strings.TrimSpace(os.Getenv("EVS_PLAN_LIMIT_ENFORCEMENT_ACTIVITY"))) != "enforce" {
		return false
	}
	tier, ok := enterprise.ManagedPlanTier(ctx, tenantID)
	return ok && enterprise.TierEnforced(tier)
}
