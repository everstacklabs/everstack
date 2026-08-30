package telemetry

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// noopTracer produces non-recording spans for lifecycle/operational hooks that
// must NOT appear in the trace list (traces-module-replan denylist, section 6).
var noopTracer = noop.NewTracerProvider().Tracer("everstack-noop")

// SessionAttrOption returns a span start option that stamps trace.session_id
// (and trace.user_id when known) onto a root span. A session inherited from the
// trace context always wins, so spans nested under an agent or workflow keep
// that grouping; fallbackSessionID (the surface's logical group, e.g. a sandbox
// instance id) is used only when no session was inherited. This is how
// standalone execution roots (sandbox, harness, eval) guarantee every trace
// carries a session without clobbering a parent's.
func SessionAttrOption(ctx context.Context, fallbackSessionID string) trace.SpanStartOption {
	session, user := resolveSessionUser(ctx, fallbackSessionID)
	kv := make([]attribute.KeyValue, 0, 2)
	if session != "" {
		kv = append(kv, attribute.String(attrs.TraceSessionID, session))
	}
	if user != "" {
		kv = append(kv, attribute.String(attrs.TraceUserID, user))
	}
	return trace.WithAttributes(kv...)
}

// resolveSessionUser returns the session and user to stamp on a root span. An
// inherited session from the trace context always wins; fallbackSessionID is
// used only when none was inherited. User is taken from the trace context as-is.
func resolveSessionUser(ctx context.Context, fallbackSessionID string) (session, user string) {
	tc := GetTraceContext(ctx)
	session = tc.SessionID
	if session == "" {
		session = fallbackSessionID
	}
	return session, tc.UserID
}

// RecordGuardrailCheck emits a guardrail span event (D7) on the active span so
// the trace detail can render a safety summary instead of a wall of untyped
// events. violations are "category: reason" strings; an empty slice records a
// pass. The event name and attribute keys match the frontend parser in
// apps/admin/src/utils/guardrail-events.ts.
func RecordGuardrailCheck(ctx context.Context, name string, violations []string) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	result := "pass"
	kvs := []attribute.KeyValue{}
	if len(violations) > 0 {
		result = "block"
		rules := make([]string, 0, len(violations))
		for _, v := range violations {
			if i := strings.Index(v, ":"); i > 0 {
				rules = append(rules, strings.TrimSpace(v[:i]))
			} else {
				rules = append(rules, v)
			}
		}
		kvs = append(kvs,
			attribute.String(attrs.GuardrailRule, strings.Join(rules, ", ")),
			attribute.String(attrs.GuardrailViolations, strings.Join(violations, "; ")),
		)
	}
	kvs = append([]attribute.KeyValue{attribute.String(attrs.GuardrailResult, result)}, kvs...)
	span.AddEvent(name, trace.WithAttributes(kvs...))
}

// addCorrelationIDToSpan extracts correlation_id from context and adds it to the span
// if present. This enables log-to-trace linking via correlation_id.
func addCorrelationIDToSpan(ctx context.Context, span trace.Span) {
	if correlationID := correlation.GetCorrelationID(ctx); correlationID != "" {
		span.SetAttributes(attribute.String(attrs.CorrelationID, correlationID))
	}
}

// StartGatewaySpan starts a new trace span for gateway operations
// Follows Portkey/Bifrost pattern for LLM gateway tracing
// Automatically adds observation type and applies trace context
func StartGatewaySpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	// Add observation type based on span name
	obsType := DetermineObservationTypeFromSpanName(name)
	allOpts := []trace.SpanStartOption{
		WithObservationType(obsType),
		WithObservationLevel(ObservationLevelDefault),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, name, allOpts...)

	// Apply trace context if available
	traceCtx := GetTraceContext(ctx)
	traceCtx.ApplyToSpan(span)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartProviderSpan starts a child span for provider API calls
// Creates CLIENT span kind to represent outbound HTTP requests
// Provider spans are marked as GENERATION type for Langfuse compatibility
func StartProviderSpan(ctx context.Context, provider, model string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("provider.%s.chat", provider)

	// Prepend span kind, observation type, and base attributes
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeGeneration), // Provider calls are generations
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.Provider, provider),
			attribute.String(attrs.LLMRequestModel, model),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartEmbeddingSpan starts a span for embedding operations
func StartEmbeddingSpan(ctx context.Context, model string, inputCount int, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeGeneration), // Embeddings are also generations
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.LLMOperation, "embeddings"),
			attribute.String(attrs.LLMRequestModel, model),
			attribute.Int("llm.embeddings.input_count", inputCount),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "gateway.embeddings", allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartStreamChunkSpan starts a span for individual streaming chunks
// Only used when TraceStreamChunks is enabled (detailed mode)
// Stream chunks are marked as EVENT type
func StartStreamChunkSpan(ctx context.Context, chunkIndex int) (context.Context, trace.Span) {
	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "stream.chunk",
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeEvent),
		WithObservationLevel(ObservationLevelDebug),
		trace.WithAttributes(
			attribute.Int(attrs.LLMStreamChunkIndex, chunkIndex),
		),
	)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartFallbackSpan starts a child span for fallback attempts
// Replaces span events with proper hierarchical spans
func StartFallbackSpan(ctx context.Context, attempt int, reason string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("fallback.attempt.%d", attempt)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.Int(attrs.FallbackAttempt, attempt),
			attribute.String(attrs.FallbackReason, reason),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartKeyRotationSpan starts a span for key rotation attempts
func StartKeyRotationSpan(ctx context.Context, provider string) (context.Context, trace.Span) {
	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "key.rotation",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String(attrs.Provider, provider),
		),
	)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartModelResolutionSpan starts a span for model routing/resolution
func StartModelResolutionSpan(ctx context.Context, requestedModel string) (context.Context, trace.Span) {
	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "gateway.model.resolution",
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.ModelRequested, requestedModel),
		),
	)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartRequestNormalizationSpan starts a span for request normalization and validation
func StartRequestNormalizationSpan(ctx context.Context) (context.Context, trace.Span) {
	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "gateway.request.normalize",
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
	)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartResponseProcessingSpan starts a span for response processing
func StartResponseProcessingSpan(ctx context.Context) (context.Context, trace.Span) {
	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "gateway.response.process",
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
	)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartCacheLookupSpan starts a span for cache lookup operations
// cacheType should be "exact", "semantic", "onnx", "auth", or "router"
func StartCacheLookupSpan(ctx context.Context, cacheType string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("cache.%s.lookup", cacheType)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.CacheType, cacheType),
			attribute.String(attrs.CacheOperation, "lookup"),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartCacheStoreSpan starts a span for cache store operations
// cacheType should be "exact", "semantic", "onnx", "auth", or "router"
func StartCacheStoreSpan(ctx context.Context, cacheType string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("cache.%s.store", cacheType)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.CacheType, cacheType),
			attribute.String(attrs.CacheOperation, "store"),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartEmbeddingLookupSpan starts a span for embedding generation
// This is used when generating embeddings for semantic cache
func StartEmbeddingLookupSpan(ctx context.Context, model string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.LLMOperation, "embeddings"),
			attribute.String(attrs.SemanticEmbeddingModel, model),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "cache.embedding.generate", allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartVectorSearchSpan starts a span for vector similarity search
// This is used for Redis vector search in semantic cache
func StartVectorSearchSpan(ctx context.Context, indexName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.VectorSearchIndexName, indexName),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "cache.vector.search", allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartAuthCacheSpan starts a span for auth cache operations
// operation should be "lookup", "store", or "warm"
func StartAuthCacheSpan(ctx context.Context, operation string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("cache.auth.%s", operation)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.CacheType, "auth"),
			attribute.String(attrs.CacheOperation, operation),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartRouterCacheSpan starts a span for router cache operations
// operation should be "lookup", "warm", or "resolve"
func StartRouterCacheSpan(ctx context.Context, operation string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("cache.router.%s", operation)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.CacheType, "router"),
			attribute.String(attrs.CacheOperation, operation),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartUnifiedCacheLookupSpan starts a parent span for all cache lookups (exact + semantic)
// This creates a unified view of cache operations in the trace
func StartUnifiedCacheLookupSpan(ctx context.Context, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.CacheOperation, "lookup"),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "gateway.cache.lookup", allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartSemanticCacheEmbeddingSpan starts a child span for embedding generation in semantic cache
// This should be nested under a cache.semantic.lookup span
func StartSemanticCacheEmbeddingSpan(ctx context.Context, model string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.LLMOperation, "embeddings"),
			attribute.String(attrs.SemanticEmbeddingModel, model),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "cache.semantic.embedding", allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartSemanticCacheSearchSpan starts a child span for vector search in semantic cache
// This should be nested under a cache.semantic.lookup span
func StartSemanticCacheSearchSpan(ctx context.Context, backend string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.SemanticSearchBackend, backend),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-gateway")
	ctx, span := tracer.Start(ctx, "cache.semantic.search", allOpts...)

	// Add correlation_id to span for log-to-trace linking
	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartAgentSessionSpan starts a span for an agent session execution.
// This is the root span for a single session lifecycle.
func StartAgentSessionSpan(ctx context.Context, agentID, sessionID, tenantID string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.AgentID, agentID),
			attribute.String(attrs.AgentSessionID, sessionID),
			// Also stamp the canonical cross-module session key so the agent
			// session groups in the Sessions view / Session column like every
			// other module's root. agent.session.id alone is not read by the
			// session machinery.
			attribute.String(attrs.TraceSessionID, sessionID),
			attribute.String(attrs.TenantID, tenantID),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, "agent.session", allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartAgentTurnSpan starts a child span for a single agent turn (user message -> response cycle).
func StartAgentTurnSpan(ctx context.Context, sessionID string, turnNumber int, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("agent.turn.%d", turnNumber)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSpan),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.AgentSessionID, sessionID),
			// Canonical session key so a turn that is itself the trace root
			// (agent invoked without a wrapping session span) still groups.
			attribute.String(attrs.TraceSessionID, sessionID),
			attribute.Int(attrs.AgentTurnNumber, turnNumber),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartAgentToolCallSpan starts a child span for an individual tool call within a turn.
// Uses CLIENT span kind since tool calls are outbound operations.
func StartAgentToolCallSpan(ctx context.Context, toolCallID, toolName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("agent.tool.%s", toolName)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeTool),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.AgentToolCallID, toolCallID),
			attribute.String(attrs.AgentToolCallName, toolName),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// Convenience wrappers for common attributes

// WithModel adds model attribute to span
func WithModel(model string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.LLMRequestModel, model))
}

// WithProvider adds provider attribute to span
func WithProvider(provider string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.Provider, provider))
}

// WithTenantID adds tenant.id attribute to span
func WithTenantID(tenantID string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.TenantID, tenantID))
}

// WithRequestID adds request ID attribute to span
func WithRequestID(requestID string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("request.id", requestID))
}

// WithSpanKind sets the span kind
func WithSpanKind(kind trace.SpanKind) trace.SpanStartOption {
	return trace.WithSpanKind(kind)
}

// WithHTTPAttributes adds HTTP-specific attributes to a span
// Deprecated: Use attrs.SetHTTPRequest() / attrs.SetHTTPResponse() from internal/telemetry/attributes instead
// For new code, prefer using InstrumentedTransport which automatically captures HTTP attributes
func WithHTTPAttributes(method, url string, statusCode int) trace.SpanStartOption {
	return trace.WithAttributes(
		attribute.String(attrs.HTTPMethod, method),
		attribute.String(attrs.HTTPURL, url),
		attribute.Int(attrs.HTTPStatusCode, statusCode),
	)
}

// RecordLLMMetrics records LLM-specific metrics on a span
// Deprecated: Use attrs.SetLLMTokens() from internal/telemetry/attributes instead
// This function is kept for backward compatibility and delegates to the new implementation
func RecordLLMMetrics(span trace.Span, inputTokens, outputTokens int64, cost float64, latencyMs int64) {
	attrs.SetLLMTokens(span, inputTokens, outputTokens, inputTokens+outputTokens)
	span.SetAttributes(
		attribute.Float64("llm.cost", cost),
		attribute.Int64(attrs.LatencyMs, latencyMs),
	)
}

// RecordLLMMetricsWithCost records LLM metrics with detailed cost breakdown
// Deprecated: Use attrs.SetLLMTokens() + attrs.SetLLMCost() from internal/telemetry/attributes instead
// This function is kept for backward compatibility and delegates to the new implementation
func RecordLLMMetricsWithCost(span trace.Span, inputTokens, outputTokens int64, inputCost, outputCost, totalCost float64, latencyMs int64) {
	// Use centralized setters
	attrs.SetLLMTokens(span, inputTokens, outputTokens, inputTokens+outputTokens)
	attrs.SetLLMCost(span, inputCost, outputCost, totalCost)
	span.SetAttributes(
		attribute.Float64("llm.cost", totalCost), // Backward compatibility
		attribute.Int64(attrs.LatencyMs, latencyMs),
	)

	// Add usage and cost details as JSON for Langfuse compatibility
	usageDetailsJSON := attrs.SerializeToJSON(map[string]int64{
		"input":  inputTokens,
		"output": outputTokens,
		"total":  inputTokens + outputTokens,
	})
	if usageDetailsJSON != "" {
		span.SetAttributes(attribute.String("llm.usage_details", usageDetailsJSON))
	}

	costDetailsJSON := attrs.SerializeToJSON(map[string]float64{
		"input":  inputCost,
		"output": outputCost,
		"total":  totalCost,
	})
	if costDetailsJSON != "" {
		span.SetAttributes(attribute.String("llm.cost_details", costDetailsJSON))
	}
}

// RecordStreamingMetrics records streaming-specific metrics on a span
// Deprecated: Use attrs.SetLLMStreamMetrics() from internal/telemetry/attributes instead
// This function is kept for backward compatibility and delegates to the new implementation
func RecordStreamingMetrics(span trace.Span, chunkCount int, firstChunkLatencyMs, totalLatencyMs int64) {
	attrs.SetLLMStreamMetrics(span, chunkCount, firstChunkLatencyMs, totalLatencyMs, 0)
}

// RecordError records an error on a span with proper status and classification
func RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		span.SetStatus(codes.Error, err.Error())

		// Classify error and add structured metadata
		errorType, retryable := attrs.ClassifyError(err)
		attrs.SetErrorDetails(span, errorType, retryable, "")
	}
}

// AddFallbackEvent adds a fallback attempt event to a span
// Deprecated: Use StartFallbackSpan for hierarchical tracing
func AddFallbackEvent(span trace.Span, attempt int, provider string, reason string) {
	span.AddEvent("fallback.attempt", trace.WithAttributes(
		attribute.Int(attrs.FallbackAttempt, attempt),
		attribute.String(attrs.Provider, provider),
		attribute.String(attrs.FallbackReason, reason),
	))
}

// AddSpanEvent adds a timestamped event to a span with optional attributes
// Events are used to mark significant points in time during span execution
// and appear in the trace timeline
func AddSpanEvent(span trace.Span, name string, attributes ...attribute.KeyValue) {
	if len(attributes) > 0 {
		span.AddEvent(name, trace.WithAttributes(attributes...))
	} else {
		span.AddEvent(name)
	}
}

// StartSandboxCreateSpan previously emitted a "sandbox.create" span. Sandbox
// creation is a lifecycle/operational event, and the observability plan's
// denylist (traces-module-replan section 6) requires lifecycle events to stay
// operational and never appear in the trace list. It now returns a non-recording
// span, so existing call sites keep compiling and their End / RecordError /
// AddSpanEvent / SetAttributes calls are no-ops, but nothing is exported. The
// sandbox manager's operational event stream still records creation and failures.
func StartSandboxCreateSpan(ctx context.Context, sessionID, sandboxID, backend string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return noopTracer.Start(ctx, "sandbox.create")
}

// StartWorkflowRunSpan starts the root span for a workflow / Studio execution.
// It is an execution root (traces-module-replan section 4.7): subsequent node
// spans nest under it via the returned context.
func StartWorkflowRunSpan(ctx context.Context, workflowID, runID, tenantID string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeWorkflow),
		WithObservationLevel(ObservationLevelDefault),
		WithRootType(RootTypeWorkflow),
		WithRunID(runID),
		trace.WithAttributes(
			attribute.String(attrs.WorkflowID, workflowID),
			attribute.String(attrs.TenantID, tenantID),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-workflows")
	ctx, span := tracer.Start(ctx, "workflow.run", allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartWorkflowNodeSpan starts a child span for one workflow node execution. The
// observation type is derived from the Studio node type so a node renders the
// same as the equivalent agent step (section 5.1).
func StartWorkflowNodeSpan(ctx context.Context, nodeID, nodeType, nodeLabel string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("workflow.node.%s", nodeType)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(WorkflowNodeTypeToObservationType(nodeType)),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.NodeID, nodeID),
			attribute.String(attrs.NodeType, nodeType),
			attribute.String(attrs.NodeName, nodeLabel),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-workflows")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartVectorStoreSpan starts a span for a vector store operation (M1-T7). op is
// one of query/store/add_documents; backend is the store name (pgvector, qdrant, ...).
func StartVectorStoreSpan(ctx context.Context, op, backend string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeRetriever),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.VectorBackend, backend),
			attribute.String(attrs.VectorOperation, op),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-memory")
	ctx, span := tracer.Start(ctx, "vector."+op, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartMemoryEmbeddingSpan starts a span for an embedding generation in the
// agent memory subsystem (M1-T7). Distinct from StartEmbeddingSpan, which traces
// the gateway embeddings endpoint: this one is typed EMBEDDING in the semantic
// taxonomy and named embedding.embed.
func StartMemoryEmbeddingSpan(ctx context.Context, model string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeEmbedding),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(attribute.String(attrs.EmbeddingModel, model)),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-memory")
	ctx, span := tracer.Start(ctx, "embedding.embed", allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartMemorySpan starts a span for an agent memory operation (M1-T6). op is one
// of retrieve | extract | consolidate. Typed RETRIEVER (the memory category) so
// it groups with vector queries in the trace tree.
func StartMemorySpan(ctx context.Context, op, agentID string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeRetriever),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.AgentID, agentID),
			attribute.String(attrs.MemoryOperation, op),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-memory")
	ctx, span := tracer.Start(ctx, "memory."+op, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartHarnessRunSpan starts the root span for a harness / ADK run (M1-T8): an
// execution root (provision -> write -> install -> run -> teardown).
func StartHarnessRunSpan(ctx context.Context, tenantID string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeHarness),
		WithObservationLevel(ObservationLevelDefault),
		WithRootType(RootTypeHarness),
		trace.WithAttributes(attribute.String(attrs.TenantID, tenantID)),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, "harness.run", allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartMCPToolSpan starts a span for an MCP tool call (M1-T10). Typed TOOL.
func StartMCPToolSpan(ctx context.Context, serverID, toolName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeTool),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.MCPServerID, serverID),
			attribute.String(attrs.MCPToolName, toolName),
		),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, "mcp.tool.call", allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartA2ACallSpan starts a span for an outbound agent-to-agent call (M1-T10).
// Typed AGENT: it invokes another agent.
func StartA2ACallSpan(ctx context.Context, target string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeAgent),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(attribute.String(attrs.A2ATarget, target)),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, "a2a.call", allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartScorerSpan starts a span for a scorer / facet execution (M3-T2). Typed
// SCORER and flagged purpose=scorer so it nests under the scored trace but is
// excluded from the host trace's cost/latency/token rollups.
func StartScorerSpan(ctx context.Context, scorerName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeScorer),
		WithObservationPurpose(PurposeScorer),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(attribute.String(attrs.ScorerName, scorerName)),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-eval")
	ctx, span := tracer.Start(ctx, "scorer."+scorerName, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// ScorerTraceContext returns a context whose span context carries traceIDHex, so
// scorer spans created from it join the original trace (M3-T2). The autoscorer
// runs in a detached goroutine without the original span context, so we
// reconstruct one from the trace id alone (a stable non-zero span id is derived
// from the trace id; sharing the trace id is what groups the spans in the UI).
// If traceIDHex is not a valid trace id, ctx is returned unchanged.
func ScorerTraceContext(ctx context.Context, traceIDHex string) context.Context {
	tid, err := trace.TraceIDFromHex(traceIDHex)
	if err != nil {
		return ctx
	}
	var sid trace.SpanID
	copy(sid[:], tid[:8])
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	if !sc.IsValid() {
		return ctx
	}
	return trace.ContextWithRemoteSpanContext(ctx, sc)
}

// StartBrowserSpan starts a span for a browser automation action (M1-T4): action
// is navigate | click | type | screenshot | observe | ... Typed BROWSER.
func StartBrowserSpan(ctx context.Context, action string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeBrowser),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(attribute.String(attrs.BrowserAction, action)),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, "browser."+action, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartSandboxFSSpan starts a span for a sandbox filesystem operation (M1-T3):
// op is write | read | list | delete. Typed SANDBOX.
func StartSandboxFSSpan(ctx context.Context, op, sandboxID string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSandbox),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(attribute.String(attrs.SandboxID, sandboxID)),
		// Session = the sandbox instance (logical group: all work in one
		// sandbox), unless this op runs under an agent/workflow that already set
		// a session.
		SessionAttrOption(ctx, sandboxID),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, "sandbox.fs."+op, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// StartSandboxExecSpan starts a span for a sandbox command execution.
func StartSandboxExecSpan(ctx context.Context, sandboxID, toolName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanName := fmt.Sprintf("sandbox.exec.%s", toolName)

	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		WithObservationType(ObservationTypeSandbox),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.SandboxID, sandboxID),
			attribute.String(attrs.AgentToolCallName, toolName),
		),
		// Session = the sandbox instance unless an agent/workflow already set one.
		SessionAttrOption(ctx, sandboxID),
	}
	allOpts = append(allOpts, opts...)

	tracer := GetGlobalTracerProvider().Tracer("everstack-agents")
	ctx, span := tracer.Start(ctx, spanName, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}
