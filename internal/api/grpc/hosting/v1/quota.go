package v1

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/hosting"
)

const tenantQuotaLockPrefix = "hosting-quota:"

func (s *Server) resolveTenantQuota(ctx context.Context, tenantID string) (hosting.TenantQuota, bool, error) {
	if tenantID == "" || s.quotaResolver == nil {
		return hosting.TenantQuota{}, false, nil
	}
	quota, err := s.quotaResolver.Resolve(ctx, tenantID)
	if errors.Is(err, enterprise.ErrLimitCheckUnavailable) {
		// The organization-wide quota cannot be checked right now: refuse
		// the publish with a retryable error rather than a partial count.
		return hosting.TenantQuota{}, false, connect.NewError(connect.CodeUnavailable, enterprise.ErrLimitCheckUnavailable)
	}
	if err != nil {
		return hosting.TenantQuota{}, false, fmt.Errorf("resolve hosting plan quota: %w", err)
	}
	return quota, true, nil
}

// enforceTenantQuotaTx serializes every quota-increasing operation for one
// tenant, reads retained usage while holding that lock, and rejects the
// increase before any site/version write occurs. The transaction must remain
// open until the caller's write commits so concurrent replicas cannot both
// pass the same final slot or byte allowance.
func enforceTenantQuotaTx(
	ctx context.Context,
	tx *sqlx.Tx,
	tenantID string,
	quota hosting.TenantQuota,
	requested hosting.TenantUsage,
) error {
	// On a managed gateway the quota is the organization's: lock on the
	// organization and count every tenant id it owns.
	lockKey, tenantIDs := tenantID, []string{tenantID}
	if quota.ScopeKey != "" && len(quota.ScopeTenantIDs) > 0 {
		lockKey, tenantIDs = quota.ScopeKey, quota.ScopeTenantIDs
	}
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		tenantQuotaLockPrefix+lockKey,
	); err != nil {
		return fmt.Errorf("lock hosting quota: %w", err)
	}

	var current hosting.TenantUsage
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COUNT(DISTINCT s.id) FILTER (WHERE s.status <> 'deleted')::bigint AS sites,
			COALESCE(SUM(v.total_bytes) FILTER (
				WHERE v.status IN ('pending', 'finalized')
			), 0)::bigint AS storage_bytes
		FROM sites s
		LEFT JOIN site_versions v ON v.site_id = s.id
		WHERE s.tenant_id::text = ANY($1::text[])`,
		pq.Array(tenantIDs),
	).Scan(&current.Sites, &current.StorageBytes); err != nil {
		return fmt.Errorf("read hosting quota usage: %w", err)
	}
	return quota.Check(current, requested)
}

func retainedSiteStorageTx(ctx context.Context, tx *sqlx.Tx, siteID string) (int64, error) {
	var storageBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_bytes), 0)::bigint
		FROM site_versions
		WHERE site_id = $1 AND status IN ('pending', 'finalized')`,
		siteID,
	).Scan(&storageBytes); err != nil {
		return 0, fmt.Errorf("read claimed site storage: %w", err)
	}
	return storageBytes, nil
}

func quotaConnectError(err error) error {
	var exceeded *hosting.QuotaExceededError
	if errors.As(err, &exceeded) {
		cErr := connect.NewError(connect.CodeResourceExhausted, exceeded)
		// Same structured detail as every other plan cap, so the UI renders
		// one upgrade path for all of them.
		if detail, dErr := connect.NewErrorDetail(&errdetails.ErrorInfo{
			Reason: "plan_limit_reached",
			Domain: "everstack.ai",
			Metadata: map[string]string{
				"limit_type":  exceeded.UsageType(),
				"limit":       strconv.FormatInt(exceeded.Limit, 10),
				"used":        strconv.FormatInt(exceeded.Current, 10),
				"requested":   strconv.FormatInt(exceeded.Added, 10),
				"tier":        exceeded.Tier,
				"upgrade_url": hosting.UpgradeURL,
			},
		}); dErr == nil {
			cErr.AddDetail(detail)
		}
		return cErr
	}
	return err
}
