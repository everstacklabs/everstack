package enhanced

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

// BatchAnalyticsHandler provides pre-aggregated trace analytics
type BatchAnalyticsHandler struct {
	conn clickhouse.Conn
}

func NewBatchAnalyticsHandler(conn clickhouse.Conn) *BatchAnalyticsHandler {
	return &BatchAnalyticsHandler{
		conn: conn,
	}
}

func (h *BatchAnalyticsHandler) QueryType() string {
	return "GetTraceAnalytics"
}

// TraceAnalyticsQuery represents a query for trace analytics
type TraceAnalyticsQuery struct {
	TraceIDs []string
	FromTime *time.Time
	ToTime   *time.Time
	TenantID string
	Limit    int32
	Offset   int32
}

func (q *TraceAnalyticsQuery) QueryType() string {
	return "GetTraceAnalytics"
}

func (q *TraceAnalyticsQuery) Validate() error {
	// if q.Limit < 0 || q.Limit > 1000 {
	// 	return fmt.Errorf("limit must be between 0 and 1000")
	// }
	// if q.Offset < 0 {
	// 	return fmt.Errorf("offset must be greater than 0")
	// }
	// if q.FromTime != nil && q.ToTime != nil && q.FromTime.After(*q.ToTime) {
	// 	return fmt.Errorf("from_time cannot be after to_time")
	// }
	// if q.TenantID == "" {
	// 	return fmt.Errorf("tenant_id cannot be empty")
	// }
	// if len(q.TraceIDs) == 0 {
	// 	return fmt.Errorf("trace_ids cannot be empty")
	// }
	return nil
}

// TraceAnalyticsResult represents pre-aggregated trace statistics
type TraceAnalyticsResult struct {
	TraceID           string
	StartTime         time.Time
	EndTime           time.Time
	TotalDurationNs   int64
	TotalObservations int32
	ErrorCount        int32

	// Performance percentiles
	P50LatencyNs int64
	P95LatencyNs int64
	P99LatencyNs int64
	AvgLatencyNs int64
	MaxLatencyNs int64
	MinLatencyNs int64

	// Resource statistics
	TotalMemoryBytes int64
	PeakMemoryBytes  int64
	AvgCpuPercent    float64
	PeakCpuPercent   float64

	// Token and cost statistics
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCost         float64
	TotalSavings      float64
	TotalCarbonSaved  float64

	// Observation type breakdown
	ObservationTypeCounts    map[string]int32
	ObservationTypeDurations map[string]int64
}

func (h *BatchAnalyticsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	analyticsQuery, ok := q.(*TraceAnalyticsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for BatchAnalyticsHandler")
	}

	// Fail closed: tenant scoping is mandatory.
	tenantID := analyticsQuery.TenantID
	if tenantID == "" {
		tenantID = database.TenantSchemaFromContext(ctx)
	}
	if tenantID == "" {
		return []TraceAnalyticsResult{}, nil
	}

	whereConditions := []string{"SpanAttributes['tenant.id'] = ?"}
	args := []interface{}{tenantID}

	if len(analyticsQuery.TraceIDs) > 0 {
		placeholders := make([]string, len(analyticsQuery.TraceIDs))
		for i := range analyticsQuery.TraceIDs {
			placeholders[i] = "?"
			args = append(args, analyticsQuery.TraceIDs[i])
		}
		whereConditions = append(whereConditions, fmt.Sprintf("TraceId IN (%s)", strings.Join(placeholders, ",")))
	}

	if analyticsQuery.FromTime != nil {
		whereConditions = append(whereConditions, "Timestamp >= ?")
		args = append(args, *analyticsQuery.FromTime)
	}

	if analyticsQuery.ToTime != nil {
		whereConditions = append(whereConditions, "Timestamp <= ?")
		args = append(args, *analyticsQuery.ToTime)
	}

	whereClause := ""
	if len(whereConditions) > 0 {
		whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
	}

	limit := analyticsQuery.Limit
	if limit == 0 {
		limit = 100
	}

	// Query aggregated analytics
	// Note: toInt64() casts ensure compatibility with both Int64 and UInt64 Duration schemas
	sqlQuery := fmt.Sprintf(`
		SELECT
			TraceId,
			min(Timestamp) as start_time,
			max(Timestamp) as end_time,
			toInt64(sum(Duration)) as total_duration_ns,
			count(*) as total_observations,
			countIf(`+otelstatus.IsError(otelstatus.Column)+`) as error_count,
			
			-- Performance percentiles
			toInt64(quantile(0.50)(Duration)) as p50_latency_ns,
			toInt64(quantile(0.95)(Duration)) as p95_latency_ns,
			toInt64(quantile(0.99)(Duration)) as p99_latency_ns,
			toInt64(avg(Duration)) as avg_latency_ns,
			toInt64(max(Duration)) as max_latency_ns,
			toInt64(min(Duration)) as min_latency_ns,
			
			-- Token and cost statistics
			sum(toInt64OrZero(SpanAttributes['llm.usage.prompt_tokens'])) as total_input_tokens,
			sum(toInt64OrZero(SpanAttributes['llm.usage.completion_tokens'])) as total_output_tokens,
			sum(greatest(toFloat64OrZero(SpanAttributes['cost.estimated_usd']), toFloat64OrZero(SpanAttributes['llm.cost.total']))) as total_cost,
			sum(toFloat64OrZero(SpanAttributes['cost.savings_usd'])) as total_savings,
			sum(toFloat64OrZero(SpanAttributes['carbon.saved_grams'])) as total_carbon_saved,
			
			-- Observation type breakdown
			groupArray((ObservationType, 1)) as observation_type_counts,
			groupArray((ObservationType, toInt64(Duration))) as observation_type_durations
		FROM otel_traces
		%s
		GROUP BY TraceId
		ORDER BY start_time DESC
		LIMIT ? OFFSET ?
	`, whereClause)

	args = append(args, limit, analyticsQuery.Offset)

	logger.WithFields("query", sqlQuery, "args", args).Debug("executing trace analytics query")

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query trace analytics")
		return nil, fmt.Errorf("failed to query trace analytics: %w", err)
	}
	defer rows.Close()

	var results []TraceAnalyticsResult
	for rows.Next() {
		result := TraceAnalyticsResult{
			ObservationTypeCounts:    make(map[string]int32),
			ObservationTypeDurations: make(map[string]int64),
		}

		var obsTypeCounts [][]interface{}
		var obsTypeDurations [][]interface{}

		if err := rows.Scan(
			&result.TraceID,
			&result.StartTime,
			&result.EndTime,
			&result.TotalDurationNs,
			&result.TotalObservations,
			&result.ErrorCount,
			&result.P50LatencyNs,
			&result.P95LatencyNs,
			&result.P99LatencyNs,
			&result.AvgLatencyNs,
			&result.MaxLatencyNs,
			&result.MinLatencyNs,
			&result.TotalInputTokens,
			&result.TotalOutputTokens,
			&result.TotalCost,
			&result.TotalSavings,
			&result.TotalCarbonSaved,
			&obsTypeCounts,
			&obsTypeDurations,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan trace analytics row")
			continue
		}

		// Parse observation type counts
		for _, item := range obsTypeCounts {
			if len(item) >= 2 {
				if obsType, ok := item[0].(string); ok {
					if count, ok := item[1].(int32); ok {
						result.ObservationTypeCounts[obsType] = count
					}
				}
			}
		}

		// Parse observation type durations
		for _, item := range obsTypeDurations {
			if len(item) >= 2 {
				if obsType, ok := item[0].(string); ok {
					if duration, ok := item[1].(int64); ok {
						result.ObservationTypeDurations[obsType] = duration
					}
				}
			}
		}

		// Query resource metrics separately (not in main aggregation for performance)
		resourceMetrics, err := h.queryResourceMetrics(ctx, result.TraceID, tenantID)
		if err != nil {
			logger.WithFields("trace_id", result.TraceID, "error", err.Error()).Warn("failed to query resource metrics")
		} else {
			result.TotalMemoryBytes = resourceMetrics.TotalMemory
			result.PeakMemoryBytes = resourceMetrics.PeakMemory
			result.AvgCpuPercent = resourceMetrics.AvgCpu
			result.PeakCpuPercent = resourceMetrics.PeakCpu
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating trace analytics: %w", err)
	}

	return results, nil
}

type resourceMetricsData struct {
	TotalMemory int64
	PeakMemory  int64
	AvgCpu      float64
	PeakCpu     float64
}

func (h *BatchAnalyticsHandler) queryResourceMetrics(ctx context.Context, traceID, tenantID string) (*resourceMetricsData, error) {
	sqlQuery := `
		SELECT
			sum(MemoryUsedBytes) as total_memory,
			max(MemoryUsedBytes) as peak_memory,
			avg(CpuUsagePercent) as avg_cpu,
			max(CpuUsagePercent) as peak_cpu
		FROM otel_resource_metrics
		WHERE TraceId = ? AND ResourceAttributes['tenant.id'] = ?
	`

	row := h.conn.QueryRow(ctx, sqlQuery, traceID, tenantID)

	metrics := &resourceMetricsData{}
	if err := row.Scan(&metrics.TotalMemory, &metrics.PeakMemory, &metrics.AvgCpu, &metrics.PeakCpu); err != nil {
		return nil, fmt.Errorf("failed to scan resource metrics: %w", err)
	}

	return metrics, nil
}
