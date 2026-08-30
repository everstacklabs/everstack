package provider_config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ProviderType represents the type of provider
type ProviderType string

const (
	ProviderTypeStatic ProviderType = "static" // Fixed catalog providers
	ProviderTypeMeta   ProviderType = "meta"   // Dynamic model discovery providers
)

// Configuration represents a provider configuration stored in the database
type Configuration struct {
	ID              string            `db:"id"`
	OrganizationID  *string           `db:"organization_id"` // Owning tenant; NULL for legacy rows pre 2026-05-06 migration.
	ProviderName    string            `db:"provider_name"`
	APIKeyEncrypted string            `db:"api_key_encrypted"`
	APIKeySource    string            `db:"api_key_source"` // "yaml" or "ui"
	EnabledModels   []string          `db:"-"`              // Handled separately as JSONB
	CustomBaseURL   *string           `db:"custom_base_url"`
	CustomSettings  map[string]string `db:"-"` // Handled separately as JSONB
	IsActive        bool              `db:"is_active"`
	CreatedAt       time.Time         `db:"created_at"`
	UpdatedAt       time.Time         `db:"updated_at"`
	SyncedToYAMLAt  *time.Time        `db:"synced_to_yaml_at"`
	LastUsedAt      *time.Time        `db:"last_used_at"`

	// Catalog sync fields
	CatalogStatus   string     `db:"catalog_status"`    // "available", "configured", "active", "deprecated"
	IsFromCatalog   bool       `db:"is_from_catalog"`   // True if synced from catalog
	CatalogSyncedAt *time.Time `db:"catalog_synced_at"` // Last sync from catalog
	DeprecatedAt    *time.Time `db:"deprecated_at"`     // When marked deprecated

	// Meta-provider fields
	ProviderType           ProviderType `db:"provider_type"`            // Type of provider (static or meta)
	SupportsModelDiscovery bool         `db:"supports_model_discovery"` // Whether provider supports model discovery
	DiscoveryAPIEndpoint   *string      `db:"discovery_api_endpoint"`   // API endpoint for model discovery

	// Internal fields for JSONB handling (exported so sqlx can scan into them)
	EnabledModelsJSON  []byte `db:"enabled_models"`
	CustomSettingsJSON []byte `db:"custom_settings"`
}

// Repository handles database operations for provider configurations
type Repository struct {
	db *sqlx.DB
}

type catalogExecContext interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

// NewRepository creates a new provider configuration repository
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// GetDB returns the underlying database connection
func (r *Repository) GetDB() *sqlx.DB {
	return r.db
}

// List returns all provider configurations
func (r *Repository) List(ctx context.Context) ([]*Configuration, error) {
	query := `
		SELECT id, organization_id, provider_name, api_key_encrypted, COALESCE(api_key_source, 'yaml') as api_key_source,
		       enabled_models, custom_base_url, custom_settings, is_active, created_at, updated_at, synced_to_yaml_at,
		       COALESCE(catalog_status, '') as catalog_status, 
		       COALESCE(is_from_catalog, false) as is_from_catalog,
		       catalog_synced_at, deprecated_at,
		       COALESCE(provider_type, 'static') as provider_type,
		       COALESCE(supports_model_discovery, false) as supports_model_discovery,
		       discovery_api_endpoint,
		       last_used_at
		FROM provider_configurations
		ORDER BY provider_name ASC
	`

	var rows []Configuration
	if err := r.db.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("failed to list provider configurations: %w", err)
	}

	// Unmarshal JSONB fields
	configs := make([]*Configuration, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for provider %s: %w", rows[i].ProviderName, err)
		}
		configs = append(configs, &rows[i])
	}

	return configs, nil
}

// Get returns a provider configuration by provider name
func (r *Repository) Get(ctx context.Context, providerName string) (*Configuration, error) {
	query := `
		SELECT id, provider_name, api_key_encrypted, COALESCE(api_key_source, 'yaml') as api_key_source,
		       enabled_models, custom_base_url, custom_settings, is_active, created_at, updated_at, synced_to_yaml_at,
		       COALESCE(catalog_status, '') as catalog_status, 
		       COALESCE(is_from_catalog, false) as is_from_catalog,
		       catalog_synced_at, deprecated_at,
		       COALESCE(provider_type, 'static') as provider_type,
		       COALESCE(supports_model_discovery, false) as supports_model_discovery,
		       discovery_api_endpoint,
		       last_used_at
		FROM provider_configurations
		WHERE provider_name = $1
	`

	var config Configuration
	if err := r.db.GetContext(ctx, &config, query, providerName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("provider configuration not found: %s", providerName)
		}
		return nil, fmt.Errorf("failed to get provider configuration: %w", err)
	}

	if err := config.unmarshalJSONB(); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSONB: %w", err)
	}

	return &config, nil
}

// Exists checks if a provider configuration exists
func (r *Repository) Exists(ctx context.Context, providerName string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM provider_configurations 
			WHERE provider_name = $1
		)
	`

	var exists bool
	if err := r.db.GetContext(ctx, &exists, query, providerName); err != nil {
		return false, fmt.Errorf("failed to check if provider exists: %w", err)
	}

	return exists, nil
}

// ExistsTx checks provider existence inside the caller's catalog projection
// transaction so added-versus-updated audit classification observes the same
// database snapshot as the subsequent upsert.
func (r *Repository) ExistsTx(ctx context.Context, tx *sqlx.Tx, providerName string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM provider_configurations
			WHERE provider_name = $1
			  AND organization_id IS NULL
		)
	`
	var exists bool
	if err := tx.GetContext(ctx, &exists, query, providerName); err != nil {
		return false, fmt.Errorf("failed to check if provider exists: %w", err)
	}
	return exists, nil
}

// Upsert creates or updates a provider configuration
func (r *Repository) Upsert(ctx context.Context, config *Configuration) error {
	// Generate ID if not set
	if config.ID == "" {
		config.ID = uuid.New().String()
	}

	// Marshal JSONB fields
	if err := config.marshalJSONB(); err != nil {
		return fmt.Errorf("failed to marshal JSONB: %w", err)
	}

	// This writes instance-level (non-tenant) providers: organization_id is
	// never set here, so every row lands with organization_id IS NULL.
	// The matching arbiter is the partial unique index
	//   provider_configurations_nontenant_provider_unique
	//     ON (provider_name) WHERE organization_id IS NULL
	// (migration provider_configurations_nontenant_unique_20260601202607).
	// A bare ON CONFLICT (provider_name) cannot infer a partial index and
	// fails with SQLSTATE 42P10 since the global UNIQUE(provider_name) was
	// dropped by the org-scope migration; the predicate below is required.
	// Tenant writes go through UpsertForOrg and a separate index, so this
	// never touches per-tenant rows.
	query := `
		INSERT INTO provider_configurations
		(id, provider_name, api_key_encrypted, api_key_source, enabled_models, custom_base_url,
		 custom_settings, is_active, created_at, updated_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (provider_name) WHERE organization_id IS NULL
		DO UPDATE SET
			api_key_encrypted = EXCLUDED.api_key_encrypted,
			api_key_source = EXCLUDED.api_key_source,
			enabled_models = EXCLUDED.enabled_models,
			custom_base_url = EXCLUDED.custom_base_url,
			custom_settings = EXCLUDED.custom_settings,
			is_active = EXCLUDED.is_active,
			updated_at = EXCLUDED.updated_at,
			last_used_at = COALESCE(EXCLUDED.last_used_at, provider_configurations.last_used_at)
		RETURNING id, created_at, updated_at, last_used_at
	`

	now := time.Now()
	if config.CreatedAt.IsZero() {
		config.CreatedAt = now
	}
	config.UpdatedAt = now

	var returned struct {
		ID         string     `db:"id"`
		CreatedAt  time.Time  `db:"created_at"`
		UpdatedAt  time.Time  `db:"updated_at"`
		LastUsedAt *time.Time `db:"last_used_at"`
	}

	err := r.db.GetContext(ctx, &returned, query,
		config.ID,
		config.ProviderName,
		config.APIKeyEncrypted,
		config.APIKeySource,
		config.EnabledModelsJSON,
		config.CustomBaseURL,
		config.CustomSettingsJSON,
		config.IsActive,
		config.CreatedAt,
		config.UpdatedAt,
		config.LastUsedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert provider configuration: %w", err)
	}

	config.ID = returned.ID
	config.CreatedAt = returned.CreatedAt
	config.UpdatedAt = returned.UpdatedAt
	config.LastUsedAt = returned.LastUsedAt

	return nil
}

// Delete removes a provider configuration
func (r *Repository) Delete(ctx context.Context, providerName string) error {
	query := `
		DELETE FROM provider_configurations
		WHERE provider_name = $1
	`

	result, err := r.db.ExecContext(ctx, query, providerName)
	if err != nil {
		return fmt.Errorf("failed to delete provider configuration: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("provider configuration not found: %s", providerName)
	}

	return nil
}

// UpdateSyncTime updates the synced_to_yaml_at timestamp
func (r *Repository) UpdateSyncTime(ctx context.Context, providerName string) error {
	query := `
		UPDATE provider_configurations
		SET synced_to_yaml_at = $1
		WHERE provider_name = $2
	`

	_, err := r.db.ExecContext(ctx, query, time.Now(), providerName)
	if err != nil {
		return fmt.Errorf("failed to update sync time: %w", err)
	}

	return nil
}

// GetLastSyncTime returns the last sync time for a provider
func (r *Repository) GetLastSyncTime(ctx context.Context, providerName string) (*time.Time, error) {
	query := `
		SELECT synced_to_yaml_at
		FROM provider_configurations
		WHERE provider_name = $1
	`

	var syncTime *time.Time
	if err := r.db.GetContext(ctx, &syncTime, query, providerName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get last sync time: %w", err)
	}

	return syncTime, nil
}

// ListActive returns only active provider configurations
func (r *Repository) ListActive(ctx context.Context) ([]*Configuration, error) {
	query := `
		SELECT id, organization_id, provider_name, api_key_encrypted, enabled_models, custom_base_url,
		       custom_settings, is_active, created_at, updated_at, synced_to_yaml_at,
		       COALESCE(provider_type, 'static') as provider_type,
		       COALESCE(supports_model_discovery, false) as supports_model_discovery,
		       discovery_api_endpoint,
		       last_used_at
		FROM provider_configurations
		WHERE is_active = true
		ORDER BY provider_name ASC
	`

	var rows []Configuration
	if err := r.db.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("failed to list active provider configurations: %w", err)
	}

	// Unmarshal JSONB fields
	configs := make([]*Configuration, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for provider %s: %w", rows[i].ProviderName, err)
		}
		configs = append(configs, &rows[i])
	}

	return configs, nil
}

// marshalJSONB converts Go types to JSONB bytes
func (c *Configuration) marshalJSONB() error {
	// Marshal enabled models
	if c.EnabledModels == nil {
		c.EnabledModels = []string{}
	}
	modelsJSON, err := json.Marshal(c.EnabledModels)
	if err != nil {
		return fmt.Errorf("failed to marshal enabled_models: %w", err)
	}
	c.EnabledModelsJSON = modelsJSON

	// Marshal custom settings
	if c.CustomSettings == nil {
		c.CustomSettings = make(map[string]string)
	}
	settingsJSON, err := json.Marshal(c.CustomSettings)
	if err != nil {
		return fmt.Errorf("failed to marshal custom_settings: %w", err)
	}
	c.CustomSettingsJSON = settingsJSON

	return nil
}

// unmarshalJSONB converts JSONB bytes to Go types
func (c *Configuration) unmarshalJSONB() error {
	// Unmarshal enabled models
	if len(c.EnabledModelsJSON) > 0 {
		if err := json.Unmarshal(c.EnabledModelsJSON, &c.EnabledModels); err != nil {
			return fmt.Errorf("failed to unmarshal enabled_models: %w", err)
		}
	} else {
		c.EnabledModels = []string{}
	}

	// Unmarshal custom settings
	if len(c.CustomSettingsJSON) > 0 {
		if err := json.Unmarshal(c.CustomSettingsJSON, &c.CustomSettings); err != nil {
			return fmt.Errorf("failed to unmarshal custom_settings: %w", err)
		}
	} else {
		c.CustomSettings = make(map[string]string)
	}

	return nil
}

// UpsertFromCatalog inserts or updates a provider from catalog sync.
//
// Catalog metadata is global (organization_id IS NULL), so this shares ONE
// non-tenant row per provider with the YAML/instance config written by
// Upsert. The conflict target is the partial unique index
// `provider_configurations_nontenant_provider_unique` defined in migration
// provider_configurations_nontenant_unique_20260601202607:
//
//	(provider_name) WHERE organization_id IS NULL.
//
// Using the same arbiter as Upsert restores the pre-org-scope invariant that
// readers assume (one non-tenant row per provider). The DO UPDATE below only
// touches catalog fields, so a real API key set by Upsert survives a later
// catalog sync. The predicate still excludes organization_id IS NOT NULL, so
// per-tenant rows keep their own index and never collide with this row.
func (r *Repository) UpsertFromCatalog(ctx context.Context, providerName string, catalogStatus string) error {
	return upsertProviderFromCatalog(ctx, r.db, providerName, catalogStatus)
}

// UpsertFromCatalogTx applies a catalog provider update inside the caller's
// release transaction.
func (r *Repository) UpsertFromCatalogTx(ctx context.Context, tx *sqlx.Tx, providerName string, catalogStatus string) error {
	return upsertProviderFromCatalog(ctx, tx, providerName, catalogStatus)
}

func upsertProviderFromCatalog(ctx context.Context, executor catalogExecContext, providerName string, catalogStatus string) error {
	now := time.Now()

	query := `
		INSERT INTO provider_configurations (
			id, provider_name, api_key_encrypted, enabled_models, custom_settings,
			is_active, catalog_status, is_from_catalog, catalog_synced_at,
			created_at, updated_at
		) VALUES (
			$1, $2, '', '[]'::jsonb, '{}'::jsonb,
			false, $3, true, $4,
			$5, $6
		)
		ON CONFLICT (provider_name) WHERE organization_id IS NULL
		DO UPDATE SET
			catalog_status = $3,
			is_from_catalog = true,
			catalog_synced_at = $4,
			updated_at = $6
	`

	_, err := executor.ExecContext(ctx, query,
		uuid.New().String(), providerName, catalogStatus, &now, now, now)

	if err != nil {
		return fmt.Errorf("failed to upsert provider from catalog: %w", err)
	}

	return nil
}

// MarkDeprecated marks a provider as deprecated
func (r *Repository) MarkDeprecated(ctx context.Context, providerName string) error {
	now := time.Now()

	query := `
		UPDATE provider_configurations
		SET catalog_status = 'deprecated',
		    deprecated_at = $1,
		    updated_at = $2
		WHERE provider_name = $3 AND is_from_catalog = true
	`

	result, err := r.db.ExecContext(ctx, query, &now, now, providerName)
	if err != nil {
		return fmt.Errorf("failed to mark provider as deprecated: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("provider not found or not from catalog: %s", providerName)
	}

	return nil
}

// ListAllWithCatalogStatus returns all provider configurations with their catalog status
func (r *Repository) ListAllWithCatalogStatus(ctx context.Context) ([]*Configuration, error) {
	query := `
		SELECT id, organization_id, provider_name, api_key_encrypted, enabled_models, custom_base_url,
		       custom_settings, is_active, created_at, updated_at, synced_to_yaml_at,
		       COALESCE(catalog_status, 'configured') as catalog_status,
		       COALESCE(is_from_catalog, false) as is_from_catalog,
		       catalog_synced_at, deprecated_at,
		       COALESCE(provider_type, 'static') as provider_type,
		       COALESCE(supports_model_discovery, false) as supports_model_discovery,
		       discovery_api_endpoint,
		       last_used_at
		FROM provider_configurations
		ORDER BY 
			CASE catalog_status
				WHEN 'active' THEN 1
				WHEN 'configured' THEN 2
				WHEN 'available' THEN 3
				WHEN 'deprecated' THEN 4
				ELSE 5
			END,
			provider_name ASC
	`

	var rows []Configuration
	if err := r.db.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("failed to list provider configurations with catalog status: %w", err)
	}

	// Unmarshal JSONB fields
	configs := make([]*Configuration, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for provider %s: %w", rows[i].ProviderName, err)
		}
		configs = append(configs, &rows[i])
	}

	return configs, nil
}

// ListForOrg returns provider configurations owned by a specific tenant.
// Pre-migration rows with NULL organization_id are intentionally excluded —
// every multi-tenant deployment that runs the migration must reassign the
// legacy row to a real owner before it appears in any tenant's UI again.
func (r *Repository) ListForOrg(ctx context.Context, orgID string) ([]*Configuration, error) {
	if orgID == "" {
		return nil, fmt.Errorf("org id is required")
	}
	query := `
		SELECT id, organization_id, provider_name, api_key_encrypted, COALESCE(api_key_source, 'yaml') as api_key_source,
		       enabled_models, custom_base_url, custom_settings, is_active, created_at, updated_at, synced_to_yaml_at,
		       COALESCE(catalog_status, '') as catalog_status,
		       COALESCE(is_from_catalog, false) as is_from_catalog,
		       catalog_synced_at, deprecated_at,
		       COALESCE(provider_type, 'static') as provider_type,
		       COALESCE(supports_model_discovery, false) as supports_model_discovery,
		       discovery_api_endpoint,
		       last_used_at
		FROM provider_configurations
		WHERE organization_id = $1
		ORDER BY provider_name ASC
	`
	var rows []Configuration
	if err := r.db.SelectContext(ctx, &rows, query, orgID); err != nil {
		return nil, fmt.Errorf("failed to list provider configurations for org: %w", err)
	}
	configs := make([]*Configuration, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for provider %s: %w", rows[i].ProviderName, err)
		}
		configs = append(configs, &rows[i])
	}
	return configs, nil
}

// GetForOrg fetches a tenant's provider configuration by name.
func (r *Repository) GetForOrg(ctx context.Context, orgID, providerName string) (*Configuration, error) {
	if orgID == "" {
		return nil, fmt.Errorf("org id is required")
	}
	query := `
		SELECT id, organization_id, provider_name, api_key_encrypted, COALESCE(api_key_source, 'yaml') as api_key_source,
		       enabled_models, custom_base_url, custom_settings, is_active, created_at, updated_at, synced_to_yaml_at,
		       COALESCE(catalog_status, '') as catalog_status,
		       COALESCE(is_from_catalog, false) as is_from_catalog,
		       catalog_synced_at, deprecated_at,
		       COALESCE(provider_type, 'static') as provider_type,
		       COALESCE(supports_model_discovery, false) as supports_model_discovery,
		       discovery_api_endpoint,
		       last_used_at
		FROM provider_configurations
		WHERE organization_id = $1 AND provider_name = $2
	`
	var config Configuration
	if err := r.db.GetContext(ctx, &config, query, orgID, providerName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("provider configuration not found: %s", providerName)
		}
		return nil, fmt.Errorf("failed to get provider configuration: %w", err)
	}
	if err := config.unmarshalJSONB(); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSONB: %w", err)
	}
	return &config, nil
}

// ExistsForOrg reports whether a tenant has configured a given provider.
func (r *Repository) ExistsForOrg(ctx context.Context, orgID, providerName string) (bool, error) {
	if orgID == "" {
		return false, fmt.Errorf("org id is required")
	}
	var exists bool
	err := r.db.GetContext(ctx, &exists, `
		SELECT EXISTS(
			SELECT 1 FROM provider_configurations
			WHERE organization_id = $1 AND provider_name = $2
		)
	`, orgID, providerName)
	if err != nil {
		return false, fmt.Errorf("failed to check provider existence: %w", err)
	}
	return exists, nil
}

// UpsertForOrg creates or updates a provider configuration, scoped to the
// caller's tenant. The (organization_id, provider_name) pair is the unique
// key — different tenants can each configure their own OpenAI / Anthropic /
// etc. without colliding, and a tenant updating their existing row never
// touches another tenant's row.
func (r *Repository) UpsertForOrg(ctx context.Context, orgID string, config *Configuration) error {
	if orgID == "" {
		return fmt.Errorf("org id is required")
	}
	if config.ID == "" {
		config.ID = uuid.New().String()
	}
	config.OrganizationID = &orgID
	if err := config.marshalJSONB(); err != nil {
		return fmt.Errorf("failed to marshal JSONB: %w", err)
	}
	now := time.Now()
	if config.CreatedAt.IsZero() {
		config.CreatedAt = now
	}
	config.UpdatedAt = now

	// The unique index this matches is partial:
	//   CREATE UNIQUE INDEX provider_configurations_org_provider_unique
	//     ON provider_configurations (organization_id, provider_name)
	//     WHERE organization_id IS NOT NULL
	// Postgres requires the ON CONFLICT inference predicate to match the
	// partial index's WHERE clause exactly; without it, the planner reports
	// `there is no unique or exclusion constraint matching the ON CONFLICT
	// specification` (SQLSTATE 42P10) and the API key save fails. orgID is
	// validated non-empty above so the predicate is always satisfied here.
	query := `
		INSERT INTO provider_configurations
		(id, organization_id, provider_name, api_key_encrypted, api_key_source, enabled_models, custom_base_url,
		 custom_settings, is_active, created_at, updated_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (organization_id, provider_name) WHERE organization_id IS NOT NULL
		DO UPDATE SET
			api_key_encrypted = EXCLUDED.api_key_encrypted,
			api_key_source    = EXCLUDED.api_key_source,
			enabled_models    = EXCLUDED.enabled_models,
			custom_base_url   = EXCLUDED.custom_base_url,
			custom_settings   = EXCLUDED.custom_settings,
			is_active         = EXCLUDED.is_active,
			updated_at        = EXCLUDED.updated_at,
			last_used_at      = COALESCE(EXCLUDED.last_used_at, provider_configurations.last_used_at)
		RETURNING id, created_at, updated_at, last_used_at
	`

	var returned struct {
		ID         string     `db:"id"`
		CreatedAt  time.Time  `db:"created_at"`
		UpdatedAt  time.Time  `db:"updated_at"`
		LastUsedAt *time.Time `db:"last_used_at"`
	}
	err := r.db.GetContext(ctx, &returned, query,
		config.ID,
		orgID,
		config.ProviderName,
		config.APIKeyEncrypted,
		config.APIKeySource,
		config.EnabledModelsJSON,
		config.CustomBaseURL,
		config.CustomSettingsJSON,
		config.IsActive,
		config.CreatedAt,
		config.UpdatedAt,
		config.LastUsedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert provider configuration: %w", err)
	}
	config.ID = returned.ID
	config.CreatedAt = returned.CreatedAt
	config.UpdatedAt = returned.UpdatedAt
	config.LastUsedAt = returned.LastUsedAt
	return nil
}

// DeleteForOrg removes a tenant's provider configuration.
func (r *Repository) DeleteForOrg(ctx context.Context, orgID, providerName string) error {
	if orgID == "" {
		return fmt.Errorf("org id is required")
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM provider_configurations WHERE organization_id = $1 AND provider_name = $2`,
		orgID, providerName)
	if err != nil {
		return fmt.Errorf("failed to delete provider configuration: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider configuration not found: %s", providerName)
	}
	return nil
}

// UpdateLastUsedAt updates the last_used_at timestamp for a provider
func (r *Repository) UpdateLastUsedAt(ctx context.Context, providerName string) error {
	query := `
		UPDATE provider_configurations
		SET last_used_at = $1
		WHERE provider_name = $2
	`

	result, err := r.db.ExecContext(ctx, query, time.Now(), providerName)
	if err != nil {
		return fmt.Errorf("failed to update last used at: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		// This is fine, might be a static provider or not configured yet
		return nil
	}

	return nil
}
