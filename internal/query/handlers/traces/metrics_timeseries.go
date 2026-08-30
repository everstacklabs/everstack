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

// MetricsTimeSeriesHandler queries time-bucketed metric data from trace_metrics_hourly
type MetricsTimeSeriesHandler struct {
	conn clickhouse.Conn
}

func NewMetricsTimeSeriesHandler(conn clickhouse.Conn) *MetricsTimeSeriesHandler {
	return &MetricsTimeSeriesHandler{conn: conn}
}

func (h *MetricsTimeSeriesHandler) QueryType() string {
	return "GetMetricsTimeSeries"
}

func (h *MetricsTimeSeriesHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	tsQuery, ok := q.(*MetricsTimeSeriesQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for MetricsTimeSeriesHandler")
	}

	current, err := h.queryWindow(ctx, tsQuery, tsQuery.StartTime, tsQuery.EndTime)
	if err != nil {
		return nil, err
	}

	if !tsQuery.Compare {
		return current, nil
	}

	window := tsQuery.EndTime.Sub(tsQuery.StartTime)
	if window <= 0 {
		window = time.Hour
	}
	previousEnd := tsQuery.StartTime
	previousStart := previousEnd.Add(-window)
	previous, err := h.queryWindow(ctx, tsQuery, previousStart, previousEnd)
	if err != nil {
		return nil, err
	}

	return MetricsTimeSeriesCompareResult{
		Current:  current,
		Previous: previous,
	}, nil
}

func (h *MetricsTimeSeriesHandler) queryWindow(ctx context.Context, tsQuery *MetricsTimeSeriesQuery, startTime, endTime time.Time) ([]MetricsTimeSeriesResult, error) {
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — see metrics_dashboard.go for rationale.
		return []MetricsTimeSeriesResult{}, nil
	}

	if tsQuery.Metric == "ttft_p50" || tsQuery.Metric == "ttft_p95" {
		return h.queryTtftTimeSeries(ctx, tsQuery, startTime, endTime, tenantID)
	}

	// Determine time bucket function.
	// trace_metrics_hourly stores hourly periods; for sub-hour granularity the
	// period column is already at hour resolution so we just use it directly.
	var timeBucket string
	switch tsQuery.Granularity {
	case "minute", "5minute":
		// Sub-hour granularity: the pre-aggregated table is hourly, so we
		// return hourly buckets (the finest resolution available).
		timeBucket = "period"
	case "6hour":
		timeBucket = "toStartOfInterval(period, INTERVAL 6 HOUR)"
	case "day":
		timeBucket = "toStartOfDay(period)"
	default:
		timeBucket = "period" // hourly (native resolution)
	}

	// Determine value expression based on metric.
	// Multiply by 1.0 to coerce integer sums to Float64 for consistent scan types.
	var valueExpr string
	switch tsQuery.Metric {
	case "request_count":
		valueExpr = "sum(request_count) * 1.0"
	case "error_count":
		valueExpr = "sum(error_count) * 1.0"
	case "avg_latency_ms":
		valueExpr = "if(sum(request_count) > 0, sum(sum_duration_ns) * 1.0 / sum(request_count) / 1e6, 0.0)"
	case "total_cost":
		valueExpr = "sum(total_cost)"
	case "error_rate":
		valueExpr = "if(sum(request_count) > 0, sum(error_count) * 1.0 / sum(request_count), 0.0)"
	case "total_tokens", "tokens":
		valueExpr = "(sum(total_input_tokens) + sum(total_output_tokens)) * 1.0"
	case "input_tokens":
		valueExpr = "sum(total_input_tokens) * 1.0"
	case "output_tokens":
		valueExpr = "sum(total_output_tokens) * 1.0"
	case "agent_turn_count":
		valueExpr = "sum(agent_turn_count) * 1.0"
	case "avg_agent_turn_ms":
		valueExpr = "if(sum(agent_turn_count) > 0, sum(sum_agent_turn_duration_ns) * 1.0 / sum(agent_turn_count) / 1e6, 0.0)"
	default:
		valueExpr = "sum(request_count) * 1.0"
	}

	// Determine group-by label
	var labelExpr string
	var hasLabel bool
	switch tsQuery.GroupBy {
	case "model":
		labelExpr = "model"
		hasLabel = true
	case "provider":
		labelExpr = "provider"
		hasLabel = true
	case "provider_api_key_id":
		labelExpr = "provider_api_key_id"
		hasLabel = true
	default:
		hasLabel = false
	}

	// provider_api_key_id lives only in the dedicated per-key rollup; every
	// other timeseries dimension comes from trace_metrics_hourly.
	source := breakdownAggregateSource(tsQuery.GroupBy)

	// Build WHERE conditions — tenant_id is mandatory (early-return above)
	conditions := []string{"period >= toStartOfHour(?)", "period <= ?", "tenant_id = ?"}
	args := []interface{}{startTime, endTime, tenantID}

	if len(tsQuery.Models) > 0 {
		placeholders := buildPlaceholders(len(tsQuery.Models))
		conditions = append(conditions, fmt.Sprintf("model IN (%s)", placeholders))
		for _, m := range tsQuery.Models {
			args = append(args, m)
		}
	}

	if len(tsQuery.Providers) > 0 {
		placeholders := buildPlaceholders(len(tsQuery.Providers))
		conditions = append(conditions, fmt.Sprintf("provider IN (%s)", placeholders))
		for _, p := range tsQuery.Providers {
			args = append(args, p)
		}
	}

	if len(tsQuery.Environments) > 0 {
		placeholders := buildPlaceholders(len(tsQuery.Environments))
		conditions = append(conditions, fmt.Sprintf("environment IN (%s)", placeholders))
		for _, e := range tsQuery.Environments {
			args = append(args, e)
		}
	}

	where := joinConditions(conditions)

	var sqlQuery string
	if hasLabel {
		// Filter out empty labels for cleaner results
		labelFilter := fmt.Sprintf("%s != ''", labelExpr)

		sqlQuery = fmt.Sprintf(`
			SELECT
				%s as ts,
				%s as value,
				%s as label
			FROM %s
			WHERE %s AND %s
			GROUP BY ts, label
			ORDER BY ts ASC, label ASC
		`, timeBucket, valueExpr, labelExpr, source, where, labelFilter)
	} else {
		sqlQuery = fmt.Sprintf(`
			SELECT
				%s as ts,
				%s as value
			FROM %s
			WHERE %s
			GROUP BY ts
			ORDER BY ts ASC
		`, timeBucket, valueExpr, source, where)
	}

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query metrics time series")
		return nil, fmt.Errorf("failed to query metrics time series: %w", err)
	}
	defer rows.Close()

	// Group results by label into separate series
	seriesMap := make(map[string]*MetricsTimeSeriesResult)
	defaultLabel := tsQuery.Metric

	for rows.Next() {
		var period time.Time
		var value float64
		var label string

		if hasLabel {
			if err := rows.Scan(&period, &value, &label); err != nil {
				logger.WithFields("error", err.Error()).Error("failed to scan time series row")
				continue
			}
		} else {
			if err := rows.Scan(&period, &value); err != nil {
				logger.WithFields("error", err.Error()).Error("failed to scan time series row")
				continue
			}
			label = defaultLabel
		}

		if label == "" {
			label = "unknown"
		}

		series, exists := seriesMap[label]
		if !exists {
			series = &MetricsTimeSeriesResult{
				MetricName: label,
				Buckets:    []TimeSeriesBucketResult{},
			}
			seriesMap[label] = series
		}

		series.Buckets = append(series.Buckets, TimeSeriesBucketResult{
			Timestamp: period,
			Value:     value,
			Label:     label,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating time series: %w", err)
	}

	// Convert map to slice
	results := make([]MetricsTimeSeriesResult, 0, len(seriesMap))
	for _, series := range seriesMap {
		results = append(results, *series)
	}

	// Ensure non-nil
	if results == nil {
		results = []MetricsTimeSeriesResult{}
	}

	return results, nil
}

func (h *MetricsTimeSeriesHandler) queryTtftTimeSeries(ctx context.Context, tsQuery *MetricsTimeSeriesQuery, startTime, endTime time.Time, tenantID string) ([]MetricsTimeSeriesResult, error) {
	var timeBucket string
	switch tsQuery.Granularity {
	case "6hour":
		timeBucket = "toStartOfInterval(Timestamp, INTERVAL 6 HOUR)"
	case "day":
		timeBucket = "toStartOfDay(Timestamp)"
	default:
		timeBucket = "toStartOfHour(Timestamp)"
	}

	quantile := "0.50"
	if tsQuery.Metric == "ttft_p95" {
		quantile = "0.95"
	}

	conditions := []string{
		"Timestamp >= ?",
		"Timestamp <= ?",
		"ResourceAttributes['tenant.id'] = ?",
		"SpanAttributes['llm.stream.time_to_first_token_ms'] != ''",
	}
	args := []interface{}{startTime, endTime, tenantID}

	if len(tsQuery.Models) > 0 {
		placeholders := buildPlaceholders(len(tsQuery.Models))
		conditions = append(conditions, fmt.Sprintf("%s IN (%s)", modelSQL(), placeholders))
		for _, m := range tsQuery.Models {
			args = append(args, m)
		}
	}
	if len(tsQuery.Providers) > 0 {
		placeholders := buildPlaceholders(len(tsQuery.Providers))
		conditions = append(conditions, fmt.Sprintf("%s IN (%s)", providerSQL(), placeholders))
		for _, p := range tsQuery.Providers {
			args = append(args, p)
		}
	}
	if len(tsQuery.Environments) > 0 {
		placeholders := buildPlaceholders(len(tsQuery.Environments))
		conditions = append(conditions, fmt.Sprintf("ResourceAttributes['deployment.environment'] IN (%s)", placeholders))
		for _, e := range tsQuery.Environments {
			args = append(args, e)
		}
	}

	sqlQuery := fmt.Sprintf(`
		SELECT
			%s as ts,
			ifNotFinite(quantile(%s)(toFloat64OrZero(SpanAttributes['llm.stream.time_to_first_token_ms'])), 0) as value
		FROM otel_traces
		WHERE %s
		GROUP BY ts
		ORDER BY ts ASC
	`, timeBucket, quantile, joinConditions(conditions))

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query TTFT time series")
		return nil, fmt.Errorf("failed to query TTFT time series: %w", err)
	}
	defer rows.Close()

	result := MetricsTimeSeriesResult{
		MetricName: tsQuery.Metric,
		Buckets:    []TimeSeriesBucketResult{},
	}
	for rows.Next() {
		var period time.Time
		var value float64
		if err := rows.Scan(&period, &value); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan TTFT time series row")
			continue
		}
		result.Buckets = append(result.Buckets, TimeSeriesBucketResult{
			Timestamp: period,
			Value:     value,
			Label:     tsQuery.Metric,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating TTFT time series: %w", err)
	}
	if len(result.Buckets) == 0 {
		return []MetricsTimeSeriesResult{}, nil
	}
	return []MetricsTimeSeriesResult{result}, nil
}
