package load_balancer

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

// LoadBalancerStatsQueryHandler handles load balancer statistics queries.
type LoadBalancerStatsQueryHandler struct {
	db *sqlx.DB
}

// NewLoadBalancerStatsQueryHandler creates a new load balancer stats query handler.
func NewLoadBalancerStatsQueryHandler(db *sqlx.DB) *LoadBalancerStatsQueryHandler {
	return &LoadBalancerStatsQueryHandler{db: db}
}

// QueryType returns the query type this handler processes.
func (h *LoadBalancerStatsQueryHandler) QueryType() string {
	return "GetLoadBalancerStats"
}

// Handle processes a GetLoadBalancerStatsQuery and returns LB performance data.
func (h *LoadBalancerStatsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	statsQuery, ok := q.(*gateway.GetLoadBalancerStatsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetLoadBalancerStatsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"query_type", statsQuery.QueryType(),
		"strategy", statsQuery.Strategy,
		"correlation_id", correlationID,
	).Debug("executing load balancer stats query")

	// Named-parameter query
	baseQuery := `
SELECT 
    period,
    strategy,
    key_source,
    request_count,
    fallback_count,
    fallback_rate,
    avg_latency_ms,
    primary_success,
    fallback_success,
    total_failures,
    updated_at
FROM load_balancer_stats_view
WHERE 1=1
  AND (:strategy IS NULL OR strategy = :strategy)
  AND (:start_time IS NULL OR period >= :start_time)
  AND (:end_time IS NULL OR period <= :end_time)
ORDER BY period DESC
LIMIT :limit OFFSET :offset`

	var strategy any
	if statsQuery.Strategy != "" {
		strategy = statsQuery.Strategy
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
		"strategy":   strategy,
		"start_time": startTime,
		"end_time":   endTime,
		"limit":      statsQuery.Limit,
		"offset":     statsQuery.Offset,
	}

	qSQL, qArgs, bindErr := sqlutil.BindNamed("postgres", baseQuery, args)
	if bindErr != nil {
		return nil, fmt.Errorf("bind failed: %w", bindErr)
	}

	// Execute query
	var results []query.LoadBalancerStatsReadModel
	err := h.db.SelectContext(ctx, &results, qSQL, qArgs...)
	if err != nil {
		logger.WithFields(
			"query_type", statsQuery.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute load balancer stats query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	logger.WithFields(
		"query_type", statsQuery.QueryType(),
		"result_count", len(results),
		"correlation_id", correlationID,
	).Info("load balancer stats query executed successfully")

	return results, nil
}
