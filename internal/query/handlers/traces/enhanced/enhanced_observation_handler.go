package enhanced

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// EnhancedObservationHandler queries enhanced observations with workflow and performance data
type EnhancedObservationHandler struct {
	conn clickhouse.Conn
}

func NewEnhancedObservationHandler(conn clickhouse.Conn) *EnhancedObservationHandler {
	return &EnhancedObservationHandler{
		conn: conn,
	}
}

func (h *EnhancedObservationHandler) QueryType() string {
	return "StreamEnhancedObservations"
}

// EnhancedObservationQuery represents a query for enhanced observations
type EnhancedObservationQuery struct {
	TraceID          string
	WorkflowID       string
	ObservationTypes []string
	Nodes            []string
	MinStep          *uint32
	MaxStep          *uint32
	IncludeIO        bool
	IncludePerf      bool
	IncludeResources bool
}

func (q *EnhancedObservationQuery) QueryType() string {
	return "StreamEnhancedObservations"
}

func (q *EnhancedObservationQuery) Validate() error {
	return nil
}

// EnhancedObservationResult represents the result with all enhanced data
type EnhancedObservationResult struct {
	// Base observation fields
	ID                  string
	TraceID             string
	ParentObservationID string
	Name                string
	StartTime           int64 // Unix timestamp in nanoseconds
	EndTime             *int64
	Duration            int64 // int64 - SQL uses toInt64() for compatibility
	StatusCode          string
	StatusMessage       string

	// Enhanced fields
	Step            *uint32
	Node            *string
	ObservationType *string

	// Performance metrics
	QueueTimeNs         *int64
	ProcessingTimeNs    *int64
	NetworkLatencyNs    *int64
	SerializationTimeNs *int64
	DbQueryTimeNs       *int64
	CacheLookupTimeNs   *int64
	LlmTTFTNs           *int64
	LlmTimePerTokenNs   *int64

	// Resource metrics
	MemoryUsedBytes      *int64
	MemoryAllocatedBytes *int64
	CpuUsagePercent      *float64
	NetworkBytesSent     *int64
	NetworkBytesReceived *int64
	DiskReadBytes        *int64
	DiskWriteBytes       *int64
	ThreadCount          *int32

	// I/O data
	InputData      *string
	OutputData     *string
	InputTokens    *int64
	OutputTokens   *int64
	TotalTokens    *int64
	InputMimeType  *string
	OutputMimeType *string

	// Workflow context
	WorkflowID   *string
	WorkflowType *string
	WorkflowName *string
}

func (h *EnhancedObservationHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	enhancedQuery, ok := q.(*EnhancedObservationQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for EnhancedObservationHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — see metrics_dashboard.go for rationale.
		return nil, fmt.Errorf("trace not found")
	}

	// Build dynamic query based on what data is requested
	selectClauses := []string{
		"t.TraceId",
		"t.SpanId",
		"t.ParentSpanId",
		"t.SpanName",
		"toUnixTimestamp64Nano(t.Timestamp) as start_time_ns",
		"toUnixTimestamp64Nano(t.Timestamp + toIntervalNanosecond(t.Duration)) as end_time_ns",
		"toInt64(t.Duration) as Duration",
		"t.StatusCode",
		"t.StatusMessage",
		"t.StepNumber",
		"t.NodeName",
		"t.ObservationType",
	}

	joins := []string{}
	whereConditions := []string{"t.TraceId = ?", "t.SpanAttributes['tenant.id'] = ?"}
	args := []interface{}{enhancedQuery.TraceID, tenantID}

	// Add performance metrics if requested
	if enhancedQuery.IncludePerf {
		joins = append(joins, "LEFT JOIN otel_performance_metrics pm ON t.SpanId = pm.ObservationId AND t.TraceId = pm.TraceId")
		selectClauses = append(selectClauses,
			"pm.QueueTimeNs",
			"pm.ProcessingTimeNs",
			"pm.NetworkLatencyNs",
			"pm.SerializationTimeNs",
			"pm.DbQueryTimeNs",
			"pm.CacheLookupTimeNs",
			"pm.LlmTimeToFirstTokenNs",
			"pm.LlmTimePerTokenNs",
		)
	}

	// Add resource metrics if requested
	if enhancedQuery.IncludeResources {
		joins = append(joins, "LEFT JOIN otel_resource_metrics rm ON t.SpanId = rm.ObservationId AND t.TraceId = rm.TraceId")
		selectClauses = append(selectClauses,
			"rm.MemoryUsedBytes",
			"rm.MemoryAllocatedBytes",
			"rm.CpuUsagePercent",
			"rm.NetworkBytesSent",
			"rm.NetworkBytesReceived",
			"rm.DiskReadBytes",
			"rm.DiskWriteBytes",
			"rm.ThreadCount",
		)
	}

	// Add I/O data if requested
	if enhancedQuery.IncludeIO {
		joins = append(joins, "LEFT JOIN otel_observation_io io ON t.SpanId = io.ObservationId AND t.TraceId = io.TraceId")
		selectClauses = append(selectClauses,
			"io.InputData",
			"io.OutputData",
			"io.InputTokens",
			"io.OutputTokens",
			"io.TotalTokens",
			"io.InputMimeType",
			"io.OutputMimeType",
		)
	}

	// Add workflow context
	joins = append(joins, "LEFT JOIN otel_workflow_metadata wm ON t.TraceId = wm.TraceId")
	selectClauses = append(selectClauses,
		"wm.WorkflowId",
		"wm.WorkflowType",
		"wm.WorkflowName",
	)

	// Add filters
	if enhancedQuery.WorkflowID != "" {
		whereConditions = append(whereConditions, "wm.WorkflowId = ?")
		args = append(args, enhancedQuery.WorkflowID)
	}

	if len(enhancedQuery.ObservationTypes) > 0 {
		placeholders := make([]string, len(enhancedQuery.ObservationTypes))
		for i := range enhancedQuery.ObservationTypes {
			placeholders[i] = "?"
			args = append(args, enhancedQuery.ObservationTypes[i])
		}
		whereConditions = append(whereConditions, fmt.Sprintf("t.ObservationType IN (%s)", strings.Join(placeholders, ",")))
	}

	if len(enhancedQuery.Nodes) > 0 {
		placeholders := make([]string, len(enhancedQuery.Nodes))
		for i := range enhancedQuery.Nodes {
			placeholders[i] = "?"
			args = append(args, enhancedQuery.Nodes[i])
		}
		whereConditions = append(whereConditions, fmt.Sprintf("t.NodeName IN (%s)", strings.Join(placeholders, ",")))
	}

	if enhancedQuery.MinStep != nil {
		whereConditions = append(whereConditions, "t.StepNumber >= ?")
		args = append(args, *enhancedQuery.MinStep)
	}

	if enhancedQuery.MaxStep != nil {
		whereConditions = append(whereConditions, "t.StepNumber <= ?")
		args = append(args, *enhancedQuery.MaxStep)
	}

	// Build final query
	sqlQuery := fmt.Sprintf(`
		SELECT %s
		FROM otel_traces t
		%s
		WHERE %s
		ORDER BY t.StepNumber ASC, t.Timestamp ASC
	`, strings.Join(selectClauses, ", "), strings.Join(joins, " "), strings.Join(whereConditions, " AND "))

	logger.WithFields("query", sqlQuery, "args", args).Debug("executing enhanced observation query")

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query enhanced observations")
		return nil, fmt.Errorf("failed to query enhanced observations: %w", err)
	}
	defer rows.Close()

	var results []EnhancedObservationResult
	for rows.Next() {
		result := EnhancedObservationResult{}

		// Prepare scan destinations based on what was selected
		scanDest := []interface{}{
			&result.TraceID,
			&result.ID,
			&result.ParentObservationID,
			&result.Name,
			&result.StartTime,
			&result.EndTime,
			&result.Duration,
			&result.StatusCode,
			&result.StatusMessage,
			&result.Step,
			&result.Node,
			&result.ObservationType,
		}

		if enhancedQuery.IncludePerf {
			scanDest = append(scanDest,
				&result.QueueTimeNs,
				&result.ProcessingTimeNs,
				&result.NetworkLatencyNs,
				&result.SerializationTimeNs,
				&result.DbQueryTimeNs,
				&result.CacheLookupTimeNs,
				&result.LlmTTFTNs,
				&result.LlmTimePerTokenNs,
			)
		}

		if enhancedQuery.IncludeResources {
			scanDest = append(scanDest,
				&result.MemoryUsedBytes,
				&result.MemoryAllocatedBytes,
				&result.CpuUsagePercent,
				&result.NetworkBytesSent,
				&result.NetworkBytesReceived,
				&result.DiskReadBytes,
				&result.DiskWriteBytes,
				&result.ThreadCount,
			)
		}

		if enhancedQuery.IncludeIO {
			scanDest = append(scanDest,
				&result.InputData,
				&result.OutputData,
				&result.InputTokens,
				&result.OutputTokens,
				&result.TotalTokens,
				&result.InputMimeType,
				&result.OutputMimeType,
			)
		}

		scanDest = append(scanDest,
			&result.WorkflowID,
			&result.WorkflowType,
			&result.WorkflowName,
		)

		if err := rows.Scan(scanDest...); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan enhanced observation row")
			continue
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating enhanced observations: %w", err)
	}

	return results, nil
}
