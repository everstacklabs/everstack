package custom_models

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

// ModelSource represents how a custom model was added
type ModelSource string

const (
	ModelSourceManual     ModelSource = "manual"
	ModelSourceDiscovered ModelSource = "discovered"
)

// CustomModel represents a user-configured custom model from a meta-provider
type CustomModel struct {
	ID            string                 `db:"id"`
	ProviderName  string                 `db:"provider_name"`
	ModelName     string                 `db:"model_name"`
	DisplayName   string                 `db:"display_name"`
	ModelMetadata map[string]interface{} `db:"-"` // Handled separately as JSONB
	Source        ModelSource            `db:"source"`
	IsActive      bool                   `db:"is_active"`
	CreatedAt     time.Time              `db:"created_at"`
	UpdatedAt     time.Time              `db:"updated_at"`

	// Internal field for JSONB handling
	ModelMetadataJSON []byte `db:"model_metadata"`
}

// Repository handles database operations for custom models
type Repository struct {
	db *sqlx.DB
}

// NewRepository creates a new custom models repository
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// List returns all custom models
func (r *Repository) List(ctx context.Context) ([]*CustomModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, model_metadata,
		       source, is_active, created_at, updated_at
		FROM custom_models
		ORDER BY provider_name ASC, display_name ASC
	`

	var rows []CustomModel
	if err := r.db.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("failed to list custom models: %w", err)
	}

	// Unmarshal JSONB fields
	models := make([]*CustomModel, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for model %s: %w", rows[i].ModelName, err)
		}
		models = append(models, &rows[i])
	}

	return models, nil
}

// ListByProvider returns all custom models for a specific provider
func (r *Repository) ListByProvider(ctx context.Context, providerName string) ([]*CustomModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, model_metadata,
		       source, is_active, created_at, updated_at
		FROM custom_models
		WHERE provider_name = $1
		ORDER BY display_name ASC
	`

	var rows []CustomModel
	if err := r.db.SelectContext(ctx, &rows, query, providerName); err != nil {
		return nil, fmt.Errorf("failed to list custom models for provider %s: %w", providerName, err)
	}

	// Unmarshal JSONB fields
	models := make([]*CustomModel, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for model %s: %w", rows[i].ModelName, err)
		}
		models = append(models, &rows[i])
	}

	return models, nil
}

// ListActiveByProvider returns all active custom models for a specific provider
func (r *Repository) ListActiveByProvider(ctx context.Context, providerName string) ([]*CustomModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, model_metadata,
		       source, is_active, created_at, updated_at
		FROM custom_models
		WHERE provider_name = $1 AND is_active = true
		ORDER BY display_name ASC
	`

	var rows []CustomModel
	if err := r.db.SelectContext(ctx, &rows, query, providerName); err != nil {
		return nil, fmt.Errorf("failed to list active custom models for provider %s: %w", providerName, err)
	}

	// Unmarshal JSONB fields
	models := make([]*CustomModel, 0, len(rows))
	for i := range rows {
		if err := rows[i].unmarshalJSONB(); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSONB for model %s: %w", rows[i].ModelName, err)
		}
		models = append(models, &rows[i])
	}

	return models, nil
}

// Get returns a custom model by provider and model name
func (r *Repository) Get(ctx context.Context, providerName, modelName string) (*CustomModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, model_metadata,
		       source, is_active, created_at, updated_at
		FROM custom_models
		WHERE provider_name = $1 AND model_name = $2
	`

	var model CustomModel
	if err := r.db.GetContext(ctx, &model, query, providerName, modelName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("custom model not found: %s/%s", providerName, modelName)
		}
		return nil, fmt.Errorf("failed to get custom model: %w", err)
	}

	if err := model.unmarshalJSONB(); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSONB: %w", err)
	}

	return &model, nil
}

// GetByID returns a custom model by ID
func (r *Repository) GetByID(ctx context.Context, id string) (*CustomModel, error) {
	query := `
		SELECT id, provider_name, model_name, display_name, model_metadata,
		       source, is_active, created_at, updated_at
		FROM custom_models
		WHERE id = $1
	`

	var model CustomModel
	if err := r.db.GetContext(ctx, &model, query, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("custom model not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get custom model: %w", err)
	}

	if err := model.unmarshalJSONB(); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSONB: %w", err)
	}

	return &model, nil
}

// Exists checks if a custom model exists
func (r *Repository) Exists(ctx context.Context, providerName, modelName string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM custom_models 
			WHERE provider_name = $1 AND model_name = $2
		)
	`

	var exists bool
	if err := r.db.GetContext(ctx, &exists, query, providerName, modelName); err != nil {
		return false, fmt.Errorf("failed to check if custom model exists: %w", err)
	}

	return exists, nil
}

// Create creates a new custom model
func (r *Repository) Create(ctx context.Context, model *CustomModel) error {
	// Generate ID if not set
	if model.ID == "" {
		model.ID = uuid.New().String()
	}

	// Marshal JSONB fields
	if err := model.marshalJSONB(); err != nil {
		return fmt.Errorf("failed to marshal JSONB: %w", err)
	}

	query := `
		INSERT INTO custom_models 
		(id, provider_name, model_name, display_name, model_metadata, source, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at
	`

	now := time.Now()
	if model.CreatedAt.IsZero() {
		model.CreatedAt = now
	}
	model.UpdatedAt = now

	var returned struct {
		ID        string    `db:"id"`
		CreatedAt time.Time `db:"created_at"`
		UpdatedAt time.Time `db:"updated_at"`
	}

	err := r.db.GetContext(ctx, &returned, query,
		model.ID,
		model.ProviderName,
		model.ModelName,
		model.DisplayName,
		model.ModelMetadataJSON,
		model.Source,
		model.IsActive,
		model.CreatedAt,
		model.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create custom model: %w", err)
	}

	model.ID = returned.ID
	model.CreatedAt = returned.CreatedAt
	model.UpdatedAt = returned.UpdatedAt

	return nil
}

// Update updates an existing custom model
func (r *Repository) Update(ctx context.Context, model *CustomModel) error {
	// Marshal JSONB fields
	if err := model.marshalJSONB(); err != nil {
		return fmt.Errorf("failed to marshal JSONB: %w", err)
	}

	query := `
		UPDATE custom_models
		SET display_name = $1,
		    model_metadata = $2,
		    is_active = $3,
		    updated_at = $4
		WHERE id = $5
		RETURNING updated_at
	`

	model.UpdatedAt = time.Now()

	var updatedAt time.Time
	err := r.db.GetContext(ctx, &updatedAt, query,
		model.DisplayName,
		model.ModelMetadataJSON,
		model.IsActive,
		model.UpdatedAt,
		model.ID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("custom model not found: %s", model.ID)
		}
		return fmt.Errorf("failed to update custom model: %w", err)
	}

	model.UpdatedAt = updatedAt

	return nil
}

// Delete removes a custom model
func (r *Repository) Delete(ctx context.Context, id string) error {
	query := `
		DELETE FROM custom_models
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete custom model: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("custom model not found: %s", id)
	}

	return nil
}

// DeleteByProviderAndModel removes a custom model by provider and model name
func (r *Repository) DeleteByProviderAndModel(ctx context.Context, providerName, modelName string) error {
	query := `
		DELETE FROM custom_models
		WHERE provider_name = $1 AND model_name = $2
	`

	result, err := r.db.ExecContext(ctx, query, providerName, modelName)
	if err != nil {
		return fmt.Errorf("failed to delete custom model: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("custom model not found: %s/%s", providerName, modelName)
	}

	return nil
}

// SetActive sets the active status of a custom model
func (r *Repository) SetActive(ctx context.Context, id string, isActive bool) error {
	query := `
		UPDATE custom_models
		SET is_active = $1,
		    updated_at = $2
		WHERE id = $3
	`

	_, err := r.db.ExecContext(ctx, query, isActive, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to set active status: %w", err)
	}

	return nil
}

// marshalJSONB converts Go types to JSONB bytes
func (m *CustomModel) marshalJSONB() error {
	// Marshal model metadata
	if m.ModelMetadata == nil {
		m.ModelMetadata = make(map[string]interface{})
	}
	metadataJSON, err := json.Marshal(m.ModelMetadata)
	if err != nil {
		return fmt.Errorf("failed to marshal model_metadata: %w", err)
	}
	m.ModelMetadataJSON = metadataJSON

	return nil
}

// unmarshalJSONB converts JSONB bytes to Go types
func (m *CustomModel) unmarshalJSONB() error {
	// Unmarshal model metadata
	if len(m.ModelMetadataJSON) > 0 {
		if err := json.Unmarshal(m.ModelMetadataJSON, &m.ModelMetadata); err != nil {
			return fmt.Errorf("failed to unmarshal model_metadata: %w", err)
		}
	} else {
		m.ModelMetadata = make(map[string]interface{})
	}

	return nil
}
