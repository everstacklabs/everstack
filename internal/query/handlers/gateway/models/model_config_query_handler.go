package models

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/database/sqlutil"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/query/handlers/gateway"
)

// ModelConfigQueryHandler handles model configuration queries.
type ModelConfigQueryHandler struct {
	db *sqlx.DB
}

// NewModelConfigQueryHandler creates a new model config query handler.
func NewModelConfigQueryHandler(db *sqlx.DB) *ModelConfigQueryHandler {
	return &ModelConfigQueryHandler{db: db}
}

// QueryType returns the query type this handler processes.
func (h *ModelConfigQueryHandler) QueryType() string {
	return "GetActiveModels"
}

// Handle processes a GetActiveModelsQuery and returns model configurations.
func (h *ModelConfigQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	modelsQuery, ok := q.(*gateway.GetActiveModelsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetActiveModelsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"query_type", modelsQuery.QueryType(),
		"provider", modelsQuery.Provider,
		"enabled", modelsQuery.Enabled,
		"correlation_id", correlationID,
	).Debug("executing active models query")

	// Named-parameter query
	baseQuery := `
SELECT 
    provider,
    model_id,
    alias,
    config,
    enabled,
    is_default,
    created_at,
    updated_at,
    version
FROM model_configs_view
WHERE 1=1
  AND (:provider IS NULL OR provider = :provider)
  AND (:enabled IS NULL OR enabled = :enabled)
ORDER BY provider, alias
LIMIT :limit OFFSET :offset`

	var provider any
	if modelsQuery.Provider != "" {
		provider = modelsQuery.Provider
	}
	var enabled any
	if modelsQuery.Enabled != nil {
		enabled = *modelsQuery.Enabled
	}

	args := map[string]any{
		"provider": provider,
		"enabled":  enabled,
		"limit":    modelsQuery.Limit,
		"offset":   modelsQuery.Offset,
	}

	qSQL, qArgs, bindErr := sqlutil.BindNamed("postgres", baseQuery, args)
	if bindErr != nil {
		return nil, fmt.Errorf("bind failed: %w", bindErr)
	}

	// Execute query
	var results []query.ModelConfigReadModel
	err := h.db.SelectContext(ctx, &results, qSQL, qArgs...)
	if err != nil {
		logger.WithFields(
			"query_type", modelsQuery.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute active models query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	logger.WithFields(
		"query_type", modelsQuery.QueryType(),
		"result_count", len(results),
		"correlation_id", correlationID,
	).Info("active models query executed successfully")

	return results, nil
}
