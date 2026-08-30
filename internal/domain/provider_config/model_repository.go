package provider_config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ModelRepository handles database operations for model status
type ModelRepository struct {
	db *sqlx.DB
}

// NewModelRepository creates a new model repository
func NewModelRepository(db *sqlx.DB) *ModelRepository {
	return &ModelRepository{db: db}
}

// UpsertModel inserts or updates a model status
func (r *ModelRepository) UpsertModel(ctx context.Context, providerName, modelName string, isNew bool) error {
	return upsertCatalogModel(ctx, r.db, providerName, modelName, isNew)
}

// UpsertModelTx applies a catalog model update inside the caller's release
// transaction.
func (r *ModelRepository) UpsertModelTx(ctx context.Context, tx *sqlx.Tx, providerName, modelName string, isNew bool) error {
	return upsertCatalogModel(ctx, tx, providerName, modelName, isNew)
}

func upsertCatalogModel(ctx context.Context, executor catalogExecContext, providerName, modelName string, isNew bool) error {
	now := time.Now()
	freshness := "stable"
	var markedNewAt *time.Time

	if isNew {
		freshness = "new"
		markedNewAt = &now
	}

	query := `
		INSERT INTO provider_model_status (
			id, provider_name, model_name, status, freshness, marked_new_at,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, 'available', $4, $5, $6, $7
		)
		ON CONFLICT (provider_name, model_name) 
		DO UPDATE SET
			status = 'available',
			freshness = CASE 
				WHEN provider_model_status.freshness = 'new' THEN 'new'
				ELSE $4
			END,
			marked_new_at = COALESCE(provider_model_status.marked_new_at, $5),
			updated_at = $7
	`

	_, err := executor.ExecContext(ctx, query,
		uuid.New().String(), providerName, modelName, freshness, markedNewAt, now, now)

	if err != nil {
		return fmt.Errorf("failed to upsert model status: %w", err)
	}

	return nil
}

// MarkDeprecated marks a model as deprecated
func (r *ModelRepository) MarkDeprecated(ctx context.Context, providerName, modelName string) error {
	now := time.Now()

	query := `
		UPDATE provider_model_status
		SET status = 'deprecated',
		    deprecated_at = $1,
		    updated_at = $2
		WHERE provider_name = $3 AND model_name = $4
	`

	result, err := r.db.ExecContext(ctx, query, &now, now, providerName, modelName)
	if err != nil {
		return fmt.Errorf("failed to mark model as deprecated: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("model not found: %s/%s", providerName, modelName)
	}

	return nil
}

// UpdateFreshness updates model freshness based on age threshold (e.g., 8 weeks)
func (r *ModelRepository) UpdateFreshness(ctx context.Context, threshold time.Duration) error {
	cutoffTime := time.Now().Add(-threshold)

	query := `
		UPDATE provider_model_status
		SET freshness = 'stable',
		    updated_at = $1
		WHERE freshness = 'new' 
		  AND marked_new_at IS NOT NULL 
		  AND marked_new_at < $2
	`

	result, err := r.db.ExecContext(ctx, query, time.Now(), cutoffTime)
	if err != nil {
		return fmt.Errorf("failed to update model freshness: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows > 0 {
		fmt.Printf("Updated %d models from 'new' to 'stable'\n", rows)
	}

	return nil
}

// GetModelStatus returns the status of a specific model
func (r *ModelRepository) GetModelStatus(ctx context.Context, providerName, modelName string) (*ModelStatus, error) {
	query := `
		SELECT id, provider_name, model_name, status, freshness, marked_new_at,
		       deprecated_at, created_at, updated_at
		FROM provider_model_status
		WHERE provider_name = $1 AND model_name = $2
	`

	var status ModelStatus
	if err := r.db.GetContext(ctx, &status, query, providerName, modelName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("model status not found: %s/%s", providerName, modelName)
		}
		return nil, fmt.Errorf("failed to get model status: %w", err)
	}

	return &status, nil
}

// ListByProvider returns all model statuses for a provider
func (r *ModelRepository) ListByProvider(ctx context.Context, providerName string) ([]*ModelStatus, error) {
	query := `
		SELECT id, provider_name, model_name, status, freshness, marked_new_at,
		       deprecated_at, created_at, updated_at
		FROM provider_model_status
		WHERE provider_name = $1
		ORDER BY 
			CASE status
				WHEN 'active' THEN 1
				WHEN 'configured' THEN 2
				WHEN 'available' THEN 3
				WHEN 'deprecated' THEN 4
				ELSE 5
			END,
			CASE freshness
				WHEN 'new' THEN 1
				WHEN 'stable' THEN 2
				ELSE 3
			END,
			model_name ASC
	`

	var statuses []*ModelStatus
	if err := r.db.SelectContext(ctx, &statuses, query, providerName); err != nil {
		return nil, fmt.Errorf("failed to list model statuses for provider: %w", err)
	}

	return statuses, nil
}

// ListAll returns all model statuses
func (r *ModelRepository) ListAll(ctx context.Context) ([]*ModelStatus, error) {
	query := `
		SELECT id, provider_name, model_name, status, freshness, marked_new_at,
		       deprecated_at, created_at, updated_at
		FROM provider_model_status
		ORDER BY provider_name ASC, model_name ASC
	`

	var statuses []*ModelStatus
	if err := r.db.SelectContext(ctx, &statuses, query); err != nil {
		return nil, fmt.Errorf("failed to list all model statuses: %w", err)
	}

	return statuses, nil
}
