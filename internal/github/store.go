package github

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/encryption"
)

// Installation represents a stored GitHub App installation.
type Installation struct {
	ID                  int64           `db:"id" json:"id"`
	TenantID            string          `db:"tenant_id" json:"tenant_id"`
	InstallationID      int64           `db:"installation_id" json:"installation_id"`
	AccountLogin        string          `db:"account_login" json:"account_login"`
	AccountType         string          `db:"account_type" json:"account_type"`
	AppID               int64           `db:"app_id" json:"app_id"`
	Permissions         json.RawMessage `db:"permissions" json:"permissions"`
	RepositorySelection string          `db:"repository_selection" json:"repository_selection"`
	Status              string          `db:"status" json:"status"`
	InstalledBy         *string         `db:"installed_by" json:"installed_by,omitempty"`
	CreatedAt           time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time       `db:"updated_at" json:"updated_at"`
}

// AppRecord stores per-tenant GitHub App credentials created via manifest flow.
type AppRecord struct {
	ID                     int64     `db:"id"`
	TenantID               string    `db:"tenant_id"`
	AppID                  int64     `db:"app_id"`
	AppSlug                string    `db:"app_slug"`
	AppName                string    `db:"app_name"`
	PrivateKeyEncrypted    string    `db:"private_key_encrypted"`
	WebhookSecretEncrypted string    `db:"webhook_secret_encrypted"`
	WebhookKey             string    `db:"webhook_key"`
	SetupURL               string    `db:"setup_url"`
	HTMLURL                string    `db:"html_url"`
	Status                 string    `db:"status"`
	CreatedAt              time.Time `db:"created_at"`
	UpdatedAt              time.Time `db:"updated_at"`
}

// ManifestSession stores one-time state for GitHub App manifest start/callback.
type ManifestSession struct {
	State      string     `db:"state"`
	TenantID   string     `db:"tenant_id"`
	WebhookKey string     `db:"webhook_key"`
	ReturnTo   string     `db:"return_to"`
	ExpiresAt  time.Time  `db:"expires_at"`
	UsedAt     *time.Time `db:"used_at"`
	CreatedAt  time.Time  `db:"created_at"`
}

// Store handles database operations for GitHub App integrations.
type Store struct {
	db *sqlx.DB
}

var (
	// ErrStoreNotConfigured indicates the GitHub store was created without a DB.
	ErrStoreNotConfigured = errors.New("github: store database is not configured")
	// ErrSchemaNotReady indicates required GitHub tables are not available yet.
	ErrSchemaNotReady = errors.New("github: schema not ready")
	// ErrGitHubAppNotFound indicates no GitHub app credentials found for tenant/key.
	ErrGitHubAppNotFound = errors.New("github: app not found")
	// ErrManifestSessionNotFound indicates no valid manifest state exists.
	ErrManifestSessionNotFound = errors.New("github: manifest session not found or expired")
)

// NewStore creates a new GitHub integration store.
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

func (s *Store) ensureDB() error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	return nil
}

func (s *Store) encryptionService() (*encryption.Service, error) {
	secret := strings.TrimSpace(apikey.GetSecret())
	if secret == "" {
		return nil, fmt.Errorf("github: encryption secret is not configured")
	}
	return encryption.NewService(secret)
}

func wrapSchemaError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 42P01 = undefined_table, 3F000 = invalid_schema_name
		if pgErr.Code == "42P01" || pgErr.Code == "3F000" {
			return fmt.Errorf("%w: %v", ErrSchemaNotReady, err)
		}
	}

	// Postgres undefined_table (42P01) and generic table-missing text fallback.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "42p01") || strings.Contains(msg, "3f000") || strings.Contains(msg, "does not exist") {
		if strings.Contains(msg, "github_app_installations") ||
			strings.Contains(msg, "github_webhook_deliveries") ||
			strings.Contains(msg, "github_apps") ||
			strings.Contains(msg, "github_manifest_sessions") {
			return fmt.Errorf("%w: %v", ErrSchemaNotReady, err)
		}
		if strings.Contains(msg, `schema "everstack" does not exist`) {
			return fmt.Errorf("%w: %v", ErrSchemaNotReady, err)
		}
	}
	return err
}

// UpsertInstallation creates or updates a GitHub App installation.
// Uses installation_id as the unique conflict key.
func (s *Store) UpsertInstallation(ctx context.Context, inst *Installation) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	const q = `
		INSERT INTO github_app_installations
			(tenant_id, installation_id, account_login, account_type, app_id,
			 permissions, repository_selection, status, installed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (installation_id) DO UPDATE SET
			tenant_id = EXCLUDED.tenant_id,
			account_login = EXCLUDED.account_login,
			account_type = EXCLUDED.account_type,
			permissions = EXCLUDED.permissions,
			repository_selection = EXCLUDED.repository_selection,
			status = EXCLUDED.status,
			updated_at = NOW()
		RETURNING id, created_at, updated_at`

	err := s.db.QueryRowxContext(ctx, q,
		inst.TenantID, inst.InstallationID, inst.AccountLogin, inst.AccountType, inst.AppID,
		inst.Permissions, inst.RepositorySelection, inst.Status, inst.InstalledBy,
	).StructScan(inst)
	return wrapSchemaError(err)
}

// ListActiveInstallations returns all active installations for a tenant.
func (s *Store) ListActiveInstallations(ctx context.Context, tenantID string) ([]Installation, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	var installations []Installation
	const q = `
		SELECT id, tenant_id, installation_id, account_login, account_type, app_id,
		       permissions, repository_selection, status, installed_by, created_at, updated_at
		FROM github_app_installations
		WHERE status = 'active'
		ORDER BY created_at DESC`

	if err := s.db.SelectContext(ctx, &installations, q); err != nil {
		return nil, wrapSchemaError(err)
	}
	return installations, nil
}

// GetInstallation returns a single installation by its GitHub installation ID.
func (s *Store) GetInstallation(ctx context.Context, installationID int64) (*Installation, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	var inst Installation
	const q = `
		SELECT id, tenant_id, installation_id, account_login, account_type, app_id,
		       permissions, repository_selection, status, installed_by, created_at, updated_at
		FROM github_app_installations
		WHERE installation_id = $1`

	if err := s.db.GetContext(ctx, &inst, q, installationID); err != nil {
		return nil, wrapSchemaError(err)
	}
	return &inst, nil
}

// GetInstallationForTenant returns an installation by its GitHub installation ID,
// scoped to a specific tenant (prevents cross-tenant access).
func (s *Store) GetInstallationForTenant(ctx context.Context, tenantID string, installationID int64) (*Installation, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	var inst Installation
	const q = `
		SELECT id, tenant_id, installation_id, account_login, account_type, app_id,
		       permissions, repository_selection, status, installed_by, created_at, updated_at
		FROM github_app_installations
		WHERE installation_id = $1 AND status = 'active'`

	if err := s.db.GetContext(ctx, &inst, q, installationID); err != nil {
		return nil, wrapSchemaError(err)
	}
	return &inst, nil
}

// RemoveInstallation soft-deletes an installation by setting status to 'removed'.
func (s *Store) RemoveInstallation(ctx context.Context, tenantID string, installationID int64) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	const q = `
		UPDATE github_app_installations
		SET status = 'removed', updated_at = NOW()
		WHERE installation_id = $1 AND status = 'active'`

	result, err := s.db.ExecContext(ctx, q, installationID)
	if err != nil {
		return wrapSchemaError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInstallationNotFound
	}
	return nil
}

// SuspendInstallation sets an installation's status to 'suspended'.
func (s *Store) SuspendInstallation(ctx context.Context, installationID int64) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	const q = `
		UPDATE github_app_installations
		SET status = 'suspended', updated_at = NOW()
		WHERE installation_id = $1 AND status = 'active'`

	_, err := s.db.ExecContext(ctx, q, installationID)
	return wrapSchemaError(err)
}

// UnsuspendInstallation sets a suspended installation back to 'active'.
func (s *Store) UnsuspendInstallation(ctx context.Context, installationID int64) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	const q = `
		UPDATE github_app_installations
		SET status = 'active', updated_at = NOW()
		WHERE installation_id = $1 AND status = 'suspended'`

	_, err := s.db.ExecContext(ctx, q, installationID)
	return wrapSchemaError(err)
}

// DeleteInstallation hard-deletes an installation (used when GitHub sends deletion event).
func (s *Store) DeleteInstallation(ctx context.Context, installationID int64) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	const q = `DELETE FROM github_app_installations WHERE installation_id = $1`
	_, err := s.db.ExecContext(ctx, q, installationID)
	return wrapSchemaError(err)
}

// CheckDeliveryID checks if a webhook delivery ID has been processed.
// Returns true if this is a new delivery (should be processed).
// Uses INSERT ON CONFLICT DO NOTHING for atomic deduplication.
func (s *Store) CheckDeliveryID(ctx context.Context, deliveryID string) (bool, error) {
	if err := s.ensureDB(); err != nil {
		return false, err
	}
	const q = `
		INSERT INTO github_webhook_deliveries (delivery_id)
		VALUES ($1)
		ON CONFLICT DO NOTHING
		RETURNING delivery_id`

	var id string
	err := s.db.QueryRowxContext(ctx, q, deliveryID).Scan(&id)
	if err != nil {
		// No rows returned means conflict (duplicate)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, wrapSchemaError(err)
	}
	return true, nil
}

// CleanupExpiredDeliveries removes delivery IDs older than 24 hours.
func (s *Store) CleanupExpiredDeliveries(ctx context.Context) (int64, error) {
	if err := s.ensureDB(); err != nil {
		return 0, err
	}
	const q = `
		DELETE FROM github_webhook_deliveries
		WHERE received_at < NOW() - INTERVAL '24 hours'`

	result, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return 0, wrapSchemaError(err)
	}
	return result.RowsAffected()
}

// CreateManifestSession stores a one-time manifest state used for callback validation.
func (s *Store) CreateManifestSession(ctx context.Context, session *ManifestSession) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	const q = `
		INSERT INTO github_manifest_sessions
			(state, tenant_id, webhook_key, return_to, expires_at)
		VALUES ($1, $2, $3, $4, $5)`

	_, err := s.db.ExecContext(ctx, q, session.State, session.TenantID, session.WebhookKey, session.ReturnTo, session.ExpiresAt)
	return wrapSchemaError(err)
}

// ConsumeManifestSession marks a manifest state as used and returns the session.
func (s *Store) ConsumeManifestSession(ctx context.Context, state string) (*ManifestSession, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	const q = `
		UPDATE github_manifest_sessions
		SET used_at = NOW()
		WHERE state = $1
		  AND used_at IS NULL
		  AND expires_at > NOW()
		RETURNING state, tenant_id, webhook_key, return_to, expires_at, used_at, created_at`

	var session ManifestSession
	err := s.db.QueryRowxContext(ctx, q, state).StructScan(&session)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrManifestSessionNotFound
		}
		return nil, wrapSchemaError(err)
	}
	return &session, nil
}

// UpsertTenantApp stores or updates encrypted app credentials for a tenant.
func (s *Store) UpsertTenantApp(ctx context.Context, tenantID string, appID int64, appSlug, appName, privateKeyPEM, webhookSecret, webhookKey, setupURL, htmlURL string) (*AppRecord, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	enc, err := s.encryptionService()
	if err != nil {
		return nil, err
	}
	privateKeyEncrypted, err := enc.Encrypt(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("github: failed to encrypt app private key: %w", err)
	}
	webhookSecretEncrypted, err := enc.Encrypt(webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("github: failed to encrypt webhook secret: %w", err)
	}

	const q = `
		INSERT INTO github_apps
			(tenant_id, app_id, app_slug, app_name, private_key_encrypted,
			 webhook_secret_encrypted, webhook_key, setup_url, html_url, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active')
		ON CONFLICT (tenant_id) DO UPDATE SET
			app_id = EXCLUDED.app_id,
			app_slug = EXCLUDED.app_slug,
			app_name = EXCLUDED.app_name,
			private_key_encrypted = EXCLUDED.private_key_encrypted,
			webhook_secret_encrypted = EXCLUDED.webhook_secret_encrypted,
			webhook_key = EXCLUDED.webhook_key,
			setup_url = EXCLUDED.setup_url,
			html_url = EXCLUDED.html_url,
			status = 'active',
			updated_at = NOW()
		RETURNING id, tenant_id, app_id, app_slug, app_name, private_key_encrypted,
		          webhook_secret_encrypted, webhook_key,
		          COALESCE(setup_url, '') AS setup_url,
		          COALESCE(html_url, '') AS html_url,
		          status, created_at, updated_at`

	var rec AppRecord
	err = s.db.QueryRowxContext(ctx, q, tenantID, appID, appSlug, appName, privateKeyEncrypted, webhookSecretEncrypted, webhookKey, setupURL, htmlURL).StructScan(&rec)
	if err != nil {
		return nil, wrapSchemaError(err)
	}
	return &rec, nil
}

// GetAppByTenant returns an app record by tenant id.
func (s *Store) GetAppByTenant(ctx context.Context, tenantID string) (*AppRecord, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	const q = `
		SELECT id, tenant_id, app_id, app_slug, app_name, private_key_encrypted,
		       webhook_secret_encrypted, webhook_key, COALESCE(setup_url, '') AS setup_url,
		       COALESCE(html_url, '') AS html_url, status, created_at, updated_at
		FROM github_apps
		WHERE status = 'active'`

	var rec AppRecord
	err := s.db.GetContext(ctx, &rec, q)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGitHubAppNotFound
		}
		return nil, wrapSchemaError(err)
	}
	return &rec, nil
}

// GetAppByWebhookKey returns an app record by webhook key.
func (s *Store) GetAppByWebhookKey(ctx context.Context, webhookKey string) (*AppRecord, error) {
	if err := s.ensureDB(); err != nil {
		return nil, err
	}
	const q = `
		SELECT id, tenant_id, app_id, app_slug, app_name, private_key_encrypted,
		       webhook_secret_encrypted, webhook_key, COALESCE(setup_url, '') AS setup_url,
		       COALESCE(html_url, '') AS html_url, status, created_at, updated_at
		FROM github_apps
		WHERE webhook_key = $1 AND status = 'active'`

	var rec AppRecord
	err := s.db.GetContext(ctx, &rec, q, webhookKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGitHubAppNotFound
		}
		return nil, wrapSchemaError(err)
	}
	return &rec, nil
}

func (s *Store) buildAppClient(rec *AppRecord) (*App, error) {
	enc, err := s.encryptionService()
	if err != nil {
		return nil, err
	}
	privateKeyPEM, err := enc.Decrypt(rec.PrivateKeyEncrypted)
	if err != nil {
		return nil, fmt.Errorf("github: failed to decrypt private key: %w", err)
	}
	webhookSecret, err := enc.Decrypt(rec.WebhookSecretEncrypted)
	if err != nil {
		return nil, fmt.Errorf("github: failed to decrypt webhook secret: %w", err)
	}
	return NewApp(rec.AppID, privateKeyPEM, webhookSecret)
}

// LoadAppClientForTenant creates a GitHub API client for a tenant's stored app.
func (s *Store) LoadAppClientForTenant(ctx context.Context, tenantID string) (*App, *AppRecord, error) {
	rec, err := s.GetAppByTenant(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	appClient, err := s.buildAppClient(rec)
	if err != nil {
		return nil, nil, err
	}
	return appClient, rec, nil
}

// LoadAppClientForWebhookKey creates a GitHub API client for webhook handling.
func (s *Store) LoadAppClientForWebhookKey(ctx context.Context, webhookKey string) (*App, *AppRecord, error) {
	rec, err := s.GetAppByWebhookKey(ctx, webhookKey)
	if err != nil {
		return nil, nil, err
	}
	appClient, err := s.buildAppClient(rec)
	if err != nil {
		return nil, nil, err
	}
	return appClient, rec, nil
}

// StartDeliveryGC runs periodic delivery ID cleanup every hour.
func (s *Store) StartDeliveryGC(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleaned, err := s.CleanupExpiredDeliveries(ctx)
				if err != nil {
					logger.WithFields("error", err.Error()).
						Warn("github: failed to cleanup expired webhook deliveries")
				} else if cleaned > 0 {
					logger.WithFields("count", cleaned).
						Debug("github: cleaned up expired webhook deliveries")
				}
			}
		}
	}()
}
