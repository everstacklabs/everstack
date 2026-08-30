package traces

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// MetricsDashboardHandler queries aggregated dashboard KPIs from trace_metrics_hourly
type MetricsDashboardHandler struct {
	conn clickhouse.Conn
}

func NewMetricsDashboardHandler(conn clickhouse.Conn) *MetricsDashboardHandler {
	return &MetricsDashboardHandler{conn: conn}
}

func (h *MetricsDashboardHandler) QueryType() string {
	return "GetMetricsDashboard"
}

func (h *MetricsDashboardHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	dashQuery, ok := q.(*MetricsDashboardQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for MetricsDashboardHandler")
	}

	current, err := h.queryWindow(ctx, dashQuery, dashQuery.StartTime, dashQuery.EndTime)
	if err != nil {
		return nil, err
	}

	if !dashQuery.Compare {
		return current, nil
	}

	window := dashQuery.EndTime.Sub(dashQuery.StartTime)
	if window <= 0 {
		window = time.Hour
	}
	previousEnd := dashQuery.StartTime
	previousStart := previousEnd.Add(-window)
	previous, err := h.queryWindow(ctx, dashQuery, previousStart, previousEnd)
	if err != nil {
		return nil, err
	}

	return MetricsDashboardCompareResult{
		Current:  current,
		Previous: previous,
	}, nil
}

func (h *MetricsDashboardHandler) queryWindow(ctx context.Context, dashQuery *MetricsDashboardQuery, startTime, endTime time.Time) (MetricsDashboardResult, error) {

	// tenant_id from context is mandatory. Pre-fix this handler ran
	// `if tenantID != ""` and silently skipped the WHERE clause when
	// the schema was missing — every tenant's metrics flowed back to
	// the caller. Same Pattern B leak shape as the postgres handlers.
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return MetricsDashboardResult{}, nil
	}

	conditions := []string{"period >= toStartOfHour(?)", "period <= ?", "tenant_id = ?"}
	args := []interface{}{startTime, endTime, tenantID}

	if len(dashQuery.Models) > 0 {
		placeholders := buildPlaceholders(len(dashQuery.Models))
		conditions = append(conditions, fmt.Sprintf("model IN (%s)", placeholders))
		for _, m := range dashQuery.Models {
			args = append(args, m)
		}
	}

	if len(dashQuery.Providers) > 0 {
		placeholders := buildPlaceholders(len(dashQuery.Providers))
		conditions = append(conditions, fmt.Sprintf("provider IN (%s)", placeholders))
		for _, p := range dashQuery.Providers {
			args = append(args, p)
		}
	}

	if len(dashQuery.Environments) > 0 {
		placeholders := buildPlaceholders(len(dashQuery.Environments))
		conditions = append(conditions, fmt.Sprintf("environment IN (%s)", placeholders))
		for _, e := range dashQuery.Environments {
			args = append(args, e)
		}
	}

	where := joinConditions(conditions)

	// Query KPIs from the pre-aggregated table.
	// SummingMergeTree may have unmerged parts, so we SUM explicitly.
	// Use a subquery to avoid ClickHouse "nested aggregate" errors with type casts.
	kpiSQL := fmt.Sprintf(`
		SELECT
			total_requests,
			total_errors,
			if(total_requests > 0, total_duration_ns / total_requests / 1e6, 0) as avg_latency_ms,
			total_cost,
			if(total_requests > 0, total_errors / total_requests, 0) as error_rate,
			total_in_tokens + total_out_tokens as total_tokens,
			total_in_tokens,
			total_out_tokens,
			total_agent_turns,
			if(total_agent_turns > 0, total_agent_turn_duration_ns / total_agent_turns / 1e6, 0) as avg_agent_turn_ms
		FROM (
			SELECT
				sum(request_count) as total_requests,
				sum(error_count) as total_errors,
				sum(sum_duration_ns) as total_duration_ns,
				sum(total_cost) as total_cost,
				sum(total_input_tokens) as total_in_tokens,
				sum(total_output_tokens) as total_out_tokens,
				sum(agent_turn_count) as total_agent_turns,
				sum(sum_agent_turn_duration_ns) as total_agent_turn_duration_ns
			FROM trace_metrics_hourly
			WHERE %s
		)
	`, where)

	rows, err := h.conn.Query(ctx, kpiSQL, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query metrics dashboard KPIs")
		return MetricsDashboardResult{}, fmt.Errorf("failed to query metrics dashboard: %w", err)
	}
	defer rows.Close()

	result := MetricsDashboardResult{}
	if rows.Next() {
		var totalTokens, totalInTokens, totalOutTokens uint64
		if err := rows.Scan(
			&result.TotalRequests,
			&result.TotalErrors,
			&result.AvgLatencyMs,
			&result.TotalCost,
			&result.ErrorRate,
			&totalTokens,
			&totalInTokens,
			&totalOutTokens,
			&result.TotalAgentTurns,
			&result.AvgAgentTurnMs,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan metrics dashboard KPI row")
			return MetricsDashboardResult{}, fmt.Errorf("failed to scan metrics dashboard: %w", err)
		}
		result.TotalTokens = int64(totalTokens)
		result.TotalInputTokens = int64(totalInTokens)
		result.TotalOutputTokens = int64(totalOutTokens)
	}
	rows.Close()

	// Query unique models and providers from the metrics table
	discoverySQL := fmt.Sprintf(`
		SELECT
			uniqIf(model, model != '') as unique_models,
			uniqIf(provider, provider != '') as unique_providers
		FROM trace_metrics_hourly
		WHERE %s
	`, where)

	dRows, err := h.conn.Query(ctx, discoverySQL, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to query metrics dashboard discovery")
	} else {
		defer dRows.Close()
		if dRows.Next() {
			if err := dRows.Scan(&result.UniqueModels, &result.UniqueProviders); err != nil {
				logger.WithFields("error", err.Error()).Warn("failed to scan metrics dashboard discovery row")
			}
		}
	}

	// Compute latency percentiles (ms) directly from otel_traces.
	// trace_metrics_hourly only has sum_duration_ns, so quantiles must come
	// from raw spans. This is the read-path; if it gets hot, add quantileState
	// columns to the materialized view and switch over.
	percentileConditions := []string{
		"Timestamp >= ?",
		"Timestamp <= ?",
		"ResourceAttributes['tenant.id'] = ?",
		"Duration > 0",
	}
	percentileArgs := []interface{}{startTime, endTime, tenantID}
	if len(dashQuery.Models) > 0 {
		placeholders := buildPlaceholders(len(dashQuery.Models))
		percentileConditions = append(percentileConditions, fmt.Sprintf("%s IN (%s)", modelSQL(), placeholders))
		for _, m := range dashQuery.Models {
			percentileArgs = append(percentileArgs, m)
		}
	}
	if len(dashQuery.Providers) > 0 {
		placeholders := buildPlaceholders(len(dashQuery.Providers))
		percentileConditions = append(percentileConditions, fmt.Sprintf("%s IN (%s)", providerSQL(), placeholders))
		for _, p := range dashQuery.Providers {
			percentileArgs = append(percentileArgs, p)
		}
	}
	if len(dashQuery.Environments) > 0 {
		placeholders := buildPlaceholders(len(dashQuery.Environments))
		percentileConditions = append(percentileConditions, fmt.Sprintf("ResourceAttributes['deployment.environment'] IN (%s)", placeholders))
		for _, e := range dashQuery.Environments {
			percentileArgs = append(percentileArgs, e)
		}
	}
	percentileWhere := joinConditions(percentileConditions)
	percentileSQL := fmt.Sprintf(`
		SELECT
			-- ifNotFinite collapses NaN/Inf (which quantile() returns when
			-- the window has zero matching rows) to 0. Without this, the
			-- frontend renders "NaNms" on empty / new tenants. Mirrors the
			-- pattern in outcome_dashboard.go.
			ifNotFinite(quantile(0.50)(Duration) / 1e6, 0) as p50_ms,
			ifNotFinite(quantile(0.95)(Duration) / 1e6, 0) as p95_ms,
			ifNotFinite(quantile(0.99)(Duration) / 1e6, 0) as p99_ms,
			ifNotFinite(quantileIf(0.50)(
				toFloat64OrZero(SpanAttributes['llm.stream.time_to_first_token_ms']),
				SpanAttributes['llm.stream.time_to_first_token_ms'] != ''
			), 0) as ttft_p50_ms,
			ifNotFinite(quantileIf(0.95)(
				toFloat64OrZero(SpanAttributes['llm.stream.time_to_first_token_ms']),
				SpanAttributes['llm.stream.time_to_first_token_ms'] != ''
			), 0) as ttft_p95_ms
		FROM otel_traces
		WHERE %s
	`, percentileWhere)

	pRows, err := h.conn.Query(ctx, percentileSQL, percentileArgs...)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to query latency percentiles")
	} else {
		defer pRows.Close()
		if pRows.Next() {
			if err := pRows.Scan(&result.P50LatencyMs, &result.P95LatencyMs, &result.P99LatencyMs, &result.TtftP50Ms, &result.TtftP95Ms); err != nil {
				logger.WithFields("error", err.Error()).Warn("failed to scan latency percentile row")
			}
		}
	}

	return result, nil
}

// buildPlaceholders returns comma-separated "?" placeholders
func buildPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	result := "?"
	for i := 1; i < count; i++ {
		result += ", ?"
	}
	return result
}
