package enhanced

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// PerformanceBreakdownHandler provides detailed performance analysis
type PerformanceBreakdownHandler struct {
	conn clickhouse.Conn
}

func NewPerformanceBreakdownHandler(conn clickhouse.Conn) *PerformanceBreakdownHandler {
	return &PerformanceBreakdownHandler{
		conn: conn,
	}
}

func (h *PerformanceBreakdownHandler) QueryType() string {
	return "GetPerformanceBreakdown"
}

// PerformanceBreakdownQuery represents a query for performance breakdown
type PerformanceBreakdownQuery struct {
	TraceID       string
	ObservationID string // Optional: breakdown for single observation
	GroupByNode   bool
	GroupByType   bool
}

func (q *PerformanceBreakdownQuery) QueryType() string {
	return "GetPerformanceBreakdown"
}

func (q *PerformanceBreakdownQuery) Validate() error {
	return nil
}

// PerformanceBreakdownResult represents detailed performance analysis
type PerformanceBreakdownResult struct {
	Entries      []PerformanceEntry
	TotalMetrics PerformanceMetricsData
}

// PerformanceEntry represents performance for a group or single observation
type PerformanceEntry struct {
	Node              *string
	ObservationType   *string
	ObservationID     *string
	Metrics           PerformanceMetricsData
	PercentageOfTotal float64
}

// PerformanceMetricsData represents all performance timing data
type PerformanceMetricsData struct {
	QueueTimeNs         int64
	ProcessingTimeNs    int64
	NetworkLatencyNs    int64
	SerializationTimeNs int64
	DbQueryTimeNs       int64
	CacheLookupTimeNs   int64
	LlmTTFTNs           int64
	LlmTimePerTokenNs   int64
	TotalTimeNs         int64
}

func (h *PerformanceBreakdownHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	breakdownQuery, ok := q.(*PerformanceBreakdownQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for PerformanceBreakdownHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return &PerformanceBreakdownResult{
			Entries:      []PerformanceEntry{},
			TotalMetrics: PerformanceMetricsData{},
		}, nil
	}

	// First, get total metrics for the trace
	totalMetrics, err := h.queryTotalMetrics(ctx, breakdownQuery.TraceID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query total metrics: %w", err)
	}

	// If specific observation requested, return just that one
	if breakdownQuery.ObservationID != "" {
		entry, err := h.queryObservationMetrics(ctx, breakdownQuery.TraceID, breakdownQuery.ObservationID, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to query observation metrics: %w", err)
		}

		if totalMetrics.TotalTimeNs > 0 {
			entry.PercentageOfTotal = float64(entry.Metrics.TotalTimeNs) / float64(totalMetrics.TotalTimeNs) * 100
		}

		return &PerformanceBreakdownResult{
			Entries:      []PerformanceEntry{*entry},
			TotalMetrics: *totalMetrics,
		}, nil
	}

	// Otherwise, get grouped breakdown
	var entries []PerformanceEntry

	if breakdownQuery.GroupByNode && breakdownQuery.GroupByType {
		entries, err = h.queryGroupedByNodeAndType(ctx, breakdownQuery.TraceID, tenantID)
	} else if breakdownQuery.GroupByNode {
		entries, err = h.queryGroupedByNode(ctx, breakdownQuery.TraceID, tenantID)
	} else if breakdownQuery.GroupByType {
		entries, err = h.queryGroupedByType(ctx, breakdownQuery.TraceID, tenantID)
	} else {
		// Return all observations individually
		entries, err = h.queryAllObservations(ctx, breakdownQuery.TraceID, tenantID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query performance breakdown: %w", err)
	}

	// Calculate percentages
	if totalMetrics.TotalTimeNs > 0 {
		for i := range entries {
			entries[i].PercentageOfTotal = float64(entries[i].Metrics.TotalTimeNs) / float64(totalMetrics.TotalTimeNs) * 100
		}
	}

	return &PerformanceBreakdownResult{
		Entries:      entries,
		TotalMetrics: *totalMetrics,
	}, nil
}

func (h *PerformanceBreakdownHandler) queryTotalMetrics(ctx context.Context, traceID, tenantID string) (*PerformanceMetricsData, error) {
	sqlQuery := `
		SELECT
			sum(QueueTimeNs) as total_queue_time,
			sum(ProcessingTimeNs) as total_processing_time,
			sum(NetworkLatencyNs) as total_network_latency,
			sum(SerializationTimeNs) as total_serialization_time,
			sum(DbQueryTimeNs) as total_db_query_time,
			sum(CacheLookupTimeNs) as total_cache_lookup_time,
			sum(LlmTimeToFirstTokenNs) as total_llm_ttft,
			sum(LlmTimePerTokenNs) as total_llm_time_per_token,
			sum(ProcessingTimeNs + NetworkLatencyNs + SerializationTimeNs + DbQueryTimeNs + CacheLookupTimeNs) as total_time
		FROM otel_performance_metrics
		WHERE TraceId = ? AND SpanAttributes['tenant.id'] = ?
	`

	args := []interface{}{traceID, tenantID}

	row := h.conn.QueryRow(ctx, sqlQuery, args...)

	metrics := &PerformanceMetricsData{}
	if err := row.Scan(
		&metrics.QueueTimeNs,
		&metrics.ProcessingTimeNs,
		&metrics.NetworkLatencyNs,
		&metrics.SerializationTimeNs,
		&metrics.DbQueryTimeNs,
		&metrics.CacheLookupTimeNs,
		&metrics.LlmTTFTNs,
		&metrics.LlmTimePerTokenNs,
		&metrics.TotalTimeNs,
	); err != nil {
		return nil, fmt.Errorf("failed to scan total metrics: %w", err)
	}

	return metrics, nil
}

func (h *PerformanceBreakdownHandler) queryObservationMetrics(ctx context.Context, traceID, observationID, tenantID string) (*PerformanceEntry, error) {
	sqlQuery := `
		SELECT
			t.NodeName,
			t.ObservationType,
			pm.QueueTimeNs,
			pm.ProcessingTimeNs,
			pm.NetworkLatencyNs,
			pm.SerializationTimeNs,
			pm.DbQueryTimeNs,
			pm.CacheLookupTimeNs,
			pm.LlmTimeToFirstTokenNs,
			pm.LlmTimePerTokenNs,
			(pm.ProcessingTimeNs + pm.NetworkLatencyNs + pm.SerializationTimeNs + pm.DbQueryTimeNs + pm.CacheLookupTimeNs) as total_time
		FROM otel_performance_metrics pm
		INNER JOIN otel_traces t ON pm.ObservationId = t.SpanId AND pm.TraceId = t.TraceId
		WHERE pm.TraceId = ? AND pm.ObservationId = ? AND t.SpanAttributes['tenant.id'] = ?
	`

	args := []interface{}{traceID, observationID, tenantID}

	row := h.conn.QueryRow(ctx, sqlQuery, args...)

	entry := &PerformanceEntry{
		ObservationID: &observationID,
	}

	if err := row.Scan(
		&entry.Node,
		&entry.ObservationType,
		&entry.Metrics.QueueTimeNs,
		&entry.Metrics.ProcessingTimeNs,
		&entry.Metrics.NetworkLatencyNs,
		&entry.Metrics.SerializationTimeNs,
		&entry.Metrics.DbQueryTimeNs,
		&entry.Metrics.CacheLookupTimeNs,
		&entry.Metrics.LlmTTFTNs,
		&entry.Metrics.LlmTimePerTokenNs,
		&entry.Metrics.TotalTimeNs,
	); err != nil {
		return nil, fmt.Errorf("failed to scan observation metrics: %w", err)
	}

	return entry, nil
}

func (h *PerformanceBreakdownHandler) queryGroupedByNode(ctx context.Context, traceID, tenantID string) ([]PerformanceEntry, error) {
	sqlQuery := `
		SELECT
			t.NodeName,
			sum(pm.QueueTimeNs) as total_queue_time,
			sum(pm.ProcessingTimeNs) as total_processing_time,
			sum(pm.NetworkLatencyNs) as total_network_latency,
			sum(pm.SerializationTimeNs) as total_serialization_time,
			sum(pm.DbQueryTimeNs) as total_db_query_time,
			sum(pm.CacheLookupTimeNs) as total_cache_lookup_time,
			sum(pm.LlmTimeToFirstTokenNs) as total_llm_ttft,
			sum(pm.LlmTimePerTokenNs) as total_llm_time_per_token,
			sum(pm.ProcessingTimeNs + pm.NetworkLatencyNs + pm.SerializationTimeNs + pm.DbQueryTimeNs + pm.CacheLookupTimeNs) as total_time
		FROM otel_performance_metrics pm
		INNER JOIN otel_traces t ON pm.ObservationId = t.SpanId AND pm.TraceId = t.TraceId
		WHERE pm.TraceId = ? AND t.SpanAttributes['tenant.id'] = ?
		GROUP BY t.NodeName
		ORDER BY total_time DESC
	`

	args := []interface{}{traceID, tenantID}

	return h.queryGroupedMetrics(ctx, sqlQuery, args, true, false)
}

func (h *PerformanceBreakdownHandler) queryGroupedByType(ctx context.Context, traceID, tenantID string) ([]PerformanceEntry, error) {
	sqlQuery := `
		SELECT
			t.ObservationType,
			sum(pm.QueueTimeNs) as total_queue_time,
			sum(pm.ProcessingTimeNs) as total_processing_time,
			sum(pm.NetworkLatencyNs) as total_network_latency,
			sum(pm.SerializationTimeNs) as total_serialization_time,
			sum(pm.DbQueryTimeNs) as total_db_query_time,
			sum(pm.CacheLookupTimeNs) as total_cache_lookup_time,
			sum(pm.LlmTimeToFirstTokenNs) as total_llm_ttft,
			sum(pm.LlmTimePerTokenNs) as total_llm_time_per_token,
			sum(pm.ProcessingTimeNs + pm.NetworkLatencyNs + pm.SerializationTimeNs + pm.DbQueryTimeNs + pm.CacheLookupTimeNs) as total_time
		FROM otel_performance_metrics pm
		INNER JOIN otel_traces t ON pm.ObservationId = t.SpanId AND pm.TraceId = t.TraceId
		WHERE pm.TraceId = ? AND t.SpanAttributes['tenant.id'] = ?
		GROUP BY t.ObservationType
		ORDER BY total_time DESC
	`

	args := []interface{}{traceID, tenantID}

	return h.queryGroupedMetrics(ctx, sqlQuery, args, false, true)
}

func (h *PerformanceBreakdownHandler) queryGroupedByNodeAndType(ctx context.Context, traceID, tenantID string) ([]PerformanceEntry, error) {
	sqlQuery := `
		SELECT
			t.NodeName,
			t.ObservationType,
			sum(pm.QueueTimeNs) as total_queue_time,
			sum(pm.ProcessingTimeNs) as total_processing_time,
			sum(pm.NetworkLatencyNs) as total_network_latency,
			sum(pm.SerializationTimeNs) as total_serialization_time,
			sum(pm.DbQueryTimeNs) as total_db_query_time,
			sum(pm.CacheLookupTimeNs) as total_cache_lookup_time,
			sum(pm.LlmTimeToFirstTokenNs) as total_llm_ttft,
			sum(pm.LlmTimePerTokenNs) as total_llm_time_per_token,
			sum(pm.ProcessingTimeNs + pm.NetworkLatencyNs + pm.SerializationTimeNs + pm.DbQueryTimeNs + pm.CacheLookupTimeNs) as total_time
		FROM otel_performance_metrics pm
		INNER JOIN otel_traces t ON pm.ObservationId = t.SpanId AND pm.TraceId = t.TraceId
		WHERE pm.TraceId = ? AND t.SpanAttributes['tenant.id'] = ?
		GROUP BY t.NodeName, t.ObservationType
		ORDER BY total_time DESC
	`

	args := []interface{}{traceID, tenantID}

	return h.queryGroupedMetrics(ctx, sqlQuery, args, true, true)
}

func (h *PerformanceBreakdownHandler) queryGroupedMetrics(ctx context.Context, sqlQuery string, args []interface{}, hasNode, hasType bool) ([]PerformanceEntry, error) {
	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query grouped metrics")
		return nil, fmt.Errorf("failed to query grouped metrics: %w", err)
	}
	defer rows.Close()

	var entries []PerformanceEntry
	for rows.Next() {
		entry := PerformanceEntry{}

		scanDest := []interface{}{}
		if hasNode {
			scanDest = append(scanDest, &entry.Node)
		}
		if hasType {
			scanDest = append(scanDest, &entry.ObservationType)
		}
		scanDest = append(scanDest,
			&entry.Metrics.QueueTimeNs,
			&entry.Metrics.ProcessingTimeNs,
			&entry.Metrics.NetworkLatencyNs,
			&entry.Metrics.SerializationTimeNs,
			&entry.Metrics.DbQueryTimeNs,
			&entry.Metrics.CacheLookupTimeNs,
			&entry.Metrics.LlmTTFTNs,
			&entry.Metrics.LlmTimePerTokenNs,
			&entry.Metrics.TotalTimeNs,
		)

		if err := rows.Scan(scanDest...); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan grouped metrics row")
			continue
		}

		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

func (h *PerformanceBreakdownHandler) queryAllObservations(ctx context.Context, traceID, tenantID string) ([]PerformanceEntry, error) {
	sqlQuery := `
		SELECT
			t.SpanId,
			t.NodeName,
			t.ObservationType,
			pm.QueueTimeNs,
			pm.ProcessingTimeNs,
			pm.NetworkLatencyNs,
			pm.SerializationTimeNs,
			pm.DbQueryTimeNs,
			pm.CacheLookupTimeNs,
			pm.LlmTimeToFirstTokenNs,
			pm.LlmTimePerTokenNs,
			(pm.ProcessingTimeNs + pm.NetworkLatencyNs + pm.SerializationTimeNs + pm.DbQueryTimeNs + pm.CacheLookupTimeNs) as total_time
		FROM otel_performance_metrics pm
		INNER JOIN otel_traces t ON pm.ObservationId = t.SpanId AND pm.TraceId = t.TraceId
		WHERE pm.TraceId = ? AND t.SpanAttributes['tenant.id'] = ?
		ORDER BY total_time DESC
	`

	args := []interface{}{traceID, tenantID}

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query all observations")
		return nil, fmt.Errorf("failed to query all observations: %w", err)
	}
	defer rows.Close()

	var entries []PerformanceEntry
	for rows.Next() {
		entry := PerformanceEntry{}
		var obsID string

		if err := rows.Scan(
			&obsID,
			&entry.Node,
			&entry.ObservationType,
			&entry.Metrics.QueueTimeNs,
			&entry.Metrics.ProcessingTimeNs,
			&entry.Metrics.NetworkLatencyNs,
			&entry.Metrics.SerializationTimeNs,
			&entry.Metrics.DbQueryTimeNs,
			&entry.Metrics.CacheLookupTimeNs,
			&entry.Metrics.LlmTTFTNs,
			&entry.Metrics.LlmTimePerTokenNs,
			&entry.Metrics.TotalTimeNs,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan observation row")
			continue
		}

		entry.ObservationID = &obsID
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}
