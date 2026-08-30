package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const (
	// ProviderEverstack is the logical provider exposed for system-managed storage.
	ProviderEverstack = "everstack"
	// ManagementCustomer marks a storage connection owned by the tenant.
	ManagementCustomer = "customer"
	// ManagementSystem marks a storage connection owned by Everstack.
	ManagementSystem = "system"
)

// ManagedConnection is the internal placement record for a tenant's logical
// Everstack Storage connection. CellID and PathPrefix never cross the tenant
// API boundary.
type ManagedConnection struct {
	ConfigID   string    `db:"config_id"`
	TenantID   string    `db:"tenant_id"`
	CellID     string    `db:"cell_id"`
	PathPrefix string    `db:"path_prefix"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

// ManagedDefaultEnsurer creates or repairs the stable system-owned default for
// one authenticated tenant. Repair never changes an existing physical cell or
// prefix; moving bytes between cells requires a separate migration workflow.
type ManagedDefaultEnsurer interface {
	EnsureDefault(ctx context.Context, tenantID string) (*ManagedConnection, error)
}

// ManagedStoreResolver turns an internal cell placement into a tenant-scoped
// object store. Implementations own physical endpoints, buckets and platform
// credentials; callers provide none of those values.
type ManagedStoreResolver interface {
	ResolveManagedStore(ctx context.Context, connection ManagedConnection) (ObjectStore, error)
}

// PostgresManagedDefaults persists one stable managed storage connection per tenant.
type PostgresManagedDefaults struct {
	db     *sqlx.DB
	cellID string
}

// NewPostgresManagedDefaults creates a managed-default provisioner for new
// tenants in cellID. Existing placements remain unchanged when the bootstrap
// cell changes.
func NewPostgresManagedDefaults(db *sqlx.DB, cellID string) (*PostgresManagedDefaults, error) {
	if db == nil {
		return nil, errors.New("managed storage database is required")
	}
	cellID = strings.TrimSpace(cellID)
	if cellID == "" {
		return nil, errors.New("managed storage cell ID is required")
	}
	return &PostgresManagedDefaults{db: db, cellID: cellID}, nil
}

// ManagedDefaultConfigID derives an opaque, stable identity without embedding
// a customer-controlled tenant identifier in the public config ID.
func ManagedDefaultConfigID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://everstack.ai/storage/default/"+tenantID)).String()
}

// ManagedTenantPrefix creates a normalized, opaque and non-overlapping prefix.
func ManagedTenantPrefix(tenantID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(tenantID)))
	return "tenants/" + hex.EncodeToString(digest[:])
}

// EnsureDefault creates or repairs the tenant's managed storage connection.
func (s *PostgresManagedDefaults) EnsureDefault(ctx context.Context, tenantID string) (*ManagedConnection, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, errors.New("managed storage tenant ID is required")
	}

	var connection ManagedConnection
	err := database.RunWithTenant(ctx, s.db, tenantID, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
			"everstack-storage-default:"+tenantID,
		); err != nil {
			return fmt.Errorf("lock managed storage default: %w", err)
		}

		var existing ManagedConnection
		err := tx.GetContext(ctx, &existing, `
			SELECT id AS config_id, tenant_id, managed_cell_id AS cell_id,
			       managed_path_prefix AS path_prefix, created_at, updated_at
			FROM object_storage_configs
			WHERE tenant_id = $1 AND management_mode = 'system'
			FOR UPDATE
		`,
			tenantID,
		)
		create := errors.Is(err, sql.ErrNoRows)
		if err != nil && !create {
			return fmt.Errorf("find managed storage default: %w", err)
		}
		configID := existing.ConfigID
		cellID := existing.CellID
		prefix := existing.PathPrefix
		if create {
			configID = ManagedDefaultConfigID(tenantID)
			cellID = s.cellID
			prefix = ManagedTenantPrefix(tenantID)
		}

		if _, err := tx.ExecContext(ctx, `
		UPDATE object_storage_configs
		SET is_default = false, updated_at = NOW()
		WHERE tenant_id = $1 AND id <> $2 AND is_default = true
	`, tenantID, configID); err != nil {
			return fmt.Errorf("demote previous storage default: %w", err)
		}

		if create {
			if _, err := tx.ExecContext(ctx, `
			INSERT INTO object_storage_configs (
				id, tenant_id, provider, endpoint, region, bucket,
				access_key_id, secret_access_key, credential_ref, path_prefix,
				is_default, enabled, management_mode, managed_cell_id,
				managed_path_prefix, created_at, updated_at
			) VALUES (
				$1, $2, 'everstack', '', '', '', '', '', NULL, '',
				true, true, 'system', $3, $4, NOW(), NOW()
			)
		`, configID, tenantID, cellID, prefix); err != nil {
				return fmt.Errorf("create managed storage default: %w", err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
			UPDATE object_storage_configs
			SET provider = 'everstack', endpoint = '', region = '', bucket = '',
				access_key_id = '', secret_access_key = '', credential_ref = NULL,
				path_prefix = '', is_default = true, enabled = true,
				management_mode = 'system', updated_at = NOW()
			WHERE id = $1 AND tenant_id = $2 AND management_mode = 'system'
			  AND (
				provider IS DISTINCT FROM 'everstack'
				OR endpoint IS DISTINCT FROM ''
				OR region IS DISTINCT FROM ''
				OR bucket IS DISTINCT FROM ''
				OR access_key_id IS DISTINCT FROM ''
				OR secret_access_key IS DISTINCT FROM ''
				OR credential_ref IS NOT NULL
				OR path_prefix IS DISTINCT FROM ''
				OR is_default IS DISTINCT FROM true
				OR enabled IS DISTINCT FROM true
			  )
		`, configID, tenantID); err != nil {
				return fmt.Errorf("repair managed storage default: %w", err)
			}
		}

		if err := tx.GetContext(ctx, &connection, `
		SELECT id AS config_id, tenant_id, managed_cell_id AS cell_id,
		       managed_path_prefix AS path_prefix, created_at, updated_at
		FROM object_storage_configs
		WHERE id = $1 AND tenant_id = $2 AND management_mode = 'system'
	`, configID, tenantID); err != nil {
			return fmt.Errorf("read managed storage default: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ensure managed storage default: %w", err)
	}
	return &connection, nil
}
