package telemetry

import (
	"context"
	"encoding/json"
	"sync"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TraceContext holds rich contextual information for tracing
type TraceContext struct {
	// Session and user tracking
	SessionID string
	UserID    string
	// ThreadID is the conversational continuation, distinct from SessionID. A
	// session is a product-level grouping; a thread is the multi-turn
	// conversation, which may live within or span sessions.
	ThreadID string

	// Deployment metadata
	Release     string
	Environment string
	Tags        []string

	// Request metadata
	TraceName string
	Metadata  map[string]interface{}

	// Model parameters (for LLM requests)
	ModelParameters map[string]interface{}

	// Step tracking for execution ordering
	currentStep uint32
	mu          sync.Mutex // Protect step counter
}

// traceContextKeyType is a private type for trace context keys
type traceContextKeyType string

const traceContextKey traceContextKeyType = "trace_context"

// WithTraceContext adds trace context to the parent context
func WithTraceContext(parent context.Context, tc *TraceContext) context.Context {
	return context.WithValue(parent, traceContextKey, tc)
}

// WithSession stamps the observability session (and optionally user) onto the
// trace context, so every span created downstream carries trace.session_id /
// trace.user_id. This is the standard entry point every execution root (agent,
// workflow, sandbox, eval, voice, function) calls so session is a consistent,
// cross-module grouping key rather than an agent-only concept.
//
// Empty arguments are ignored, so callers can set just a session, just a user,
// or both. The existing trace context is cloned rather than mutated in place to
// avoid racing a TraceContext pointer shared with parent goroutines.
func WithSession(parent context.Context, sessionID, userID string) context.Context {
	if sessionID == "" && userID == "" {
		return parent
	}
	tc := GetTraceContext(parent).clone()
	if sessionID != "" {
		tc.SessionID = sessionID
	}
	if userID != "" {
		tc.UserID = userID
	}
	return WithTraceContext(parent, tc)
}

// clone returns a shallow copy of the trace context safe to mutate. Maps are
// re-referenced (callers that need deep isolation should copy them), but the
// mutex and step counter are reset so the copy tracks its own step sequence.
func (tc *TraceContext) clone() *TraceContext {
	if tc == nil {
		return &TraceContext{
			Metadata:        make(map[string]interface{}),
			ModelParameters: make(map[string]interface{}),
		}
	}
	cp := &TraceContext{
		SessionID:       tc.SessionID,
		UserID:          tc.UserID,
		ThreadID:        tc.ThreadID,
		Release:         tc.Release,
		Environment:     tc.Environment,
		Tags:            tc.Tags,
		TraceName:       tc.TraceName,
		Metadata:        tc.Metadata,
		ModelParameters: tc.ModelParameters,
	}
	if cp.Metadata == nil {
		cp.Metadata = make(map[string]interface{})
	}
	if cp.ModelParameters == nil {
		cp.ModelParameters = make(map[string]interface{})
	}
	return cp
}

// GetTraceContext retrieves trace context from the context
func GetTraceContext(ctx context.Context) *TraceContext {
	if val := ctx.Value(traceContextKey); val != nil {
		if tc, ok := val.(*TraceContext); ok {
			return tc
		}
	}
	return &TraceContext{
		Metadata:        make(map[string]interface{}),
		ModelParameters: make(map[string]interface{}),
	}
}

// ApplyToSpan applies the trace context attributes to a span
// Now uses centralized attribute constants from the registry
func (tc *TraceContext) ApplyToSpan(span trace.Span) {
	if tc == nil {
		return
	}

	if tc.SessionID != "" {
		span.SetAttributes(attribute.String(attrs.TraceSessionID, tc.SessionID))
	}

	if tc.UserID != "" {
		span.SetAttributes(attribute.String(attrs.TraceUserID, tc.UserID))
	}

	if tc.ThreadID != "" {
		span.SetAttributes(attribute.String(attrs.TraceThreadID, tc.ThreadID))
	}

	if tc.TraceName != "" {
		span.SetAttributes(attribute.String(attrs.TraceName, tc.TraceName))
	}

	if len(tc.Tags) > 0 {
		// Store as JSON array string for ClickHouse compatibility
		if tagsJSON, err := json.Marshal(tc.Tags); err == nil {
			span.SetAttributes(attribute.String(attrs.TraceTags, string(tagsJSON)))
		}
	}

	if len(tc.Metadata) > 0 {
		if metadataJSON, err := json.Marshal(tc.Metadata); err == nil {
			span.SetAttributes(attribute.String(attrs.TraceMetadata, string(metadataJSON)))
		}
	}

	if len(tc.ModelParameters) > 0 {
		if paramsJSON, err := json.Marshal(tc.ModelParameters); err == nil {
			span.SetAttributes(attribute.String(attrs.LLMRequestModelParameters, string(paramsJSON)))
		}
	}
}

// SerializeToJSON serializes an object to JSON string, handling errors gracefully
// Deprecated: Use attrs.SerializeToJSON() from internal/telemetry/attributes instead
func SerializeToJSON(v interface{}) string {
	return attrs.SerializeToJSON(v)
}

// TruncateString truncates a string to maxLen with an indicator if truncated
// Deprecated: Use attrs.TruncateString() from internal/telemetry/attributes instead
func TruncateString(s string, maxLen int) string {
	return attrs.TruncateString(s, maxLen)
}

// SerializeAndTruncate serializes to JSON and truncates if needed (default 10KB)
// Deprecated: Use attrs.SerializeAndTruncate() from internal/telemetry/attributes instead
// The new version supports span-aware truncation limits via SpanType
func SerializeAndTruncate(v interface{}, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 // 10KB default
	}
	// Backward compatibility: use raw truncation instead of SpanType
	jsonStr := attrs.SerializeToJSON(v)
	return attrs.TruncateString(jsonStr, maxBytes)
}

// ExtractModelParameters extracts common model parameters into a map
func ExtractModelParameters(temperature *float64, maxTokens *int, topP *float64, stop []string) map[string]interface{} {
	params := make(map[string]interface{})

	if temperature != nil {
		params["temperature"] = *temperature
	}

	if maxTokens != nil {
		params["max_tokens"] = *maxTokens
	}

	if topP != nil {
		params["top_p"] = *topP
	}

	if len(stop) > 0 {
		params["stop"] = stop
	}

	return params
}

// BuildTraceContextFromRequest creates a TraceContext from common request parameters
func BuildTraceContextFromRequest(
	sessionID, userID, environment, release string,
	tags []string,
	metadata map[string]interface{},
) *TraceContext {
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	return &TraceContext{
		SessionID:       sessionID,
		UserID:          userID,
		Environment:     environment,
		Release:         release,
		Tags:            tags,
		Metadata:        metadata,
		ModelParameters: make(map[string]interface{}),
	}
}

// NextStep atomically increments and returns the next step number
func (tc *TraceContext) NextStep() uint32 {
	if tc == nil {
		return 0
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.currentStep++
	return tc.currentStep
}

// GetCurrentStep returns the current step number without incrementing
func (tc *TraceContext) GetCurrentStep() uint32 {
	if tc == nil {
		return 0
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.currentStep
}
