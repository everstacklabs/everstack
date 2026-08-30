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
)

// WorkflowMetricsHandler aggregates workflow performance metrics
type WorkflowMetricsHandler struct {
	conn clickhouse.Conn
}

func NewWorkflowMetricsHandler(conn clickhouse.Conn) *WorkflowMetricsHandler {
	return &WorkflowMetricsHandler{
		conn: conn,
	}
}

func (h *WorkflowMetricsHandler) QueryType() string {
	return "GetWorkflowMetrics"
}

// WorkflowMetricsQuery represents a query for workflow metrics
type WorkflowMetricsQuery struct {
	WorkflowID   string
	WorkflowType string
	TraceID      string
	FromTime     *time.Time
	ToTime       *time.Time
}

func (q *WorkflowMetricsQuery) QueryType() string {
	return "GetWorkflowMetrics"
}

func (q *WorkflowMetricsQuery) Validate() error {
	return nil
}

// WorkflowMetricsResult represents aggregated workflow performance
type WorkflowMetricsResult struct {
	WorkflowID        string
	WorkflowType      string
	WorkflowName      string
	TotalSteps        int32
	CompletedSteps    int32
	FailedSteps       int32
	TotalDurationNs   int64
	SuccessRate       float64
	Steps             []StepMetrics
	SlowestStep       *string
	SlowestStepDur    *int64
	MostExpensiveStep *string
	MostExpensiveCost *float64
}

// StepMetrics represents performance for a single step
type StepMetrics struct {
	StepNumber      uint32
	NodeName        string
	ObservationType string
	DurationNs      int64
	Cost            *float64
	Tokens          *int64
	Status          string
}

func (h *WorkflowMetricsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	metricsQuery, ok := q.(*WorkflowMetricsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for WorkflowMetricsHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — workflow metrics need a tenant scope.
		return []WorkflowMetricsResult{}, nil
	}

	whereConditions := []string{"t.SpanAttributes['tenant.id'] = ?"}
	args := []interface{}{tenantID}

	if metricsQuery.WorkflowID != "" {
		whereConditions = append(whereConditions, "wm.WorkflowId = ?")
		args = append(args, metricsQuery.WorkflowID)
	}

	if metricsQuery.WorkflowType != "" {
		whereConditions = append(whereConditions, "wm.WorkflowType = ?")
		args = append(args, metricsQuery.WorkflowType)
	}

	if metricsQuery.TraceID != "" {
		whereConditions = append(whereConditions, "wm.TraceId = ?")
		args = append(args, metricsQuery.TraceID)
	}

	if metricsQuery.FromTime != nil {
		whereConditions = append(whereConditions, "t.Timestamp >= ?")
		args = append(args, *metricsQuery.FromTime)
	}

	if metricsQuery.ToTime != nil {
		whereConditions = append(whereConditions, "t.Timestamp <= ?")
		args = append(args, *metricsQuery.ToTime)
	}

	whereClause := ""
	if len(whereConditions) > 0 {
		whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
	}

	// Query workflow-level metrics
	// Note: toInt64() cast ensures compatibility with both Int64 and UInt64 Duration schemas
	sqlQuery := fmt.Sprintf(`
		SELECT
			wm.WorkflowId,
			wm.WorkflowType,
			wm.WorkflowName,
			count(*) as total_steps,
			countIf(t.StatusCode = 'OK') as completed_steps,
			countIf(t.StatusCode = 'ERROR') as failed_steps,
			toInt64(sum(t.Duration)) as total_duration_ns,
			avgIf(1, t.StatusCode = 'OK') as success_rate
		FROM otel_workflow_metadata wm
		INNER JOIN otel_traces t ON wm.TraceId = t.TraceId
		%s
		GROUP BY wm.WorkflowId, wm.WorkflowType, wm.WorkflowName
	`, whereClause)

	logger.WithFields("query", sqlQuery, "args", args).Debug("executing workflow metrics query")

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query workflow metrics")
		return nil, fmt.Errorf("failed to query workflow metrics: %w", err)
	}
	defer rows.Close()

	var results []WorkflowMetricsResult
	for rows.Next() {
		result := WorkflowMetricsResult{}
		if err := rows.Scan(
			&result.WorkflowID,
			&result.WorkflowType,
			&result.WorkflowName,
			&result.TotalSteps,
			&result.CompletedSteps,
			&result.FailedSteps,
			&result.TotalDurationNs,
			&result.SuccessRate,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan workflow metrics row")
			continue
		}

		// Query step-level metrics for this workflow
		stepMetrics, err := h.queryStepMetrics(ctx, result.WorkflowID)
		if err != nil {
			logger.WithFields("workflow_id", result.WorkflowID, "error", err.Error()).Warn("failed to query step metrics")
		} else {
			result.Steps = stepMetrics

			// Find slowest and most expensive steps
			var slowestDur int64
			var highestCost float64

			for _, step := range stepMetrics {
				if step.DurationNs > slowestDur {
					slowestDur = step.DurationNs
					result.SlowestStep = &step.NodeName
					result.SlowestStepDur = &step.DurationNs
				}

				if step.Cost != nil && *step.Cost > highestCost {
					highestCost = *step.Cost
					result.MostExpensiveStep = &step.NodeName
					result.MostExpensiveCost = step.Cost
				}
			}
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating workflow metrics: %w", err)
	}

	return results, nil
}

func (h *WorkflowMetricsHandler) queryStepMetrics(ctx context.Context, workflowID string) ([]StepMetrics, error) {
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return []StepMetrics{}, nil
	}
	tenantFilter := "AND t.SpanAttributes['tenant.id'] = ?"

	sqlQuery := fmt.Sprintf(`
		SELECT
			t.StepNumber,
			t.NodeName,
			t.ObservationType,
			toInt64(t.Duration) as Duration,
			greatest(toFloat64OrZero(t.SpanAttributes['cost.estimated_usd']), toFloat64OrZero(t.SpanAttributes['llm.cost.total'])) as cost,
			toInt64OrZero(t.SpanAttributes['llm.usage.total_tokens']) as tokens,
			t.StatusCode
		FROM otel_workflow_metadata wm
		INNER JOIN otel_traces t ON wm.TraceId = t.TraceId
		WHERE wm.WorkflowId = ? AND t.StepNumber IS NOT NULL %s
		ORDER BY t.StepNumber ASC
	`, tenantFilter)

	queryArgs := []interface{}{workflowID, tenantID}

	rows, err := h.conn.Query(ctx, sqlQuery, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query step metrics: %w", err)
	}
	defer rows.Close()

	var steps []StepMetrics
	for rows.Next() {
		step := StepMetrics{}
		var cost float64
		var tokens int64

		if err := rows.Scan(
			&step.StepNumber,
			&step.NodeName,
			&step.ObservationType,
			&step.DurationNs,
			&cost,
			&tokens,
			&step.Status,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan step metrics row")
			continue
		}

		if cost > 0 {
			step.Cost = &cost
		}
		if tokens > 0 {
			step.Tokens = &tokens
		}

		steps = append(steps, step)
	}

	return steps, rows.Err()
}
