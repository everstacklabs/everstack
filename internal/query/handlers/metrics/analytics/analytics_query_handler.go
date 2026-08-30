package analytics

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

// AnalyticsQueryHandler handles analytics-related queries.
type AnalyticsQueryHandler struct {
	db      *sqlx.DB
	dialect string
}

// NewAnalyticsQueryHandler creates a new analytics query handler.
func NewAnalyticsQueryHandler(db *sqlx.DB, dialect string) *AnalyticsQueryHandler {
	return &AnalyticsQueryHandler{db: db, dialect: dialect}
}

// QueryType returns the query type this handler processes.
func (h *AnalyticsQueryHandler) QueryType() string {
	return "GetModelUsageStats"
}

// Handle processes a GetModelUsageStatsQuery and returns usage statistics.
func (h *AnalyticsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	statsQuery, ok := q.(*gateway.GetModelUsageStatsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetModelUsageStatsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"query_type", statsQuery.QueryType(),
		"provider", statsQuery.Provider,
		"model", statsQuery.Model,
		"group_by", statsQuery.GroupBy,
		"aggregator", statsQuery.Aggregator,
		"correlation_id", correlationID,
	).Debug("executing model usage stats query")

	// Named-parameter query with optional filters
	baseQuery := `
SELECT 
    provider,
    model,
    period,
    request_count,
    success_count,
    error_count,
    avg_latency_ms,
    min_latency_ms,
    max_latency_ms,
    total_tokens_used,
    total_cost,
    updated_at
FROM model_usage_stats_view
WHERE tenant_id = :tenant_id
  AND (:provider IS NULL OR provider = :provider)
  AND (:model IS NULL OR model = :model)
  AND (:start_time IS NULL OR period >= :start_time)
  AND (:end_time IS NULL OR period <= :end_time)
ORDER BY period DESC
LIMIT :limit OFFSET :offset`

	var provider any
	if statsQuery.Provider != "" {
		provider = statsQuery.Provider
	}
	var model any
	if statsQuery.Model != "" {
		model = statsQuery.Model
	}
	var startTime any
	if !statsQuery.StartTime.IsZero() {
		startTime = statsQuery.StartTime
	}
	var endTime any
	if !statsQuery.EndTime.IsZero() {
		endTime = statsQuery.EndTime
	}

	args := map[string]any{
		"tenant_id":  database.TenantSchemaFromContext(ctx),
		"provider":   provider,
		"model":      model,
		"start_time": startTime,
		"end_time":   endTime,
		"limit":      statsQuery.Limit,
		"offset":     statsQuery.Offset,
	}

	qSQL, qArgs, bindErr := sqlutil.BindNamed(h.dialect, baseQuery, args)
	if bindErr != nil {
		return nil, fmt.Errorf("bind failed: %w", bindErr)
	}

	// Execute query
	var results []query.ModelUsageStatsReadModel
	err := h.db.SelectContext(ctx, &results, qSQL, qArgs...)
	if err != nil {
		logger.WithFields(
			"query_type", statsQuery.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute model usage stats query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	logger.WithFields(
		"query_type", statsQuery.QueryType(),
		"result_count", len(results),
		"correlation_id", correlationID,
	).Info("model usage stats query executed successfully")

	return results, nil
}

// ChatHistoryQueryHandler handles chat history queries.
type ChatHistoryQueryHandler struct {
	db      *sqlx.DB
	dialect string
}

// NewChatHistoryQueryHandler creates a new chat history query handler.
func NewChatHistoryQueryHandler(db *sqlx.DB, dialect string) *ChatHistoryQueryHandler {
	return &ChatHistoryQueryHandler{db: db, dialect: dialect}
}

// QueryType returns the query type this handler processes.
func (h *ChatHistoryQueryHandler) QueryType() string {
	return "GetChatHistory"
}

// Handle processes a GetChatHistoryQuery and returns chat sessions.
func (h *ChatHistoryQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	historyQuery, ok := q.(*gateway.GetChatHistoryQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetChatHistoryQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"query_type", historyQuery.QueryType(),
		"aggregate_id", historyQuery.AggregateID,
		"user_id", historyQuery.UserID,
		"correlation_id", correlationID,
	).Debug("executing chat history query")

	// Named-parameter query for chat sessions
	baseQuery := `
SELECT 
    id,
    user_id,
    api_key,
    model,
    provider,
    message_count,
    tokens_used,
    started_at,
    completed_at,
    duration,
    success,
    error_code,
    error_message,
    metadata,
    correlation_id
FROM chat_sessions_view
WHERE tenant_id = :tenant_id
  AND (:id IS NULL OR id = :id)
  AND (:user_id IS NULL OR user_id = :user_id)
  AND (:start_time IS NULL OR started_at >= :start_time)
  AND (:end_time IS NULL OR started_at <= :end_time)
ORDER BY started_at DESC
LIMIT :limit OFFSET :offset`

	var id any
	if historyQuery.AggregateID != "" {
		id = historyQuery.AggregateID
	}
	var userID any
	if historyQuery.UserID != "" {
		userID = historyQuery.UserID
	}
	var startTime any
	if !historyQuery.StartTime.IsZero() {
		startTime = historyQuery.StartTime
	}
	var endTime any
	if !historyQuery.EndTime.IsZero() {
		endTime = historyQuery.EndTime
	}

	args := map[string]any{
		"tenant_id":  database.TenantSchemaFromContext(ctx),
		"id":         id,
		"user_id":    userID,
		"start_time": startTime,
		"end_time":   endTime,
		"limit":      historyQuery.Limit,
		"offset":     historyQuery.Offset,
	}

	qSQL, qArgs, bindErr := sqlutil.BindNamed(h.dialect, baseQuery, args)
	if bindErr != nil {
		return nil, fmt.Errorf("bind failed: %w", bindErr)
	}

	// Execute query
	var results []query.ChatSessionReadModel
	err := h.db.SelectContext(ctx, &results, qSQL, qArgs...)
	if err != nil {
		logger.WithFields(
			"query_type", historyQuery.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute chat history query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	logger.WithFields(
		"query_type", historyQuery.QueryType(),
		"result_count", len(results),
		"correlation_id", correlationID,
	).Info("chat history query executed successfully")

	return results, nil
}
