package traces

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

// MetricsBreakdownHandler queries Top-N metric breakdowns.
type MetricsBreakdownHandler struct {
	conn clickhouse.Conn
}

func NewMetricsBreakdownHandler(conn clickhouse.Conn) *MetricsBreakdownHandler {
	return &MetricsBreakdownHandler{conn: conn}
}

func (h *MetricsBreakdownHandler) QueryType() string {
	return "GetMetricsBreakdown"
}

func (h *MetricsBreakdownHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	breakdownQuery, ok := q.(*MetricsBreakdownQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for MetricsBreakdownHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return MetricsBreakdownResult{Rows: []MetricsBreakdownRowResult{}}, nil
	}

	current, err := h.queryWindow(ctx, breakdownQuery, breakdownQuery.StartTime, breakdownQuery.EndTime, tenantID, nil)
	if err != nil {
		return nil, err
	}

	if breakdownQuery.Compare && len(current.Rows) > 0 {
		window := breakdownQuery.EndTime.Sub(breakdownQuery.StartTime)
		if window <= 0 {
			window = time.Hour
		}
		keys := make([]string, 0, len(current.Rows))
		for _, row := range current.Rows {
			keys = append(keys, row.Key)
		}
		previousEnd := breakdownQuery.StartTime
		previousStart := previousEnd.Add(-window)
		previous, err := h.queryWindow(ctx, breakdownQuery, previousStart, previousEnd, tenantID, keys)
		if err != nil {
			return nil, err
		}
		previousByKey := make(map[string]float64, len(previous.Rows))
		for _, row := range previous.Rows {
			previousByKey[row.Key] = row.Value
		}
		for i := range current.Rows {
			current.Rows[i].PreviousValue = previousByKey[current.Rows[i].Key]
		}
	}

	return current, nil
}

func (h *MetricsBreakdownHandler) queryWindow(ctx context.Context, q *MetricsBreakdownQuery, startTime, endTime time.Time, tenantID string, restrictKeys []string) (MetricsBreakdownResult, error) {
	// model/provider/environment come from the pre-aggregated trace_metrics_hourly
	// table; trace_name/session/user are not columns there, so they are computed
	// directly from otel_traces root spans.
	if dimExpr, ok := breakdownOtelDimension(q.GroupBy); ok {
		return h.queryOtelWindow(ctx, q, startTime, endTime, tenantID, restrictKeys, dimExpr)
	}
	return h.queryAggregateWindow(ctx, q, startTime, endTime, tenantID, restrictKeys)
}

// breakdownOtelDimension returns the SQL expression to group by when the
// dimension is not a trace_metrics_hourly column, and whether the dimension is
// such an otel_traces-computed one. session/user reuse the same coalesce as the
// rest of the read path so any-SDK spans group correctly.
func breakdownOtelDimension(groupBy string) (string, bool) {
	switch groupBy {
	case "trace_name":
		return "SpanName", true
	case "session":
		return sessionSQL(), true
	case "user":
		return userSQL(), true
	default:
		return "", false
	}
}

func (h *MetricsBreakdownHandler) queryAggregateWindow(ctx context.Context, q *MetricsBreakdownQuery, startTime, endTime time.Time, tenantID string, restrictKeys []string) (MetricsBreakdownResult, error) {
	dim, err := breakdownAggregateDimension(q.GroupBy)
	if err != nil {
		return MetricsBreakdownResult{}, err
	}
	valueExpr, err := breakdownAggregateMetric(q.Metric)
	if err != nil {
		return MetricsBreakdownResult{}, err
	}

	conditions := []string{"period >= toStartOfHour(?)", "period <= ?", "tenant_id = ?"}
	args := []interface{}{startTime, endTime, tenantID}

	if len(q.Models) > 0 {
		placeholders := buildPlaceholders(len(q.Models))
		conditions = append(conditions, fmt.Sprintf("model IN (%s)", placeholders))
		for _, m := range q.Models {
			args = append(args, m)
		}
	}
	if len(q.Providers) > 0 {
		placeholders := buildPlaceholders(len(q.Providers))
		conditions = append(conditions, fmt.Sprintf("provider IN (%s)", placeholders))
		for _, p := range q.Providers {
			args = append(args, p)
		}
	}
	if len(q.Environments) > 0 {
		placeholders := buildPlaceholders(len(q.Environments))
		conditions = append(conditions, fmt.Sprintf("environment IN (%s)", placeholders))
		for _, e := range q.Environments {
			args = append(args, e)
		}
	}
	if len(restrictKeys) > 0 {
		placeholders := buildPlaceholders(len(restrictKeys))
		conditions = append(conditions, fmt.Sprintf("%s IN (%s)", dim, placeholders))
		for _, key := range restrictKeys {
			args = append(args, key)
		}
	}

	// Surface the dominant provider per key so the UI can show a provider
	// logo. Meaningful when grouping by model or provider_api_key_id (both of
	// which fan out under a single provider); other dimensions return ''.
	providerExpr := "''"
	if q.GroupBy == "model" || q.GroupBy == "provider_api_key_id" {
		providerExpr = "argMax(provider, request_count)"
	}

	source := breakdownAggregateSource(q.GroupBy)
	where := joinConditions(conditions)
	sqlQuery := fmt.Sprintf(`
		SELECT
			%s as key,
			%s as value,
			sum(request_count) as request_count_total,
			%s as provider
		FROM %s
		WHERE %s
		GROUP BY key
		HAVING key != ''
		ORDER BY value DESC
		LIMIT ?
	`, dim, valueExpr, providerExpr, source, where)

	queryArgs := append([]interface{}{}, args...)
	if len(restrictKeys) > 0 {
		queryArgs = append(queryArgs, len(restrictKeys))
	} else {
		queryArgs = append(queryArgs, q.Limit)
	}
	rows, err := h.conn.Query(ctx, sqlQuery, queryArgs...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query metrics breakdown")
		return MetricsBreakdownResult{}, fmt.Errorf("failed to query metrics breakdown: %w", err)
	}
	defer rows.Close()

	result := MetricsBreakdownResult{Rows: []MetricsBreakdownRowResult{}}
	for rows.Next() {
		var row MetricsBreakdownRowResult
		if err := rows.Scan(&row.Key, &row.Value, &row.RequestCount, &row.Provider); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan metrics breakdown row")
			continue
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return MetricsBreakdownResult{}, fmt.Errorf("error iterating metrics breakdown: %w", err)
	}

	if len(restrictKeys) == 0 {
		total, err := h.queryAggregateTotalGroups(ctx, source, dim, where, args)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to query metrics breakdown total groups")
		} else {
			result.TotalGroups = total
		}
	}

	return result, nil
}

func (h *MetricsBreakdownHandler) queryAggregateTotalGroups(ctx context.Context, source, dim, where string, args []interface{}) (uint64, error) {
	sqlQuery := fmt.Sprintf(`
		SELECT uniqExactIf(%s, %s != '')
		FROM %s
		WHERE %s
	`, dim, dim, source, where)
	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total uint64
	if rows.Next() {
		if err := rows.Scan(&total); err != nil {
			return 0, err
		}
	}
	return total, rows.Err()
}

func (h *MetricsBreakdownHandler) queryOtelWindow(ctx context.Context, q *MetricsBreakdownQuery, startTime, endTime time.Time, tenantID string, restrictKeys []string, dimExpr string) (MetricsBreakdownResult, error) {
	valueExpr, err := breakdownTraceNameMetric(q.Metric)
	if err != nil {
		return MetricsBreakdownResult{}, err
	}

	conditions := []string{
		"Timestamp >= ?",
		"Timestamp <= ?",
		"ResourceAttributes['tenant.id'] = ?",
		"ParentSpanId = ''",
	}
	args := []interface{}{startTime, endTime, tenantID}

	if len(q.Environments) > 0 {
		placeholders := buildPlaceholders(len(q.Environments))
		conditions = append(conditions, fmt.Sprintf("ResourceAttributes['deployment.environment'] IN (%s)", placeholders))
		for _, e := range q.Environments {
			args = append(args, e)
		}
	}
	if len(restrictKeys) > 0 {
		placeholders := buildPlaceholders(len(restrictKeys))
		conditions = append(conditions, fmt.Sprintf("%s IN (%s)", dimExpr, placeholders))
		for _, key := range restrictKeys {
			args = append(args, key)
		}
	}

	where := joinConditions(conditions)
	sqlQuery := fmt.Sprintf(`
		SELECT
			%s as key,
			%s as value,
			count() as request_count
		FROM otel_traces
		WHERE %s
		GROUP BY key
		HAVING key != ''
		ORDER BY value DESC
		LIMIT ?
	`, dimExpr, valueExpr, where)

	queryArgs := append([]interface{}{}, args...)
	if len(restrictKeys) > 0 {
		queryArgs = append(queryArgs, len(restrictKeys))
	} else {
		queryArgs = append(queryArgs, q.Limit)
	}
	rows, err := h.conn.Query(ctx, sqlQuery, queryArgs...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query trace-name metrics breakdown")
		return MetricsBreakdownResult{}, fmt.Errorf("failed to query trace-name metrics breakdown: %w", err)
	}
	defer rows.Close()

	result := MetricsBreakdownResult{Rows: []MetricsBreakdownRowResult{}}
	for rows.Next() {
		var row MetricsBreakdownRowResult
		if err := rows.Scan(&row.Key, &row.Value, &row.RequestCount); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan trace-name metrics breakdown row")
			continue
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return MetricsBreakdownResult{}, fmt.Errorf("error iterating trace-name metrics breakdown: %w", err)
	}

	if len(restrictKeys) == 0 {
		totalSQL := fmt.Sprintf(`
			SELECT uniqExactIf(%s, %s != '')
			FROM otel_traces
			WHERE %s
		`, dimExpr, dimExpr, where)
		totalRows, err := h.conn.Query(ctx, totalSQL, args...)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to query trace-name metrics breakdown total groups")
		} else {
			defer totalRows.Close()
			if totalRows.Next() {
				if err := totalRows.Scan(&result.TotalGroups); err != nil {
					logger.WithFields("error", err.Error()).Warn("failed to scan trace-name metrics breakdown total groups")
				}
			}
		}
	}

	return result, nil
}

func breakdownAggregateDimension(groupBy string) (string, error) {
	switch groupBy {
	case "model":
		return "model", nil
	case "provider":
		return "provider", nil
	case "environment":
		return "environment", nil
	case "provider_api_key_id":
		return "provider_api_key_id", nil
	default:
		return "", fmt.Errorf("unsupported aggregate breakdown group_by: %s", groupBy)
	}
}

// breakdownAggregateSource picks the pre-aggregated table for a dimension.
// provider_api_key_id lives only on provider spans and is rolled up into a
// dedicated table (see mv_provider_key_metrics); every other aggregate
// dimension comes from trace_metrics_hourly.
func breakdownAggregateSource(groupBy string) string {
	if groupBy == "provider_api_key_id" {
		return "provider_key_metrics_hourly"
	}
	return "trace_metrics_hourly"
}

func breakdownAggregateMetric(metric string) (string, error) {
	switch metric {
	case "requests":
		return "sum(request_count) * 1.0", nil
	case "errors":
		return "sum(error_count) * 1.0", nil
	case "cost":
		return "sum(total_cost)", nil
	case "tokens":
		return "(sum(total_input_tokens) + sum(total_output_tokens)) * 1.0", nil
	default:
		return "", fmt.Errorf("unsupported breakdown metric: %s", metric)
	}
}

func breakdownTraceNameMetric(metric string) (string, error) {
	switch metric {
	case "requests":
		return "count() * 1.0", nil
	case "errors":
		return "countIf(" + otelstatus.IsError(otelstatus.Column) + ") * 1.0", nil
	case "cost":
		return fmt.Sprintf("sum(%s)", costSQL()), nil
	case "tokens":
		return fmt.Sprintf("sum(%s + %s) * 1.0", inputTokensSQL(), outputTokensSQL()), nil
	default:
		return "", fmt.Errorf("unsupported breakdown metric: %s", metric)
	}
}
