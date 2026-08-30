package hosting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"

	internalcfg "github.com/everstacklabs/everstack/internal/config"
)

const (
	// UsageLimitHostedSites is the maximum number of non-deleted sites owned
	// by a tenant. A site with an unfinished first publish still reserves a
	// slot until the tenant deletes it.
	UsageLimitHostedSites = "HOSTED_SITES"
	// UsageLimitHostingStorageBytes is retained hosting storage, separate from
	// the general STORAGE_BYTES allowance used by datasets and artifacts.
	UsageLimitHostingStorageBytes = "HOSTING_STORAGE_BYTES"
)

// QuotaResource identifies which hosting allowance rejected an increase.
type QuotaResource string

const (
	QuotaResourceSites   QuotaResource = "sites"
	QuotaResourceStorage QuotaResource = "storage"
)

// TenantQuota is the hosting allowance resolved from a tenant's plan. -1 is
// unlimited; zero is a valid limit that disables that resource for a plan.
type TenantQuota struct {
	Tier            string
	MaxSites        int64
	MaxStorageBytes int64
}

// TenantUsage represents either current retained usage or a proposed
// increase. Hosting storage counts pending reservations and every finalized,
// immutable version while its site is retained.
type TenantUsage struct {
	Sites        int64
	StorageBytes int64
}

// QuotaExceededError carries enough detail for transports to return a useful
// ResourceExhausted response without matching error strings.
type QuotaExceededError struct {
	Resource QuotaResource
	Tier     string
	Limit    int64
	Current  int64
	Added    int64
}

func (e *QuotaExceededError) Error() string {
	switch e.Resource {
	case QuotaResourceSites:
		return fmt.Sprintf("%s plan hosted-site limit reached (limit %d, current %d, requested +%d)", e.Tier, e.Limit, e.Current, e.Added)
	case QuotaResourceStorage:
		return fmt.Sprintf("%s plan hosting-storage limit exceeded (limit %d bytes, current %d bytes, requested +%d bytes)", e.Tier, e.Limit, e.Current, e.Added)
	default:
		return fmt.Sprintf("%s plan hosting quota exceeded", e.Tier)
	}
}

// Check verifies that adding requested usage would remain within the quota.
// The subtraction form avoids overflow when current usage is already large.
func (q TenantQuota) Check(current, requested TenantUsage) error {
	if current.Sites < 0 || current.StorageBytes < 0 || requested.Sites < 0 || requested.StorageBytes < 0 {
		return errors.New("hosting quota usage cannot be negative")
	}
	if q.MaxSites < -1 || q.MaxStorageBytes < -1 {
		return errors.New("hosting quota limits must be -1 or non-negative")
	}
	if q.MaxSites >= 0 && requested.Sites > 0 &&
		(current.Sites >= q.MaxSites || requested.Sites > q.MaxSites-current.Sites) {
		return &QuotaExceededError{
			Resource: QuotaResourceSites,
			Tier:     q.Tier,
			Limit:    q.MaxSites,
			Current:  current.Sites,
			Added:    requested.Sites,
		}
	}
	if q.MaxStorageBytes >= 0 && requested.StorageBytes > 0 &&
		(current.StorageBytes >= q.MaxStorageBytes || requested.StorageBytes > q.MaxStorageBytes-current.StorageBytes) {
		return &QuotaExceededError{
			Resource: QuotaResourceStorage,
			Tier:     q.Tier,
			Limit:    q.MaxStorageBytes,
			Current:  current.StorageBytes,
			Added:    requested.StorageBytes,
		}
	}
	return nil
}

// QuotaResolver maps an authenticated tenant to its current plan allowance.
type QuotaResolver interface {
	Resolve(ctx context.Context, tenantID string) (TenantQuota, error)
}

// QuotaResolverFunc makes the resolver seam easy to replace in tests and in
// deployments with an external plan source.
type QuotaResolverFunc func(context.Context, string) (TenantQuota, error)

func (f QuotaResolverFunc) Resolve(ctx context.Context, tenantID string) (TenantQuota, error) {
	return f(ctx, tenantID)
}

// PlanQuotaResolver reads the canonical organization plan tier and resolves
// the two hosting-specific limits from plans.json.
type PlanQuotaResolver struct {
	db         *sqlx.DB
	fallbackDB *sqlx.DB
	plans      *internalcfg.PlansConfig
}

func NewPlanQuotaResolver(db *sqlx.DB, plans *internalcfg.PlansConfig) *PlanQuotaResolver {
	return &PlanQuotaResolver{db: db, plans: plans}
}

// NewPlanQuotaResolverWithFallback uses the canonical platform database
// first, then confirms a gateway-local organization exists only when the
// platform has no row. Local-only claim organizations are always forced to
// Free; their potentially stale local plan_tier is never trusted.
func NewPlanQuotaResolverWithFallback(db, fallbackDB *sqlx.DB, plans *internalcfg.PlansConfig) *PlanQuotaResolver {
	if fallbackDB == db {
		fallbackDB = nil
	}
	return &PlanQuotaResolver{db: db, fallbackDB: fallbackDB, plans: plans}
}

func (r *PlanQuotaResolver) Resolve(ctx context.Context, tenantID string) (TenantQuota, error) {
	if r == nil || r.db == nil || r.plans == nil {
		return TenantQuota{}, errors.New("hosting plan quotas are not configured")
	}
	if strings.TrimSpace(tenantID) == "" {
		return TenantQuota{}, errors.New("tenant id is required to resolve hosting quota")
	}

	resolveTier := func(db *sqlx.DB) (string, error) {
		var tier string
		err := db.GetContext(ctx, &tier, `
		SELECT COALESCE(NULLIF(o.plan_tier, ''), 'free')
		FROM everstack.organizations o
		WHERE o.id::text = $1
		   OR o.id IN (
		       SELECT tc.organization_id
		       FROM everstack.tenant_config tc
		       WHERE tc.instance_id::text = $1
		   )
		LIMIT 1`, tenantID)
		return tier, err
	}
	tier, err := resolveTier(r.db)
	if errors.Is(err, sql.ErrNoRows) && r.fallbackDB != nil {
		var exists bool
		err = r.fallbackDB.GetContext(ctx, &exists,
			`SELECT EXISTS(SELECT 1 FROM everstack.organizations WHERE id = $1)`, tenantID)
		if err == nil {
			if !exists {
				err = sql.ErrNoRows
			} else {
				tier = "free"
			}
		}
	}
	if err != nil {
		return TenantQuota{}, fmt.Errorf("resolve organization plan tier: %w", err)
	}
	return QuotaForTier(r.plans, tier)
}

// QuotaForTier extracts hosting limits for a plan. Empty and unknown tiers
// deliberately fall back to free so a stale billing tier cannot grant an
// accidental unlimited allowance. Missing limits fail closed.
func QuotaForTier(plans *internalcfg.PlansConfig, tier string) (TenantQuota, error) {
	if plans == nil {
		return TenantQuota{}, errors.New("plans config is required")
	}

	resolvedTier := strings.ToLower(strings.TrimSpace(tier))
	if resolvedTier == "" {
		resolvedTier = "free"
	}
	plan, ok := plans.GetPlan(resolvedTier)
	if !ok {
		resolvedTier = "free"
		plan, ok = plans.GetPlan(resolvedTier)
	}
	if !ok {
		return TenantQuota{}, errors.New("free plan is missing from plans config")
	}

	quota := TenantQuota{Tier: resolvedTier}
	var foundSites, foundStorage bool
	for _, limit := range plan.UsageLimits {
		switch limit.Type {
		case UsageLimitHostedSites:
			quota.MaxSites = limit.Value
			foundSites = true
		case UsageLimitHostingStorageBytes:
			quota.MaxStorageBytes = limit.Value
			foundStorage = true
		}
	}
	if !foundSites || !foundStorage {
		return TenantQuota{}, fmt.Errorf("plan %q must define %s and %s", resolvedTier, UsageLimitHostedSites, UsageLimitHostingStorageBytes)
	}
	if quota.MaxSites < -1 || quota.MaxStorageBytes < -1 {
		return TenantQuota{}, fmt.Errorf("plan %q has invalid hosting quota values", resolvedTier)
	}
	return quota, nil
}
