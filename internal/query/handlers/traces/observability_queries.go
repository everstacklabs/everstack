package traces

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/query"
	"github.com/google/uuid"
)

// ============================================================================
// Metrics Dashboard Query
// ============================================================================

// MetricsDashboardQuery requests aggregated dashboard KPIs
type MetricsDashboardQuery struct {
	query.BaseQuery
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Models       []string  `json:"models,omitempty"`
	Providers    []string  `json:"providers,omitempty"`
	Environments []string  `json:"environments,omitempty"`
	Compare      bool      `json:"compare,omitempty"`
}

func NewMetricsDashboardQuery(startTime, endTime time.Time, models, providers, environments []string, compare ...bool) *MetricsDashboardQuery {
	compareEnabled := len(compare) > 0 && compare[0]
	return &MetricsDashboardQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		StartTime:    startTime,
		EndTime:      endTime,
		Models:       models,
		Providers:    providers,
		Environments: environments,
		Compare:      compareEnabled,
	}
}

func (q MetricsDashboardQuery) QueryType() string { return "GetMetricsDashboard" }

func (q MetricsDashboardQuery) Validate() error {
	if q.StartTime.IsZero() || q.EndTime.IsZero() {
		return fmt.Errorf("start_time and end_time are required")
	}
	if q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	return nil
}

// ============================================================================
// Metrics Time Series Query
// ============================================================================

// MetricsTimeSeriesQuery requests time-bucketed metric data
type MetricsTimeSeriesQuery struct {
	query.BaseQuery
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Metric       string    `json:"metric"`      // "request_count", "error_count", "avg_latency_ms", "total_cost", "error_rate", "tokens", "ttft_p50", "ttft_p95"
	GroupBy      string    `json:"group_by"`    // "model", "provider", ""
	Granularity  string    `json:"granularity"` // "minute", "hour", "day"
	Models       []string  `json:"models,omitempty"`
	Providers    []string  `json:"providers,omitempty"`
	Environments []string  `json:"environments,omitempty"`
	Compare      bool      `json:"compare,omitempty"`
}

func NewMetricsTimeSeriesQuery(startTime, endTime time.Time, metric, groupBy, granularity string, models, providers, environments []string, compare ...bool) *MetricsTimeSeriesQuery {
	compareEnabled := len(compare) > 0 && compare[0]
	return &MetricsTimeSeriesQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		StartTime:    startTime,
		EndTime:      endTime,
		Metric:       metric,
		GroupBy:      groupBy,
		Granularity:  granularity,
		Models:       models,
		Providers:    providers,
		Environments: environments,
		Compare:      compareEnabled,
	}
}

func (q MetricsTimeSeriesQuery) QueryType() string { return "GetMetricsTimeSeries" }

func (q MetricsTimeSeriesQuery) Validate() error {
	if q.StartTime.IsZero() || q.EndTime.IsZero() {
		return fmt.Errorf("start_time and end_time are required")
	}
	if q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	validMetrics := map[string]bool{
		"request_count":     true,
		"error_count":       true,
		"avg_latency_ms":    true,
		"total_cost":        true,
		"error_rate":        true,
		"tokens":            true,
		"total_tokens":      true,
		"input_tokens":      true,
		"output_tokens":     true,
		"agent_turn_count":  true,
		"avg_agent_turn_ms": true,
		"ttft_p50":          true,
		"ttft_p95":          true,
	}
	if !validMetrics[q.Metric] {
		return fmt.Errorf("invalid metric: %s", q.Metric)
	}
	validGranularities := map[string]bool{
		"":        true,
		"minute":  true,
		"5minute": true,
		"hour":    true,
		"6hour":   true,
		"day":     true,
	}
	if !validGranularities[q.Granularity] {
		return fmt.Errorf("invalid granularity: %s", q.Granularity)
	}
	return nil
}

// ============================================================================
// Metrics Breakdown Query
// ============================================================================

// MetricsBreakdownQuery requests ranked grouped metrics for Top-N panels.
type MetricsBreakdownQuery struct {
	query.BaseQuery
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Metric       string    `json:"metric"`
	GroupBy      string    `json:"group_by"`
	Limit        int       `json:"limit"`
	Compare      bool      `json:"compare,omitempty"`
	Models       []string  `json:"models,omitempty"`
	Providers    []string  `json:"providers,omitempty"`
	Environments []string  `json:"environments,omitempty"`
}

func NewMetricsBreakdownQuery(startTime, endTime time.Time, metric, groupBy string, limit int, compare bool, models, providers, environments []string) *MetricsBreakdownQuery {
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}
	return &MetricsBreakdownQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			Limit:     limit,
		},
		StartTime:    startTime,
		EndTime:      endTime,
		Metric:       metric,
		GroupBy:      groupBy,
		Limit:        limit,
		Compare:      compare,
		Models:       models,
		Providers:    providers,
		Environments: environments,
	}
}

func (q MetricsBreakdownQuery) QueryType() string { return "GetMetricsBreakdown" }

func (q MetricsBreakdownQuery) Validate() error {
	if q.StartTime.IsZero() || q.EndTime.IsZero() {
		return fmt.Errorf("start_time and end_time are required")
	}
	if q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	validMetrics := map[string]bool{
		"requests": true,
		"errors":   true,
		"cost":     true,
		"tokens":   true,
	}
	if !validMetrics[q.Metric] {
		return fmt.Errorf("invalid breakdown metric: %s", q.Metric)
	}
	validGroups := map[string]bool{
		"model":       true,
		"provider":    true,
		"environment": true,
		"trace_name":  true,
		"session":     true,
		"user":        true,
	}
	if !validGroups[q.GroupBy] {
		return fmt.Errorf("invalid breakdown group_by: %s", q.GroupBy)
	}
	if q.Limit < 0 || q.Limit > 50 {
		return fmt.Errorf("limit must be between 0 and 50")
	}
	return nil
}

// ============================================================================
// Session Queries
// ============================================================================

// ListSessionsQuery requests paginated session aggregations
type ListSessionsQuery struct {
	query.BaseQuery
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
	Search    string    `json:"search,omitempty"`
	OrderBy   string    `json:"order_by,omitempty"` // "last_trace_at", "trace_count", "total_cost"
}

func NewListSessionsQuery(startTime, endTime time.Time, userID, search, orderBy string, limit, offset int) *ListSessionsQuery {
	if limit <= 0 {
		limit = 50
	}
	return &ListSessionsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			Limit:     limit,
			Offset:    offset,
		},
		StartTime: startTime,
		EndTime:   endTime,
		UserID:    userID,
		Search:    search,
		OrderBy:   orderBy,
	}
}

func (q ListSessionsQuery) QueryType() string { return "ListTraceSessions" }

func (q ListSessionsQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	return nil
}

// GetSessionQuery requests a single session by ID
type GetSessionQuery struct {
	query.BaseQuery
	SessionID string `json:"session_id"`
}

func NewGetSessionQuery(sessionID string) *GetSessionQuery {
	return &GetSessionQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		SessionID: sessionID,
	}
}

func (q GetSessionQuery) QueryType() string { return "GetTraceSession" }

func (q GetSessionQuery) Validate() error {
	if q.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	return nil
}

// ============================================================================
// User Queries
// ============================================================================

// ListUsersQuery requests paginated user aggregations
type ListUsersQuery struct {
	query.BaseQuery
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Search    string    `json:"search,omitempty"`
	OrderBy   string    `json:"order_by,omitempty"` // "last_seen", "trace_count", "total_cost"
}

func NewListUsersQuery(startTime, endTime time.Time, search, orderBy string, limit, offset int) *ListUsersQuery {
	if limit <= 0 {
		limit = 50
	}
	return &ListUsersQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			Limit:     limit,
			Offset:    offset,
		},
		StartTime: startTime,
		EndTime:   endTime,
		Search:    search,
		OrderBy:   orderBy,
	}
}

func (q ListUsersQuery) QueryType() string { return "ListTraceUsers" }

func (q ListUsersQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	return nil
}

// GetUserQuery requests a single user by ID
type GetUserQuery struct {
	query.BaseQuery
	TargetUserID string `json:"target_user_id"`
}

func NewGetUserQuery(userID string) *GetUserQuery {
	return &GetUserQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		TargetUserID: userID,
	}
}

func (q GetUserQuery) QueryType() string { return "GetTraceUser" }

func (q GetUserQuery) Validate() error {
	if q.TargetUserID == "" {
		return fmt.Errorf("user_id cannot be empty")
	}
	return nil
}

// ============================================================================
// Outcome Dashboard Query (scores-backed)
// ============================================================================

// OutcomeDashboardQuery requests aggregated outcome KPIs from otel_trace_scores
type OutcomeDashboardQuery struct {
	query.BaseQuery
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	AgentID   string    `json:"agent_id,omitempty"`
	// GroupBy slices verdict_rates by one or more of the canonical dimensions:
	// "model", "provider", "prompt_template_id", "prompt_version", "tool_name".
	// Empty = overall only.
	GroupBy []string `json:"group_by,omitempty"`
}

// OutcomeGroupByDimensions is the canonical set of dimensions accepted by
// OutcomeDashboardQuery.GroupBy and OutcomeTimeSeriesQuery.GroupBy.
var OutcomeGroupByDimensions = map[string]bool{
	"model":              true,
	"provider":           true,
	"prompt_template_id": true,
	"prompt_version":     true,
	"tool_name":          true,
}

func NewOutcomeDashboardQuery(startTime, endTime time.Time, agentID string, groupBy ...string) *OutcomeDashboardQuery {
	return &OutcomeDashboardQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		StartTime: startTime,
		EndTime:   endTime,
		AgentID:   agentID,
		GroupBy:   groupBy,
	}
}

func (q OutcomeDashboardQuery) QueryType() string { return "GetOutcomeDashboard" }

func (q OutcomeDashboardQuery) Validate() error {
	if q.StartTime.IsZero() || q.EndTime.IsZero() {
		return fmt.Errorf("start_time and end_time are required")
	}
	if q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	for _, d := range q.GroupBy {
		if !OutcomeGroupByDimensions[d] {
			return fmt.Errorf("invalid group_by dimension: %s", d)
		}
	}
	return nil
}

// ============================================================================
// Outcome Time Series Query
// ============================================================================

// OutcomeTimeSeriesQuery requests time-bucketed score data from otel_trace_scores
type OutcomeTimeSeriesQuery struct {
	query.BaseQuery
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	ScoreName   string    `json:"score_name"`  // e.g. "task_completion.finished"
	Aggregation string    `json:"aggregation"` // "avg", "rate_true", "rate_false", "count"
	Granularity string    `json:"granularity"` // "minute", "hour", "day"
	AgentID     string    `json:"agent_id,omitempty"`
	// GroupBy splits the time series by one of the canonical dimensions
	// (see OutcomeGroupByDimensions). Empty = single series.
	GroupBy string `json:"group_by,omitempty"`
}

func NewOutcomeTimeSeriesQuery(startTime, endTime time.Time, scoreName, aggregation, granularity, agentID, groupBy string) *OutcomeTimeSeriesQuery {
	return &OutcomeTimeSeriesQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		StartTime:   startTime,
		EndTime:     endTime,
		ScoreName:   scoreName,
		Aggregation: aggregation,
		Granularity: granularity,
		AgentID:     agentID,
		GroupBy:     groupBy,
	}
}

func (q OutcomeTimeSeriesQuery) QueryType() string { return "GetOutcomeTimeSeries" }

func (q OutcomeTimeSeriesQuery) Validate() error {
	if q.StartTime.IsZero() || q.EndTime.IsZero() {
		return fmt.Errorf("start_time and end_time are required")
	}
	if q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	if q.ScoreName == "" {
		return fmt.Errorf("score_name is required")
	}
	validAggs := map[string]bool{"avg": true, "rate_true": true, "rate_false": true, "count": true}
	if !validAggs[q.Aggregation] {
		return fmt.Errorf("invalid aggregation: %s", q.Aggregation)
	}
	validGranularities := map[string]bool{"": true, "minute": true, "5minute": true, "hour": true, "6hour": true, "day": true}
	if !validGranularities[q.Granularity] {
		return fmt.Errorf("invalid granularity: %s", q.Granularity)
	}
	if q.GroupBy != "" && !OutcomeGroupByDimensions[q.GroupBy] {
		return fmt.Errorf("invalid group_by dimension: %s", q.GroupBy)
	}
	return nil
}
