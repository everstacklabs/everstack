package v1

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/hosting"
)

const tenantQuotaLockPrefix = "hosting-quota:"

func (s *Server) resolveTenantQuota(ctx context.Context, tenantID string) (hosting.TenantQuota, bool, error) {
	if tenantID == "" || s.quotaResolver == nil {
		return hosting.TenantQuota{}, false, nil
	}
	quota, err := s.quotaResolver.Resolve(ctx, tenantID)
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
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		tenantQuotaLockPrefix+tenantID,
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
		WHERE s.tenant_id = $1`,
		tenantID,
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
		return connect.NewError(connect.CodeResourceExhausted, exceeded)
	}
	return err
}
