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

// OutcomeTimeSeriesHandler queries time-bucketed score data from otel_trace_scores.
type OutcomeTimeSeriesHandler struct {
	conn clickhouse.Conn
}

func NewOutcomeTimeSeriesHandler(conn clickhouse.Conn) *OutcomeTimeSeriesHandler {
	return &OutcomeTimeSeriesHandler{conn: conn}
}

func (h *OutcomeTimeSeriesHandler) QueryType() string {
	return "GetOutcomeTimeSeries"
}

func (h *OutcomeTimeSeriesHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	tsq, ok := q.(*OutcomeTimeSeriesQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for OutcomeTimeSeriesHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — see metrics_dashboard.go for rationale.
		return []MetricsTimeSeriesResult{}, nil
	}

	// Time bucket
	var timeBucket string
	switch tsq.Granularity {
	case "minute":
		timeBucket = "toStartOfMinute(Timestamp)"
	case "5minute":
		timeBucket = "toStartOfFiveMinutes(Timestamp)"
	case "6hour":
		timeBucket = "toStartOfInterval(Timestamp, INTERVAL 6 HOUR)"
	case "day":
		timeBucket = "toStartOfDay(Timestamp)"
	default:
		timeBucket = "toStartOfHour(Timestamp)"
	}

	// Aggregation expression
	var valueExpr string
	switch tsq.Aggregation {
	case "avg":
		valueExpr = "CAST(ifNotFinite(avg(NumericValue), 0) AS Float64)"
	case "rate_true":
		valueExpr = "CAST(countIf(BooleanValue = 1) / greatest(count(), 1) AS Float64)"
	case "rate_false":
		valueExpr = "CAST(countIf(BooleanValue = 0) / greatest(count(), 1) AS Float64)"
	case "count":
		valueExpr = "CAST(count() AS Float64)"
	default:
		valueExpr = "CAST(ifNotFinite(avg(NumericValue), 0) AS Float64)"
	}

	// WHERE clause — tenant_id is mandatory (early-return above)
	whereConditions := []string{"Timestamp >= ?", "Timestamp <= ?", "Source = 'EVAL'", "Name = ?", "Environment = ?"}
	args := []interface{}{tsq.StartTime, tsq.EndTime, tsq.ScoreName, tenantID}

	if tsq.AgentID != "" {
		whereConditions = append(whereConditions, "ObservationId = ?")
		args = append(args, tsq.AgentID)
	}

	sqlQuery := fmt.Sprintf(`
		SELECT
			%s as period,
			%s as value
		FROM otel_trace_scores
		WHERE %s
		GROUP BY period
		ORDER BY period ASC
	`, timeBucket, valueExpr, joinConditions(whereConditions))

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithError(err).Error("outcome timeseries: query failed")
		return nil, fmt.Errorf("outcome timeseries query: %w", err)
	}
	defer rows.Close()

	var buckets []TimeSeriesBucketResult
	for rows.Next() {
		var period time.Time
		var value float64
		if err := rows.Scan(&period, &value); err != nil {
			logger.WithError(err).Warn("outcome timeseries: row scan failed")
			continue
		}
		buckets = append(buckets, TimeSeriesBucketResult{
			Timestamp: period,
			Value:     value,
			Label:     tsq.ScoreName,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outcome timeseries iteration: %w", err)
	}

	if buckets == nil {
		buckets = []TimeSeriesBucketResult{}
	}

	results := []MetricsTimeSeriesResult{
		{
			MetricName: tsq.ScoreName,
			Buckets:    buckets,
		},
	}

	return results, nil
}
