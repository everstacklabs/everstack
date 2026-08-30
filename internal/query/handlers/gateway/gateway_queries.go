package gateway

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/query"
)

// Gateway-specific queries for analytics and monitoring

// GetChatHistoryQuery retrieves chat conversation history.
type GetChatHistoryQuery struct {
	query.BaseQuery
	AggregateID string    `json:"aggregate_id,omitempty"`
	StartTime   time.Time `json:"start_time,omitempty"`
	EndTime     time.Time `json:"end_time,omitempty"`
}

func NewGetChatHistoryQuery(aggregateID string, startTime, endTime time.Time, userID, apiKey, traceID string) *GetChatHistoryQuery {
	return &GetChatHistoryQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			APIKey:    apiKey,
			TraceID:   traceID,
			Limit:     50, // Default limit
		},
		AggregateID: aggregateID,
		StartTime:   startTime,
		EndTime:     endTime,
	}
}

func (q GetChatHistoryQuery) QueryType() string { return "GetChatHistory" }

func (q GetChatHistoryQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	if q.Limit < 0 || q.Limit > 1000 {
		return fmt.Errorf("limit must be between 0 and 1000")
	}
	return nil
}

// GetModelUsageStatsQuery retrieves model usage analytics.
type GetModelUsageStatsQuery struct {
	query.BaseQuery
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	StartTime  time.Time `json:"start_time,omitempty"`
	EndTime    time.Time `json:"end_time,omitempty"`
	GroupBy    string    `json:"group_by,omitempty"`   // "hour", "day", "week", "month"
	Aggregator string    `json:"aggregator,omitempty"` // "count", "tokens", "latency"
}

func NewGetModelUsageStatsQuery(provider, model string, startTime, endTime time.Time, groupBy, aggregator, userID, traceID string) *GetModelUsageStatsQuery {
	return &GetModelUsageStatsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
			Limit:     100,
		},
		Provider:   provider,
		Model:      model,
		StartTime:  startTime,
		EndTime:    endTime,
		GroupBy:    groupBy,
		Aggregator: aggregator,
	}
}

func (q GetModelUsageStatsQuery) QueryType() string { return "GetModelUsageStats" }

func (q GetModelUsageStatsQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}

	validGroupBy := map[string]bool{
		"hour": true, "day": true, "week": true, "month": true,
	}
	if q.GroupBy != "" && !validGroupBy[q.GroupBy] {
		return fmt.Errorf("invalid group_by: %s", q.GroupBy)
	}

	validAggregator := map[string]bool{
		"count": true, "tokens": true, "latency": true,
	}
	if q.Aggregator != "" && !validAggregator[q.Aggregator] {
		return fmt.Errorf("invalid aggregator: %s", q.Aggregator)
	}

	return nil
}

// GetLoadBalancerStatsQuery retrieves load balancer performance data.
type GetLoadBalancerStatsQuery struct {
	query.BaseQuery
	Strategy  string    `json:"strategy,omitempty"`
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
}

func NewGetLoadBalancerStatsQuery(strategy string, startTime, endTime time.Time, userID, traceID string) *GetLoadBalancerStatsQuery {
	return &GetLoadBalancerStatsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
			Limit:     100,
		},
		Strategy:  strategy,
		StartTime: startTime,
		EndTime:   endTime,
	}
}

func (q GetLoadBalancerStatsQuery) QueryType() string { return "GetLoadBalancerStats" }

func (q GetLoadBalancerStatsQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	return nil
}

// GetActiveModelsQuery retrieves currently configured models.
type GetActiveModelsQuery struct {
	query.BaseQuery
	Provider string `json:"provider,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

func NewGetActiveModelsQuery(provider string, enabled *bool, userID, traceID string) *GetActiveModelsQuery {
	return &GetActiveModelsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		Provider: provider,
		Enabled:  enabled,
	}
}

func (q GetActiveModelsQuery) QueryType() string { return "GetActiveModels" }

func (q GetActiveModelsQuery) Validate() error { return nil }

// GetErrorRatesQuery retrieves error rate analytics.
type GetErrorRatesQuery struct {
	query.BaseQuery
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	ErrorType string    `json:"error_type,omitempty"`
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	GroupBy   string    `json:"group_by,omitempty"` // "hour", "day"
}

func NewGetErrorRatesQuery(provider, model, errorType string, startTime, endTime time.Time, groupBy, userID, traceID string) *GetErrorRatesQuery {
	return &GetErrorRatesQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
			Limit:     100,
		},
		Provider:  provider,
		Model:     model,
		ErrorType: errorType,
		StartTime: startTime,
		EndTime:   endTime,
		GroupBy:   groupBy,
	}
}

func (q GetErrorRatesQuery) QueryType() string { return "GetErrorRates" }

func (q GetErrorRatesQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}

	validGroupBy := map[string]bool{
		"hour": true, "day": true,
	}
	if q.GroupBy != "" && !validGroupBy[q.GroupBy] {
		return fmt.Errorf("invalid group_by: %s", q.GroupBy)
	}

	return nil
}

// GetRealTimeMetricsQuery retrieves current system metrics.
type GetRealTimeMetricsQuery struct {
	query.BaseQuery
}

func NewGetRealTimeMetricsQuery(userID, traceID string) *GetRealTimeMetricsQuery {
	return &GetRealTimeMetricsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
	}
}

func (q GetRealTimeMetricsQuery) QueryType() string { return "GetRealTimeMetrics" }

func (q GetRealTimeMetricsQuery) Validate() error { return nil }
