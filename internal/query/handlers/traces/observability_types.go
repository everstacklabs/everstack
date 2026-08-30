package traces

import "time"

// MetricsDashboardResult represents the aggregated KPIs for the dashboard
type MetricsDashboardResult struct {
	TotalRequests     uint64  `json:"total_requests"`
	TotalErrors       uint64  `json:"total_errors"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	TotalCost         float64 `json:"total_cost"`
	ErrorRate         float64 `json:"error_rate"`
	TotalTokens       int64   `json:"total_tokens"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
	UniqueModels      uint64  `json:"unique_models"`
	UniqueProviders   uint64  `json:"unique_providers"`
	// Agent loop counters split from gateway request counts.
	TotalAgentTurns uint64  `json:"total_agent_turns"`
	AvgAgentTurnMs  float64 `json:"avg_agent_turn_ms"`
	// Latency percentiles in ms across span durations in window.
	P50LatencyMs float64 `json:"p50_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
	P99LatencyMs float64 `json:"p99_latency_ms"`
	TtftP50Ms    float64 `json:"ttft_p50_ms"`
	TtftP95Ms    float64 `json:"ttft_p95_ms"`
}

// MetricsDashboardCompareResult wraps current and previous windows for compare.
type MetricsDashboardCompareResult struct {
	Current  MetricsDashboardResult `json:"current"`
	Previous MetricsDashboardResult `json:"previous"`
}

// TimeSeriesBucketResult represents a single time-series data point
type TimeSeriesBucketResult struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Label     string    `json:"label"`
}

// MetricsTimeSeriesResult represents a named time series
type MetricsTimeSeriesResult struct {
	MetricName string                   `json:"metric_name"`
	Buckets    []TimeSeriesBucketResult `json:"buckets"`
}

// MetricsTimeSeriesCompareResult wraps current and previous windows for compare.
type MetricsTimeSeriesCompareResult struct {
	Current  []MetricsTimeSeriesResult `json:"current"`
	Previous []MetricsTimeSeriesResult `json:"previous"`
}

// MetricsBreakdownRowResult represents one Top-N grouped metric row.
type MetricsBreakdownRowResult struct {
	Key           string  `json:"key"`
	Value         float64 `json:"value"`
	RequestCount  uint64  `json:"request_count"`
	PreviousValue float64 `json:"previous_value"`
	// Provider is the dominant provider for the row, populated only for
	// group_by = "model". Empty for other dimensions.
	Provider string `json:"provider"`
}

// MetricsBreakdownResult wraps ranked grouped metrics.
type MetricsBreakdownResult struct {
	Rows        []MetricsBreakdownRowResult `json:"rows"`
	TotalGroups uint64                      `json:"total_groups"`
}

// SessionReadModel represents a row from the trace_sessions table
type SessionReadModel struct {
	TenantID          string    `json:"tenant_id"`
	SessionID         string    `json:"session_id"`
	UserID            string    `json:"user_id"`
	FirstTraceAt      time.Time `json:"first_trace_at"`
	LastTraceAt       time.Time `json:"last_trace_at"`
	TraceCount        uint32    `json:"trace_count"`
	TotalDurationNs   uint64    `json:"total_duration_ns"`
	TotalInputTokens  uint64    `json:"total_input_tokens"`
	TotalOutputTokens uint64    `json:"total_output_tokens"`
	TotalCost         float64   `json:"total_cost"`
	ErrorCount        uint32    `json:"error_count"`
	Models            []string  `json:"models"`
	Tags              []string  `json:"tags"`
	Environment       string    `json:"environment"`
	// Kinds is the set of execution kinds present in the session (e.g.
	// ["agent"], ["workflow", "llm"], ["sandbox"]), derived from each trace's
	// root span so the Sessions view can show what a session actually is.
	Kinds []string `json:"kinds"`
}

// UserReadModel represents a row from the trace_users table
type UserReadModel struct {
	TenantID     string    `json:"tenant_id"`
	UserID       string    `json:"user_id"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	SessionCount uint32    `json:"session_count"`
	TraceCount   uint32    `json:"trace_count"`
	TotalTokens  uint64    `json:"total_tokens"`
	TotalCost    float64   `json:"total_cost"`
	ErrorRate    float64   `json:"error_rate"`
	AvgLatencyNs uint64    `json:"avg_latency_ns"`
}

// SessionListResult wraps paginated session results
type SessionListResult struct {
	Sessions   []SessionReadModel `json:"sessions"`
	TotalCount uint64             `json:"total_count"`
}

// UserListResult wraps paginated user results
type UserListResult struct {
	Users      []UserReadModel `json:"users"`
	TotalCount uint64          `json:"total_count"`
}

// OutcomeDashboardResult contains aggregated outcome KPIs from auto-scoring
type OutcomeDashboardResult struct {
	TaskCompletionRate   float64               `json:"task_completion_rate"`
	ToolSuccessRate      float64               `json:"tool_success_rate"`
	PolicyComplianceRate float64               `json:"policy_compliance_rate"`
	LoopHealthRate       float64               `json:"loop_health_rate"`
	IterationEfficiency  float64               `json:"iteration_efficiency"`
	SandboxSuccessRate   float64               `json:"sandbox_success_rate"`
	TotalEvaluations     uint64                `json:"total_evaluations"`
	UniqueSessions       uint64                `json:"unique_sessions"`
	Scores               []OutcomeScoreSummary `json:"scores"`
	// VerdictRates holds the overall fix_attempt_verdict win/fail/draw/no_change
	// distribution over the same filter window.
	VerdictRates VerdictRates `json:"verdict_rates"`
	// VerdictBreakdowns slices verdict rates by each requested group_by
	// dimension (model, provider, prompt_template_id, prompt_version, tool_name).
	VerdictBreakdowns []VerdictBreakdown `json:"verdict_breakdowns,omitempty"`
}

// VerdictRates is the canonical fix_attempt_verdict distribution.
// SampleSize is the total number of verdict rows used to compute the rates.
type VerdictRates struct {
	WinRate      float64 `json:"win_rate"`
	FailRate     float64 `json:"fail_rate"`
	DrawRate     float64 `json:"draw_rate"`
	NoChangeRate float64 `json:"no_change_rate"`
	SampleSize   uint64  `json:"sample_size"`
}

// VerdictBreakdownEntry is one row in a faceted verdict breakdown.
type VerdictBreakdownEntry struct {
	GroupKey string       `json:"group_key"`
	Rates    VerdictRates `json:"rates"`
}

// VerdictBreakdown groups VerdictBreakdownEntries by the dimension they were
// sliced on.
type VerdictBreakdown struct {
	Dimension string                  `json:"dimension"`
	Entries   []VerdictBreakdownEntry `json:"entries"`
}

// OutcomeScoreSummary provides stats for a single score name
type OutcomeScoreSummary struct {
	ScoreName string  `json:"score_name"`
	DataType  string  `json:"data_type"`
	Count     uint64  `json:"count"`
	Mean      float64 `json:"mean"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	P50       float64 `json:"p50"`
	P95       float64 `json:"p95"`
	PassRate  float64 `json:"pass_rate"`
}
