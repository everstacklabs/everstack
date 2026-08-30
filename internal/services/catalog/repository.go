package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// CatalogSource represents where the catalog was loaded from
type CatalogSource string

const (
	SourceFilesystem CatalogSource = "filesystem"
	SourceRemote     CatalogSource = "remote"
	SourceEmbedded   CatalogSource = "embedded"
	SourceDatabase   CatalogSource = "database"
	SourceFallback   CatalogSource = "fallback"
)

// CatalogProvider represents a provider from the catalog
type CatalogProvider struct {
	ID          string                 `db:"id"`
	Name        string                 `db:"name"`
	DisplayName string                 `db:"display_name"`
	BaseURL     string                 `db:"base_url"`
	APIVersion  string                 `db:"api_version"`
	Config      map[string]interface{} `db:"-"` // Handled separately as JSONB
	CreatedAt   time.Time              `db:"created_at"`
	UpdatedAt   time.Time              `db:"updated_at"`

	// Internal field for JSONB handling
	ConfigJSON []byte `db:"config"`
}

// CatalogModel represents a model from the catalog
type CatalogModel struct {
	ID                  string  `db:"id"`
	ProviderName        string  `db:"provider_name"`
	ModelName           string  `db:"model_name"`
	DisplayName         string  `db:"display_name"`
	MaxTokens           int     `db:"max_tokens"`
	MaxCompletionTokens int     `db:"max_completion_tokens"`
	InputCostPer1k      float64 `db:"input_cost_per_1k"`
	OutputCostPer1k     float64 `db:"output_cost_per_1k"`
	// Zero means the catalog does not price cache reads/writes for this model.
	CacheReadCostPer1k  float64                `db:"cache_read_cost_per_1k"`
	CacheWriteCostPer1k float64                `db:"cache_write_cost_per_1k"`
	Capabilities        []string               `db:"-"` // Handled separately as JSONB
	Status              string                 `db:"status"`
	Config              map[string]interface{} `db:"-"` // Handled separately as JSONB
	CreatedAt           time.Time              `db:"created_at"`
	UpdatedAt           time.Time              `db:"updated_at"`

	// Internal fields for JSONB handling
	CapabilitiesJSON []byte `db:"capabilities"`
	ConfigJSON       []byte `db:"config"`
}

// CatalogMetadataDB represents catalog version and sync information in the database
// This is different from types.CatalogMetadata which is for YAML
type CatalogMetadataDB struct {
	ID             string        `db:"id"`
	Version        string        `db:"version"`
	Source         CatalogSource `db:"source"`
	SyncedAt       time.Time     `db:"synced_at"`
	Checksum       string        `db:"checksum"`
	ModelsCount    int           `db:"models_count"`
	ProvidersCount int           `db:"providers_count"`
	CreatedAt      time.Time     `db:"created_at"`
}

// Repository handles database operations for catalog persistence
type Repository struct {
	db *sqlx.DB
}

// NewRepository creates a new catalog repository
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// GetLatestMetadata returns the most recent catalog metadata
func (r *Repository) GetLatestMetadata(ctx context.Context) (*CatalogMetadataDB, error) {
	query := `
		SELECT id, version, source, synced_at, checksum, models_count, providers_count, created_at
		FROM catalog_metadata
		ORDER BY synced_at DESC
		LIMIT 1
	`

	var metadata CatalogMetadataDB
	if err := r.db.GetContext(ctx, &metadata, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // No catalog synced yet
		}
		return nil, fmt.Errorf("failed to get latest catalog metadata: %w", err)
	}

	return &metadata, nil
}

// GetAllProviders returns all providers from the catalog
func (r *Repository) GetAllProviders(ctx context.Context) ([]*CatalogProvider, error) {
	query := `
		SELECT id, name, display_name, base_url, api_version, config, created_at, updated_at
		FROM catalog_providers
		ORDER BY name ASC
	`

	var rows []CatalogProvider
	if err := r.db.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("failed to list catalog providers: %w", err)
	}

	// Unmarshal JSONB fields
	providers := make([]*CatalogProvider, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for provider %s: %w", rows[i].Name, err)
		}
		providers = append(providers, &rows[i])
	}

	return providers, nil
}

// GetProvider returns a specific provider by name
func (r *Repository) GetProvider(ctx context.Context, name string) (*CatalogProvider, error) {
	query := `
		SELECT id, name, display_name, base_url, api_version, config, created_at, updated_at
		FROM catalog_providers
		WHERE name = $1
	`

	var provider CatalogProvider
	if err := r.db.GetContext(ctx, &provider, query, name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("provider not found: %s", name)
		}
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	if err := provider.unmarshalJSONB(); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSONB: %w", err)
	}

	return &provider, nil
}

// GetAllModels returns all models from the catalog
func (r *Repository) GetAllModels(ctx context.Context) ([]*CatalogModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, max_tokens, max_completion_tokens,
		       input_cost_per_1k, output_cost_per_1k, cache_read_cost_per_1k, cache_write_cost_per_1k,
		       capabilities, status, config,
		       created_at, updated_at
		FROM catalog_models
		ORDER BY provider_name ASC, model_name ASC
	`

	var rows []CatalogModel
	if err := r.db.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("failed to list catalog models: %w", err)
	}

	// Unmarshal JSONB fields
	models := make([]*CatalogModel, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for model %s: %w", rows[i].ModelName, err)
		}
		models = append(models, &rows[i])
	}

	return models, nil
}

// GetModelsByProvider returns all models for a specific provider
func (r *Repository) GetModelsByProvider(ctx context.Context, providerName string) ([]*CatalogModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, max_tokens, max_completion_tokens,
		       input_cost_per_1k, output_cost_per_1k, cache_read_cost_per_1k, cache_write_cost_per_1k,
		       capabilities, status, config,
		       created_at, updated_at
		FROM catalog_models
		WHERE provider_name = $1
		ORDER BY model_name ASC
	`

	var rows []CatalogModel
	if err := r.db.SelectContext(ctx, &rows, query, providerName); err != nil {
		return nil, fmt.Errorf("failed to list models for provider %s: %w", providerName, err)
	}

	// Unmarshal JSONB fields
	models := make([]*CatalogModel, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for model %s: %w", rows[i].ModelName, err)
		}
		models = append(models, &rows[i])
	}

	return models, nil
}

// GetModel returns a specific model
func (r *Repository) GetModel(ctx context.Context, providerName, modelName string) (*CatalogModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, max_tokens, max_completion_tokens,
		       input_cost_per_1k, output_cost_per_1k, cache_read_cost_per_1k, cache_write_cost_per_1k,
		       capabilities, status, config,
		       created_at, updated_at
		FROM catalog_models
		WHERE provider_name = $1 AND model_name = $2
	`

	var model CatalogModel
	if err := r.db.GetContext(ctx, &model, query, providerName, modelName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("model not found: %s/%s", providerName, modelName)
		}
		return nil, fmt.Errorf("failed to get model: %w", err)
	}

	if err := model.unmarshalJSONB(); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSONB: %w", err)
	}

	return &model, nil
}

// SyncCatalog persists the entire catalog to the database
// This is called after loading from filesystem or GitHub
func (r *Repository) SyncCatalog(ctx context.Context, providers []*CatalogProvider, models []*CatalogModel, version string, source CatalogSource) error {
	// Start transaction
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Clear existing catalog data
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_models"); err != nil {
		return fmt.Errorf("failed to clear catalog_models: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_providers"); err != nil {
		return fmt.Errorf("failed to clear catalog_providers: %w", err)
	}

	// Insert providers
	for _, provider := range providers {
		if provider.ID == "" {
			provider.ID = uuid.New().String()
		}
		if err := provider.marshalJSONB(); err != nil {
			return fmt.Errorf("failed to marshal provider %s: %w", provider.Name, err)
		}

		query := `
			INSERT INTO catalog_providers 
			(id, name, display_name, base_url, api_version, config, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`
		now := time.Now()
		_, err := tx.ExecContext(ctx, query,
			provider.ID,
			provider.Name,
			provider.DisplayName,
			provider.BaseURL,
			provider.APIVersion,
			provider.ConfigJSON,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to insert provider %s: %w", provider.Name, err)
		}
	}

	// Insert models
	for _, model := range models {
		if model.ID == "" {
			model.ID = uuid.New().String()
		}
		if err := model.marshalJSONB(); err != nil {
			return fmt.Errorf("failed to marshal model %s/%s: %w", model.ProviderName, model.ModelName, err)
		}

		query := `
			INSERT INTO catalog_models 
			(id, provider_name, model_name, display_name, max_tokens, max_completion_tokens,
			 input_cost_per_1k, output_cost_per_1k, cache_read_cost_per_1k, cache_write_cost_per_1k,
			 capabilities, status, config, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		`
		now := time.Now()
		_, err := tx.ExecContext(ctx, query,
			model.ID,
			model.ProviderName,
			model.ModelName,
			model.DisplayName,
			model.MaxTokens,
			model.MaxCompletionTokens,
			model.InputCostPer1k,
			model.OutputCostPer1k,
			model.CacheReadCostPer1k,
			model.CacheWriteCostPer1k,
			model.CapabilitiesJSON,
			model.Status,
			model.ConfigJSON,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to insert model %s/%s: %w", model.ProviderName, model.ModelName, err)
		}
	}

	// Calculate checksum
	checksum := calculateCatalogChecksum(providers, models)

	// Insert metadata
	metadataQuery := `
		INSERT INTO catalog_metadata 
		(id, version, source, synced_at, checksum, models_count, providers_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	now := time.Now()
	_, err = tx.ExecContext(ctx, metadataQuery,
		uuid.New().String(),
		version,
		source,
		now,
		checksum,
		len(models),
		len(providers),
		now,
	)
	if err != nil {
		return fmt.Errorf("failed to insert catalog metadata: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// IsCatalogStale checks if the catalog needs to be updated
func (r *Repository) IsCatalogStale(ctx context.Context, currentVersion string) (bool, error) {
	metadata, err := r.GetLatestMetadata(ctx)
	if err != nil {
		return false, err
	}

	if metadata == nil {
		return true, nil // No catalog exists
	}

	return metadata.Version != currentVersion, nil
}

// calculateCatalogChecksum creates a SHA256 hash of the catalog data
func calculateCatalogChecksum(providers []*CatalogProvider, models []*CatalogModel) string {
	hasher := sha256.New()

	// Hash providers
	for _, p := range providers {
		hasher.Write([]byte(p.Name))
		hasher.Write([]byte(p.DisplayName))
		hasher.Write([]byte(p.BaseURL))
	}

	// Hash models
	for _, m := range models {
		hasher.Write([]byte(m.ProviderName))
		hasher.Write([]byte(m.ModelName))
		hasher.Write([]byte(m.DisplayName))
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// marshalJSONB converts Go types to JSONB bytes for CatalogProvider
func (p *CatalogProvider) marshalJSONB() error {
	if p.Config == nil {
		p.Config = make(map[string]interface{})
	}
	configJSON, err := json.Marshal(p.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	p.ConfigJSON = configJSON
	return nil
}

// unmarshalJSONB converts JSONB bytes to Go types for CatalogProvider
func (p *CatalogProvider) unmarshalJSONB() error {
	if len(p.ConfigJSON) > 0 {
		if err := json.Unmarshal(p.ConfigJSON, &p.Config); err != nil {
			return fmt.Errorf("failed to unmarshal config: %w", err)
		}
	} else {
		p.Config = make(map[string]interface{})
	}
	return nil
}

// marshalJSONB converts Go types to JSONB bytes for CatalogModel
func (m *CatalogModel) marshalJSONB() error {
	// Marshal capabilities
	if m.Capabilities == nil {
		m.Capabilities = []string{}
	}
	capabilitiesJSON, err := json.Marshal(m.Capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %w", err)
	}
	m.CapabilitiesJSON = capabilitiesJSON

	// Marshal config
	if m.Config == nil {
		m.Config = make(map[string]interface{})
	}
	configJSON, err := json.Marshal(m.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	m.ConfigJSON = configJSON

	return nil
}

// unmarshalJSONB converts JSONB bytes to Go types for CatalogModel
func (m *CatalogModel) unmarshalJSONB() error {
	// Unmarshal capabilities
	if len(m.CapabilitiesJSON) > 0 {
		if err := json.Unmarshal(m.CapabilitiesJSON, &m.Capabilities); err != nil {
			return fmt.Errorf("failed to unmarshal capabilities: %w", err)
		}
	} else {
		m.Capabilities = []string{}
	}

	// Unmarshal config
	if len(m.ConfigJSON) > 0 {
		if err := json.Unmarshal(m.ConfigJSON, &m.Config); err != nil {
			return fmt.Errorf("failed to unmarshal config: %w", err)
		}
	} else {
		m.Config = make(map[string]interface{})
	}

	return nil
}
