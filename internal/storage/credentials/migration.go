package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var ErrLegacyCredentialIncomplete = errors.New("legacy storage credential is incomplete")

// LegacyMigrator upgrades a single plaintext connection in place. Runtime
// callers use this interface to preserve compatibility during rolling
// upgrades without exposing legacy values outside the credential backend.
type LegacyMigrator interface {
	MigrateLegacyConfig(ctx context.Context, tenantID, configID string) (reference string, remediated bool, err error)
}

// LegacyVolumeMigrator upgrades a bucket-scoped sandbox-volume credential.
type LegacyVolumeMigrator interface {
	MigrateLegacyVolumeBucket(ctx context.Context, tenantID string) (reference string, remediated bool, err error)
}

// LegacyInventory contains counts only. It deliberately never returns or logs
// credential values.
type LegacyInventory struct {
	PlaintextConfigs        int64
	IncompleteConfigs       int64
	PlaintextVolumeBuckets  int64
	IncompleteVolumeBuckets int64
	PostgresEvents          int64
}

// MigrateLegacyVolumeBucket encrypts a legacy per-tenant volume credential and
// clears every equivalent provider token field in one transaction.
func (s *PostgresStore) MigrateLegacyVolumeBucket(ctx context.Context, tenantID string) (string, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", false, fmt.Errorf("volume credential tenant is required")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("begin volume credential migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var legacy struct {
		CredentialRef   sql.NullString `db:"credential_ref"`
		AccessKeyID     string         `db:"access_key_id"`
		SecretAccessKey string         `db:"secret_access_key"`
		ProviderTokenID string         `db:"cf_token_id"`
	}
	if err := tx.GetContext(ctx, &legacy, `
		SELECT credential_ref, access_key_id, secret_access_key, cf_token_id
		FROM tenant_volume_buckets
		WHERE tenant_id = $1
		FOR UPDATE
	`, tenantID); errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrCredentialNotFound
	} else if err != nil {
		return "", false, fmt.Errorf("read legacy volume credential: %w", err)
	}

	reference := strings.TrimSpace(legacy.CredentialRef.String)
	createdReference := false
	if reference == "" {
		credentials := ProviderCredentials{
			AccessKeyID:     legacy.AccessKeyID,
			SecretAccessKey: legacy.SecretAccessKey,
			ProviderTokenID: legacy.ProviderTokenID,
		}
		if strings.TrimSpace(credentials.ProviderTokenID) == "" {
			credentials.ProviderTokenID = credentials.AccessKeyID
		}
		if err := credentials.Validate(); err != nil {
			return "", false, ErrLegacyCredentialIncomplete
		}
		reference = "storagecred_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		ciphertext, keyID, err := s.seal(tenantID, reference, credentials)
		if err != nil {
			return "", false, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO object_storage_credentials (id, tenant_id, backend, ciphertext, key_id, created_at)
			VALUES ($1, $2, 'postgres', $3, $4, NOW())
		`, reference, tenantID, ciphertext, keyID); err != nil {
			return "", false, fmt.Errorf("persist migrated volume credential: %w", err)
		}
		createdReference = true
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE tenant_volume_buckets
		SET credential_ref = $1, access_key_id = '', secret_access_key = '', cf_token_id = '', updated_at = NOW()
		WHERE tenant_id = $2
	`, reference, tenantID)
	if err != nil {
		return "", false, fmt.Errorf("scrub legacy volume credential: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return "", false, fmt.Errorf("scrub legacy volume credential: expected one row")
	}
	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit volume credential migration: %w", err)
	}
	remediated := createdReference || strings.TrimSpace(legacy.AccessKeyID) != "" ||
		strings.TrimSpace(legacy.SecretAccessKey) != "" || strings.TrimSpace(legacy.ProviderTokenID) != ""
	return reference, remediated, nil
}

// RemediationReport records only aggregate exposure counts.
type RemediationReport struct {
	Before                  LegacyInventory
	ConfigsRemediated       int
	VolumeBucketsRemediated int
	PostgresEventsRedacted  int64
	After                   LegacyInventory
	CutoverEnabled          bool
}

// MigrateLegacyConfig encrypts a legacy config and removes plaintext from the
// config and its PostgreSQL CQRS events in one transaction. The row lock makes
// concurrent runtime and maintenance migrations idempotent.
func (s *PostgresStore) MigrateLegacyConfig(ctx context.Context, tenantID, configID string) (string, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	configID = strings.TrimSpace(configID)
	if tenantID == "" || configID == "" {
		return "", false, fmt.Errorf("storage credential tenant and config are required")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("begin storage credential migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var legacy struct {
		CredentialRef   sql.NullString `db:"credential_ref"`
		AccessKeyID     string         `db:"access_key_id"`
		SecretAccessKey string         `db:"secret_access_key"`
	}
	if err := tx.GetContext(ctx, &legacy, `
		SELECT credential_ref, access_key_id, secret_access_key
		FROM object_storage_configs
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, configID, tenantID); errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrCredentialNotFound
	} else if err != nil {
		return "", false, fmt.Errorf("read legacy storage credential: %w", err)
	}

	reference := strings.TrimSpace(legacy.CredentialRef.String)
	createdReference := false
	if reference == "" {
		credentials := ProviderCredentials{
			AccessKeyID:     legacy.AccessKeyID,
			SecretAccessKey: legacy.SecretAccessKey,
		}
		if err := credentials.Validate(); err != nil {
			return "", false, ErrLegacyCredentialIncomplete
		}
		reference = "storagecred_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		ciphertext, keyID, err := s.seal(tenantID, reference, credentials)
		if err != nil {
			return "", false, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO object_storage_credentials (id, tenant_id, backend, ciphertext, key_id, created_at)
			VALUES ($1, $2, 'postgres', $3, $4, NOW())
		`, reference, tenantID, ciphertext, keyID); err != nil {
			return "", false, fmt.Errorf("persist migrated storage credential: %w", err)
		}
		createdReference = true
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE object_storage_configs
		SET credential_ref = $1, access_key_id = '', secret_access_key = '', updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, reference, configID, tenantID)
	if err != nil {
		return "", false, fmt.Errorf("scrub legacy storage config: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return "", false, fmt.Errorf("scrub legacy storage config: expected one row")
	}

	eventResult, err := tx.ExecContext(ctx, `
		UPDATE events
		SET payload = (payload - 'access_key_id' - 'secret_access_key') || jsonb_build_object('credential_ref', $1::text)
		WHERE stream = 'storage_configs'
		  AND type IN ('storage_config.created', 'storage_config.updated')
		  AND payload->>'tenant_id' = $2
		  AND payload->>'id' = $3
		  AND (payload ? 'access_key_id' OR payload ? 'secret_access_key')
	`, reference, tenantID, configID)
	if err != nil {
		return "", false, fmt.Errorf("redact legacy storage events: %w", err)
	}
	eventRows, _ := eventResult.RowsAffected()

	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit storage credential migration: %w", err)
	}
	return reference, createdReference || strings.TrimSpace(legacy.AccessKeyID) != "" || strings.TrimSpace(legacy.SecretAccessKey) != "" || eventRows > 0, nil
}

// InventoryLegacyCredentials reports the remaining PostgreSQL exposure using
// aggregate counts only.
func (s *PostgresStore) InventoryLegacyCredentials(ctx context.Context) (LegacyInventory, error) {
	return InventoryLegacyCredentials(ctx, s.db)
}

// InventoryLegacyCredentials reports plaintext exposure without requiring an
// encryption key. This lets a fresh external-backend installation prove it has
// nothing to migrate before enabling the fleet cutover gate.
func InventoryLegacyCredentials(ctx context.Context, db *sqlx.DB) (LegacyInventory, error) {
	if db == nil {
		return LegacyInventory{}, ErrStoreNotConfigured
	}
	var inventory LegacyInventory
	if err := db.QueryRowxContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE access_key_id <> '' OR secret_access_key <> '') AS plaintext_configs,
			COUNT(*) FILTER (
				WHERE credential_ref IS NULL
				  AND ((access_key_id = '') <> (secret_access_key = ''))
			) AS incomplete_configs
		FROM object_storage_configs
	`).Scan(&inventory.PlaintextConfigs, &inventory.IncompleteConfigs); err != nil {
		return LegacyInventory{}, fmt.Errorf("inventory legacy storage configs: %w", err)
	}
	if err := db.QueryRowxContext(ctx, `
		SELECT
			COUNT(*) FILTER (
				WHERE access_key_id <> '' OR secret_access_key <> '' OR cf_token_id <> ''
			) AS plaintext_volume_buckets,
			COUNT(*) FILTER (
				WHERE credential_ref IS NULL
				  AND ((access_key_id = '') <> (secret_access_key = ''))
			) AS incomplete_volume_buckets
		FROM tenant_volume_buckets
	`).Scan(&inventory.PlaintextVolumeBuckets, &inventory.IncompleteVolumeBuckets); err != nil {
		return LegacyInventory{}, fmt.Errorf("inventory legacy volume credentials: %w", err)
	}
	if err := db.QueryRowxContext(ctx, `
		SELECT COUNT(*)
		FROM events
		WHERE stream = 'storage_configs'
		  AND type IN ('storage_config.created', 'storage_config.updated')
		  AND (payload ? 'access_key_id' OR payload ? 'secret_access_key')
	`).Scan(&inventory.PostgresEvents); err != nil {
		return LegacyInventory{}, fmt.Errorf("inventory legacy storage events: %w", err)
	}
	return inventory, nil
}

func CredentialCutoverEnabledForDB(ctx context.Context, db *sqlx.DB) (bool, error) {
	if db == nil {
		return false, ErrStoreNotConfigured
	}
	var enabled bool
	if err := db.GetContext(ctx, &enabled, `
		SELECT cutover_enabled FROM object_storage_credential_state
		WHERE singleton = TRUE
	`); errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("storage credential cutover state is missing")
	} else if err != nil {
		return false, fmt.Errorf("read storage credential cutover state: %w", err)
	}
	return enabled, nil
}

func CountLivePostgresCredentials(ctx context.Context, db *sqlx.DB) (int64, error) {
	if db == nil {
		return 0, ErrStoreNotConfigured
	}
	var count int64
	if err := db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM object_storage_credentials
		WHERE backend = 'postgres' AND revoked_at IS NULL
	`); err != nil {
		return 0, fmt.Errorf("count live PostgreSQL storage credentials: %w", err)
	}
	return count, nil
}

func EnableCredentialCutoverIfClean(ctx context.Context, db *sqlx.DB) error {
	inventory, err := InventoryLegacyCredentials(ctx, db)
	if err != nil {
		return err
	}
	if inventory.PlaintextConfigs != 0 || inventory.IncompleteConfigs != 0 ||
		inventory.PlaintextVolumeBuckets != 0 || inventory.IncompleteVolumeBuckets != 0 || inventory.PostgresEvents != 0 {
		return fmt.Errorf("legacy storage credential exposure remains before cutover")
	}
	result, err := db.ExecContext(ctx, `
		UPDATE object_storage_credential_state
		SET cutover_enabled = TRUE, updated_at = NOW()
		WHERE singleton = TRUE
	`)
	if err != nil {
		return fmt.Errorf("enable storage credential cutover: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return fmt.Errorf("enable storage credential cutover: state row is missing")
	}
	return nil
}

// BackfillLegacyCredentials remediates every legacy PostgreSQL config in
// bounded batches, then strips credential fields from any remaining storage
// events. It is idempotent and safe to run alongside lazy runtime migration.
func (s *PostgresStore) BackfillLegacyCredentials(ctx context.Context, batchSize int) (RemediationReport, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	report := RemediationReport{}
	before, err := s.InventoryLegacyCredentials(ctx)
	if err != nil {
		return report, err
	}
	report.Before = before

	type candidate struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
	}
	for {
		var candidates []candidate
		if err := s.db.SelectContext(ctx, &candidates, `
			SELECT id, tenant_id
			FROM object_storage_configs
			WHERE access_key_id <> '' OR secret_access_key <> ''
			ORDER BY tenant_id, id
			LIMIT $1
		`, batchSize); err != nil {
			return report, fmt.Errorf("list legacy storage credentials: %w", err)
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			_, remediated, err := s.MigrateLegacyConfig(ctx, candidate.TenantID, candidate.ID)
			if err != nil {
				return report, fmt.Errorf("remediate legacy storage config %s: %w", candidate.ID, err)
			}
			if remediated {
				report.ConfigsRemediated++
			}
		}
	}

	for {
		var tenantIDs []string
		if err := s.db.SelectContext(ctx, &tenantIDs, `
			SELECT tenant_id
			FROM tenant_volume_buckets
			WHERE access_key_id <> '' OR secret_access_key <> '' OR cf_token_id <> ''
			ORDER BY tenant_id
			LIMIT $1
		`, batchSize); err != nil {
			return report, fmt.Errorf("list legacy volume credentials: %w", err)
		}
		if len(tenantIDs) == 0 {
			break
		}
		for _, tenantID := range tenantIDs {
			_, remediated, err := s.MigrateLegacyVolumeBucket(ctx, tenantID)
			if err != nil {
				return report, fmt.Errorf("remediate legacy volume credential for tenant %s: %w", tenantID, err)
			}
			if remediated {
				report.VolumeBucketsRemediated++
			}
		}
	}

	// Events for deleted configs cannot be joined to a current credential
	// reference, but their raw fields can and must still be removed.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE events AS e
		SET payload = e.payload - 'access_key_id' - 'secret_access_key'
		WHERE e.stream = 'storage_configs'
		  AND e.type IN ('storage_config.created', 'storage_config.updated')
		  AND (e.payload ? 'access_key_id' OR e.payload ? 'secret_access_key')
	`); err != nil {
		return report, fmt.Errorf("redact remaining legacy storage events: %w", err)
	}

	after, err := s.InventoryLegacyCredentials(ctx)
	if err != nil {
		return report, err
	}
	report.After = after
	if before.PostgresEvents > after.PostgresEvents {
		report.PostgresEventsRedacted = before.PostgresEvents - after.PostgresEvents
	}
	if after.PlaintextConfigs != 0 || after.IncompleteConfigs != 0 ||
		after.PlaintextVolumeBuckets != 0 || after.IncompleteVolumeBuckets != 0 || after.PostgresEvents != 0 {
		return report, fmt.Errorf("legacy storage credential exposure remains after remediation")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE object_storage_credential_state
		SET cutover_enabled = TRUE, updated_at = NOW()
		WHERE singleton = TRUE
	`)
	if err != nil {
		return report, fmt.Errorf("enable storage credential cutover: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return report, fmt.Errorf("enable storage credential cutover: state row is missing")
	}
	report.CutoverEnabled = true
	return report, nil
}

var _ LegacyMigrator = (*PostgresStore)(nil)
var _ LegacyVolumeMigrator = (*PostgresStore)(nil)
