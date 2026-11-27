package serve

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/hosting"
)

// managedHostingQuota resolves HOSTED_SITES and HOSTING_STORAGE_BYTES from
// the tenant's organization plan on a managed gateway, counted over every
// tenant id the organization owns. While the plan is not enforced (shadow), or
// no organization resolves, hosting stays unlimited as it has been.
func managedHostingQuota(ctx context.Context, tenantID string) (hosting.TenantQuota, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ent, scope, ok, err := enterprise.EnforcedManagedPlan(lookupCtx, tenantID)
	if err != nil {
		return hosting.TenantQuota{}, err // retryable: never a partial count
	}
	if !ok {
		return hosting.TenantQuota{MaxSites: -1, MaxStorageBytes: -1}, nil
	}
	quota := hosting.TenantQuota{
		Tier:            ent.Tier,
		MaxSites:        -1,
		MaxStorageBytes: -1,
		ScopeKey:        scope.Key,
		// A deleted instance's sites keep counting until the takedown sweep
		// has removed them, so a failed takedown never frees quota.
		ScopeTenantIDs: append(append([]string(nil), scope.Aliases...), scope.DeletedAliases...),
	}
	if limit, capped := ent.Limit(enterprise.UsageTypeHostedSites); capped {
		quota.MaxSites = limit
	}
	if limit, capped := ent.Limit(enterprise.UsageTypeHostingStorageBytes); capped {
		quota.MaxStorageBytes = limit
	}
	return quota, nil
}
