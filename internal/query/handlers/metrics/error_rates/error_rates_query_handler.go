package error_rates

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/database/sqlutil"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/query/handlers/gateway"
)

// ErrorRatesQueryHandler handles error rate analytics queries.
type ErrorRatesQueryHandler struct {
	db      *sqlx.DB
	dialect string
}

// NewErrorRatesQueryHandler creates a new error rates query handler.
func NewErrorRatesQueryHandler(db *sqlx.DB, dialect string) *ErrorRatesQueryHandler {
	return &ErrorRatesQueryHandler{db: db, dialect: dialect}
}

// QueryType returns the query type this handler processes.
func (h *ErrorRatesQueryHandler) QueryType() string {
	return "GetErrorRates"
}

// Handle processes a GetErrorRatesQuery and returns error rate analytics.
func (h *ErrorRatesQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	errorQuery, ok := q.(*gateway.GetErrorRatesQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetErrorRatesQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"query_type", errorQuery.QueryType(),
		"provider", errorQuery.Provider,
		"model", errorQuery.Model,
		"error_type", errorQuery.ErrorType,
		"correlation_id", correlationID,
	).Debug("executing error rates query")

	// Dialect-agnostic SQL using simple macros for JSON/time parts
	baseQuery := sqlutil.ExpandMacros(h.dialect, `
SELECT 
    provider,
    model,
    error_type,
    error_code,
    period,
    error_count,
    COALESCE(total_requests.total_count, 0) as total_count,
    CASE 
        WHEN COALESCE(total_requests.total_count, 0) > 0 
        THEN ROUND((error_count::NUMERIC / total_requests.total_count::NUMERIC) * 100, 2)
        ELSE 0.0 
    END as error_rate,
    updated_at
FROM error_rates_view err
LEFT JOIN (
    SELECT 
        {{period_hour_from_created_at}} as period,
        {{json_provider}} as provider,
        {{json_model}} as model,
        COUNT(*) as total_count
    FROM events
    WHERE tenant_id = :tenant_id
      AND type IN ('chat.session.started', 'embedding.request.started')
    GROUP BY 
        {{period_hour_from_created_at}},
        {{json_provider}},
        {{json_model}}
) total_requests ON err.period = total_requests.period 
    AND err.provider = total_requests.provider 
    AND err.model = total_requests.model
WHERE 1=1
  AND (:provider IS NULL OR provider = :provider)
  AND (:model IS NULL OR model = :model)
  AND (:error_type IS NULL OR error_type = :error_type)
  AND (:start_time IS NULL OR period >= :start_time)
  AND (:end_time IS NULL OR period <= :end_time)
ORDER BY period DESC, error_rate DESC
LIMIT :limit OFFSET :offset`)

	// Prepare named arguments (nil disables filters)
	var provider any
	if errorQuery.Provider != "" {
		provider = errorQuery.Provider
	}
	var model any
	if errorQuery.Model != "" {
		model = errorQuery.Model
	}
	var errorType any
	if errorQuery.ErrorType != "" {
		errorType = errorQuery.ErrorType
	}
	var startTime any
	if !errorQuery.StartTime.IsZero() {
		startTime = errorQuery.StartTime
	}
	var endTime any
	if !errorQuery.EndTime.IsZero() {
		endTime = errorQuery.EndTime
	}

	args := map[string]any{
		"tenant_id":  database.TenantSchemaFromContext(ctx),
		"provider":   provider,
		"model":      model,
		"error_type": errorType,
		"start_time": startTime,
		"end_time":   endTime,
		"limit":      errorQuery.Limit,
		"offset":     errorQuery.Offset,
	}

	// Bind named and rebind based on dialect
	querySQL, vals, nerr := sqlutil.BindNamed(h.dialect, baseQuery, args)
	if nerr != nil {
		return nil, fmt.Errorf("failed to bind named params: %w", nerr)
	}

	// Execute query
	var results []query.ErrorRateReadModel
	err := h.db.SelectContext(ctx, &results, querySQL, vals...)
	if err != nil {
		logger.WithFields(
			"query_type", errorQuery.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute error rates query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	logger.WithFields(
		"query_type", errorQuery.QueryType(),
		"result_count", len(results),
		"correlation_id", correlationID,
	).Info("error rates query executed successfully")

	return results, nil
}
