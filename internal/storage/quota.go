package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

// QuotaEnforcer checks storage usage against plan limits.
type QuotaEnforcer struct {
	db *sqlx.DB
}

// NewQuotaEnforcer creates a new QuotaEnforcer.
func NewQuotaEnforcer(db *sqlx.DB) *QuotaEnforcer {
	return &QuotaEnforcer{db: db}
}

// Usage returns current storage usage for a tenant.
type Usage struct {
	TenantID            string `db:"tenant_id" json:"tenant_id"`
	TotalBytes          int64  `db:"total_bytes" json:"total_bytes"`
	ObjectCount         int64  `db:"object_count" json:"object_count"`
	ReservedBytes       int64  `db:"reserved_bytes" json:"reserved_bytes"`
	ReservedObjectCount int64  `db:"reserved_object_count" json:"reserved_object_count"`
}

// GetUsage returns the current storage usage for a tenant.
func (q *QuotaEnforcer) GetUsage(ctx context.Context, tenantID string) (*Usage, error) {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionUsageRead, tenantID); err != nil {
		return nil, err
	}
	var usage Usage
	err := q.db.GetContext(ctx, &usage,
		`SELECT tenant_id, total_bytes, object_count, reserved_bytes, reserved_object_count FROM object_storage_usage WHERE tenant_id = $1`,
		tenantID,
	)
	if err == sql.ErrNoRows {
		return &Usage{TenantID: tenantID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &usage, nil
}

// CheckQuota checks if a tenant can store additional bytes.
func (q *QuotaEnforcer) CheckQuota(ctx context.Context, tenantID string, additionalBytes int64, quotaBytes int64) error {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionUploadInitiate, tenantID); err != nil {
		return err
	}
	if quotaBytes <= 0 {
		// Unlimited or not configured
		return nil
	}

	usage, err := q.GetUsage(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("failed to check usage: %w", err)
	}

	if quotaExceeded(usage.TotalBytes, usage.ReservedBytes, additionalBytes, quotaBytes) {
		return fmt.Errorf("storage quota exceeded: current %d bytes + %d reserved bytes + %d bytes would exceed %d byte limit",
			usage.TotalBytes, usage.ReservedBytes, additionalBytes, quotaBytes)
	}

	return nil
}

// IncrementUsage atomically increments the storage usage for a tenant.
func (q *QuotaEnforcer) IncrementUsage(ctx context.Context, tenantID string, sizeBytes int64) error {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionUsageUpdate, tenantID); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO object_storage_usage (tenant_id, total_bytes, object_count, updated_at)
		VALUES ($1, $2, 1, NOW())
		ON CONFLICT (tenant_id) DO UPDATE SET
			total_bytes = object_storage_usage.total_bytes + $2,
			object_count = object_storage_usage.object_count + 1,
			updated_at = NOW()
	`, tenantID, sizeBytes)
	return err
}

// DecrementUsage atomically decrements the storage usage for a tenant.
func (q *QuotaEnforcer) DecrementUsage(ctx context.Context, tenantID string, sizeBytes int64) error {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionUsageUpdate, tenantID); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `
		UPDATE object_storage_usage SET
			total_bytes = GREATEST(0, total_bytes - $2),
			object_count = GREATEST(0, object_count - 1),
			updated_at = NOW()
		WHERE tenant_id = $1
	`, tenantID, sizeBytes)
	return err
}
