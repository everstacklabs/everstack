package query

import (
	"time"
)

// Read models represent optimized data structures for queries

// ChatSessionReadModel represents a chat session for analytics.
type ChatSessionReadModel struct {
	ID            string                 `json:"id" db:"id"`
	UserID        string                 `json:"user_id" db:"user_id"`
	APIKey        string                 `json:"api_key" db:"api_key"`
	Model         string                 `json:"model" db:"model"`
	Provider      string                 `json:"provider" db:"provider"`
	MessageCount  int                    `json:"message_count" db:"message_count"`
	TokensUsed    int                    `json:"tokens_used" db:"tokens_used"`
	StartedAt     time.Time              `json:"started_at" db:"started_at"`
	CompletedAt   *time.Time             `json:"completed_at,omitempty" db:"completed_at"`
	Duration      *time.Duration         `json:"duration,omitempty" db:"duration"`
	Success       bool                   `json:"success" db:"success"`
	ErrorCode     *string                `json:"error_code,omitempty" db:"error_code"`
	ErrorMessage  *string                `json:"error_message,omitempty" db:"error_message"`
	Metadata      map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
	CorrelationID string                 `json:"correlation_id" db:"correlation_id"`
}

// ModelUsageStatsReadModel represents aggregated model usage statistics.
type ModelUsageStatsReadModel struct {
	Provider        string    `json:"provider" db:"provider"`
	Model           string    `json:"model" db:"model"`
	Period          time.Time `json:"period" db:"period"` // Start of the time period
	RequestCount    int64     `json:"request_count" db:"request_count"`
	SuccessCount    int64     `json:"success_count" db:"success_count"`
	ErrorCount      int64     `json:"error_count" db:"error_count"`
	AvgLatencyMs    float64   `json:"avg_latency_ms" db:"avg_latency_ms"`
	MinLatencyMs    int64     `json:"min_latency_ms" db:"min_latency_ms"`
	MaxLatencyMs    int64     `json:"max_latency_ms" db:"max_latency_ms"`
	TotalTokensUsed int64     `json:"total_tokens_used" db:"total_tokens_used"`
	TotalCost       float64   `json:"total_cost" db:"total_cost"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// LoadBalancerStatsReadModel represents load balancer performance metrics.
type LoadBalancerStatsReadModel struct {
	Period          time.Time `json:"period" db:"period"`
	Strategy        string    `json:"strategy" db:"strategy"`
	KeySource       string    `json:"key_source" db:"key_source"`
	RequestCount    int64     `json:"request_count" db:"request_count"`
	FallbackCount   int64     `json:"fallback_count" db:"fallback_count"`
	FallbackRate    float64   `json:"fallback_rate" db:"fallback_rate"`
	AvgLatencyMs    float64   `json:"avg_latency_ms" db:"avg_latency_ms"`
	PrimarySuccess  int64     `json:"primary_success" db:"primary_success"`
	FallbackSuccess int64     `json:"fallback_success" db:"fallback_success"`
	TotalFailures   int64     `json:"total_failures" db:"total_failures"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// ModelConfigReadModel represents current model configurations.
type ModelConfigReadModel struct {
	Provider  string                 `json:"provider" db:"provider"`
	ModelID   string                 `json:"model_id" db:"model_id"`
	Alias     string                 `json:"alias" db:"alias"`
	Config    map[string]interface{} `json:"config" db:"config"`
	Enabled   bool                   `json:"enabled" db:"enabled"`
	IsDefault bool                   `json:"is_default" db:"is_default"`
	CreatedAt time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt time.Time              `json:"updated_at" db:"updated_at"`
	Version   int                    `json:"version" db:"version"`
}

// ErrorRateReadModel represents error rate analytics.
type ErrorRateReadModel struct {
	Provider   string    `json:"provider" db:"provider"`
	Model      string    `json:"model" db:"model"`
	ErrorType  string    `json:"error_type" db:"error_type"`
	ErrorCode  string    `json:"error_code" db:"error_code"`
	Period     time.Time `json:"period" db:"period"`
	ErrorCount int64     `json:"error_count" db:"error_count"`
	TotalCount int64     `json:"total_count" db:"total_count"`
	ErrorRate  float64   `json:"error_rate" db:"error_rate"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

// APIKeyUsageReadModel represents API key usage patterns.
type APIKeyUsageReadModel struct {
	APIKeyHash     string    `json:"api_key_hash" db:"api_key_hash"` // Hashed for privacy
	UserID         string    `json:"user_id" db:"user_id"`
	Period         time.Time `json:"period" db:"period"`
	RequestCount   int64     `json:"request_count" db:"request_count"`
	TokensUsed     int64     `json:"tokens_used" db:"tokens_used"`
	UniqueSessions int64     `json:"unique_sessions" db:"unique_sessions"`
	TopModelsUsed  []string  `json:"top_models_used" db:"top_models_used"`
	AvgLatencyMs   float64   `json:"avg_latency_ms" db:"avg_latency_ms"`
	ErrorRate      float64   `json:"error_rate" db:"error_rate"`
	LastActivityAt time.Time `json:"last_activity_at" db:"last_activity_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// ModelUsageSnapshot represents current model usage.
type ModelUsageSnapshot struct {
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	ActiveRequests int     `json:"active_requests"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	SuccessRate    float64 `json:"success_rate"`
}

// LoadBalancerSnapshot represents current LB state.
type LoadBalancerSnapshot struct {
	Strategy       string             `json:"strategy"`
	KeySource      string             `json:"key_source"`
	ActiveTargets  int                `json:"active_targets"`
	FallbackActive bool               `json:"fallback_active"`
	Weights        map[string]float64 `json:"weights"`
}

// ProviderHealthStatus represents provider availability.
type ProviderHealthStatus struct {
	Provider     string    `json:"provider"`
	Status       string    `json:"status"` // "healthy", "degraded", "unhealthy"
	LastChecked  time.Time `json:"last_checked"`
	ResponseTime float64   `json:"response_time_ms"`
	ErrorRate    float64   `json:"error_rate"`
}

// RealTimeMetricsReadModel represents current system state.
type RealTimeMetricsReadModel struct {
	Timestamp           time.Time              `json:"timestamp" db:"timestamp"`
	ActiveConnections   int                    `json:"active_connections" db:"active_connections"`
	RequestsPerSecond   float64                `json:"requests_per_second" db:"requests_per_second"`
	AvgLatencyMs        float64                `json:"avg_latency_ms" db:"avg_latency_ms"`
	ErrorRate           float64                `json:"error_rate" db:"error_rate"`
	TopModels           []ModelUsageSnapshot   `json:"top_models"`
	LoadBalancerStatus  LoadBalancerSnapshot   `json:"load_balancer_status"`
	ProviderHealthCheck []ProviderHealthStatus `json:"provider_health_check"`
}

// ChunkMetadataReadModel represents detailed timing for a single streaming chunk
type ChunkMetadataReadModel struct {
	Index            int   `json:"index"`             // Chunk index (0-based)
	TimestampMs      int64 `json:"timestamp_ms"`      // Unix timestamp when chunk was received
	LatencyMs        int64 `json:"latency_ms"`        // Time since previous chunk (inter-chunk latency)
	TokenCount       int   `json:"token_count"`       // Estimated tokens in this chunk
	CumulativeTokens int   `json:"cumulative_tokens"` // Running total of tokens received
}

// StreamingMetricsReadModel contains performance metrics for streaming responses
type StreamingMetricsReadModel struct {
	TtftMs                 int64                    `json:"ttft_ms"`                   // Time to first token (ms)
	ChunkCount             int                      `json:"chunk_count"`               // Total chunks received
	AvgChunkLatencyMs      float64                  `json:"avg_chunk_latency_ms"`      // Average inter-chunk latency
	MaxChunkLatencyMs      int64                    `json:"max_chunk_latency_ms"`      // Slowest chunk latency
	TokensPerSecond        float64                  `json:"tokens_per_second"`         // Throughput: tokens / duration
	StreamDurationMs       int64                    `json:"stream_duration_ms"`        // Total streaming time
	PartialResponseOnError string                   `json:"partial_response_on_error"` // Captured text before error
	ChunkTimeline          []ChunkMetadataReadModel `json:"chunk_timeline,omitempty"`  // Optional detailed timeline
}

// FunctionExecutionReadModel represents a single function execution within a request
type FunctionExecutionReadModel struct {
	FunctionID    string `json:"function_id" db:"function_id"`
	FunctionName  string `json:"function_name" db:"function_name"`
	Runtime       string `json:"runtime" db:"runtime"`
	Backend       string `json:"backend" db:"backend"`
	ExecutionMode string `json:"execution_mode" db:"execution_mode"` // "warm" or "cold"
	DurationMs    int64  `json:"duration_ms" db:"duration_ms"`
	Success       bool   `json:"success" db:"success"`
	Error         string `json:"error,omitempty" db:"error"`
	ErrorType     string `json:"error_type,omitempty" db:"error_type"`
	Stdout        string `json:"stdout,omitempty" db:"stdout"`
	Stderr        string `json:"stderr,omitempty" db:"stderr"`
}

// RequestLogReadModel represents a single gateway request log (grouped by correlation_id)
// TraceLogReadModel is one raw OTLP log record correlated to a trace, returned
// by the GetTraceLogs query. Unlike RequestLogReadModel (gateway request logs,
// grouped by correlation_id) this is a generic per-record view that works for
// any OTLP log source, including coding agents like Claude Code.
type TraceLogReadModel struct {
	Timestamp      time.Time         `json:"timestamp"`
	SeverityText   string            `json:"severity_text"`
	SeverityNumber int32             `json:"severity_number"`
	Body           string            `json:"body"`
	SpanID         string            `json:"span_id"`
	ScopeName      string            `json:"scope_name"`
	ServiceName    string            `json:"service_name"`
	Attributes     map[string]string `json:"attributes"`
}

type RequestLogReadModel struct {
	CorrelationID    string  `json:"correlation_id" db:"correlation_id"`
	Timestamp        string  `json:"timestamp" db:"timestamp"`
	FirstTimestamp   string  `json:"first_timestamp" db:"first_timestamp"`
	CommandType      string  `json:"command_type" db:"command_type"`
	Provider         string  `json:"provider" db:"provider"`
	Model            string  `json:"model" db:"model"` // Kept for backward compatibility - same as ServedModel
	LatencyMs        int64   `json:"latency_ms" db:"latency_ms"`
	PromptTokens     int64   `json:"prompt_tokens" db:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens" db:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens" db:"total_tokens"`
	Cost             float64 `json:"cost" db:"cost"`
	Status           string  `json:"status" db:"status"` // "success", "error", "pending"
	Severity         string  `json:"severity" db:"severity"`
	LogEvent         string  `json:"log_event" db:"log_event"`
	Stream           bool    `json:"stream" db:"stream"`      // Whether this was a streaming request
	Payload          string  `json:"payload" db:"payload"`    // JSON string
	RequestText      string  `json:"request_text,omitempty"`  // Extracted request text
	ResponseText     string  `json:"response_text,omitempty"` // Extracted response text
	TraceID          string  `json:"trace_id" db:"trace_id"`
	SpanID           string  `json:"span_id" db:"span_id"`
	TenantID         string  `json:"tenant_id" db:"tenant_id"`
	TenantType       string  `json:"tenant_type" db:"tenant_type"`

	// Multi-model fallback tracking
	RequestedModel     string   `json:"requested_model" db:"requested_model"`           // What user asked for
	ServedModel        string   `json:"served_model" db:"served_model"`                 // What actually responded/errored
	AllAttemptedModels []string `json:"all_attempted_models" db:"all_attempted_models"` // All models tried (chronological)
	FallbackOccurred   bool     `json:"fallback_occurred" db:"fallback_occurred"`       // Did fallback happen?
	AttemptCount       int      `json:"attempt_count" db:"attempt_count"`               // Number of attempts

	// Streaming performance metrics (only populated when Stream = true)
	StreamingMetrics *StreamingMetricsReadModel `json:"streaming_metrics,omitempty"`

	// Function executions (for requests that triggered serverless functions)
	FunctionExecutions []FunctionExecutionReadModel `json:"function_executions,omitempty"`

	// CustomAttrValues holds resolved attribute-sourced custom log column
	// values, keyed by column key. Only populated when the list query was
	// given custom log columns.
	CustomAttrValues map[string]string `json:"custom_attr_values,omitempty"`
}

// TraceReadModel represents a trace summary for analytics
type TraceReadModel struct {
	TraceID        string    `json:"trace_id" db:"TraceId"`
	StartTime      time.Time `json:"start_time" db:"StartTime"`
	EndTime        time.Time `json:"end_time" db:"EndTime"`
	TotalDuration  int64     `json:"total_duration" db:"TotalDuration"` // int64 - SQL casts to Int64 for compatibility
	ErrorCount     uint64    `json:"error_count" db:"ErrorCount"`
	RootStatus     string    `json:"root_status" db:"RootStatus"` // Status from root span (Ok, Error, Unset)
	RequestedModel string    `json:"requested_model" db:"RequestedModel"`
	ServedModel    string    `json:"served_model" db:"ServedModel"`
	LLMModel       string    `json:"llm_model" db:"LLMModel"`
	Provider       string    `json:"provider" db:"Provider"`
	TenantID       string    `json:"tenant_id" db:"TenantID"`
	SpanCount      uint64    `json:"span_count" db:"SpanCount"`
	// Rich trace fields
	TraceInput       string  `json:"trace_input,omitempty" db:"TraceInput"`
	TraceOutput      string  `json:"trace_output,omitempty" db:"TraceOutput"`
	UserID           string  `json:"user_id,omitempty" db:"UserId"`
	SessionID        string  `json:"session_id,omitempty" db:"SessionId"`
	ThreadID         string  `json:"thread_id,omitempty" db:"ThreadId"`
	TotalCost        float64 `json:"total_cost" db:"TotalCost"`
	TotalSavings     float64 `json:"total_savings" db:"TotalSavings"`
	TotalCarbonSaved float64 `json:"total_carbon_saved" db:"TotalCarbonSaved"`
	ModelParameters  string  `json:"model_parameters,omitempty" db:"ModelParameters"`
	// Aggregated token usage + metadata for the traces list columns.
	InputTokens     int64    `json:"input_tokens" db:"InputTokens"`
	OutputTokens    int64    `json:"output_tokens" db:"OutputTokens"`
	TotalTokens     int64    `json:"total_tokens" db:"TotalTokens"`
	CachedTokens    int64    `json:"cached_tokens" db:"CachedTokens"`
	ReasoningTokens int64    `json:"reasoning_tokens" db:"ReasoningTokens"`
	Metadata        string   `json:"metadata,omitempty" db:"Metadata"`
	TraceKinds      []string `json:"trace_kinds,omitempty" db:"TraceKinds"` // agent/workflow/sandbox/... derived from spans
	// ServiceName / ScopeName identify the emitter (e.g. service "claude-code",
	// scope "com.anthropic.claude_code.tracing"); used to derive the trace's
	// client/agent. TraceNameAttr is the root span's trace.name attribute and
	// RootSpanName is the root span name — the trace-name fallbacks.
	ServiceName   string `json:"service_name,omitempty"`
	ScopeName     string `json:"scope_name,omitempty"`
	TraceNameAttr string `json:"trace_name_attr,omitempty"`
	RootSpanName  string `json:"root_span_name,omitempty"`
	// CustomAttrValues holds resolved attribute-sourced custom column values,
	// keyed by column key. Only populated when the list query was given
	// CustomAttrColumns. Metadata-sourced columns are resolved separately.
	CustomAttrValues map[string]string `json:"custom_attr_values,omitempty"`
}

// SpanEvent represents an event that occurred during a span's lifetime
type SpanEvent struct {
	Timestamp  time.Time         `json:"timestamp"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// SpanReadModel represents a single span in a trace
type SpanReadModel struct {
	TraceID            string            `json:"trace_id" db:"TraceId"`
	SpanID             string            `json:"span_id" db:"SpanId"`
	ParentSpanID       string            `json:"parent_span_id" db:"ParentSpanId"`
	SpanName           string            `json:"span_name" db:"SpanName"`
	SpanKind           string            `json:"span_kind" db:"SpanKind"`
	Timestamp          time.Time         `json:"timestamp" db:"Timestamp"`
	Duration           int64             `json:"duration" db:"Duration"` // int64 to match ClickHouse Int64 Duration column type
	StatusCode         string            `json:"status_code" db:"StatusCode"`
	StatusMessage      string            `json:"status_message" db:"StatusMessage"`
	SpanAttributes     map[string]string `json:"span_attributes" db:"SpanAttributes"`
	ResourceAttributes map[string]string `json:"resource_attributes" db:"ResourceAttributes"`
	Events             []SpanEvent       `json:"events,omitempty"`
}

// SpanTreeNode represents hierarchical span structure for tree visualization
type SpanTreeNode struct {
	Span     SpanReadModel   `json:"span"`
	Children []*SpanTreeNode `json:"children"`
}

// TraceStatsReadModel represents aggregated trace statistics
type TraceStatsReadModel struct {
	Period           time.Time `json:"period" db:"period"`
	TenantID         string    `json:"tenant_id" db:"tenant_id"`
	TraceCount       int64     `json:"trace_count" db:"trace_count"`
	AvgDuration      float64   `json:"avg_duration" db:"avg_duration"`
	ErrorCount       int64     `json:"error_count" db:"error_count"`
	ErrorRate        float64   `json:"error_rate" db:"error_rate"`
	TotalSpans       int64     `json:"total_spans" db:"total_spans"`
	AvgSpansPerTrace float64   `json:"avg_spans_per_trace" db:"avg_spans_per_trace"`
}
