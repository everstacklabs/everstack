// Package v1 provides gRPC handlers for the gateway service.
package v1

import (
	"context"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// CacheTracingHooks implements cache.TracingHooks using OpenTelemetry.
// This bridges the cache layer to the telemetry package without creating import cycles.
type CacheTracingHooks struct{}

// NewCacheTracingHooks creates a new CacheTracingHooks instance.
// This is called from cmd/serve to inject tracing into the semantic cache.
func NewCacheTracingHooks() *CacheTracingHooks {
	return &CacheTracingHooks{}
}

// StartEmbeddingSpan starts a span for embedding generation within semantic cache.
func (h *CacheTracingHooks) StartEmbeddingSpan(ctx context.Context, model string, inputLength int) (context.Context, cache.SpanHandle) {
	ctx, span := telemetry.StartSemanticCacheEmbeddingSpan(ctx, model)

	// Add initial attributes
	span.SetAttributes(
		attribute.Int(attrs.SemanticInputTextLength, inputLength),
	)

	// Add embedding generation start event
	telemetry.AddSpanEvent(span, attrs.EventEmbeddingGenerationStart,
		attribute.String("model", model),
		attribute.Int("input_length", inputLength))

	return ctx, &spanHandleImpl{span: span, eventName: attrs.EventEmbeddingGenerationComplete}
}

// StartSearchSpan starts a span for vector similarity search.
func (h *CacheTracingHooks) StartSearchSpan(ctx context.Context, backend string) (context.Context, cache.SpanHandle) {
	ctx, span := telemetry.StartSemanticCacheSearchSpan(ctx, backend)

	// Add vector search start event
	telemetry.AddSpanEvent(span, attrs.EventVectorSearchStart,
		attribute.String("backend", backend))

	return ctx, &spanHandleImpl{span: span, eventName: attrs.EventVectorSearchComplete}
}

// AddEvent adds an event to the current span in context.
func (h *CacheTracingHooks) AddEvent(ctx context.Context, name string, attrsMap map[string]interface{}) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}

	otelAttrs := convertToOTELAttributes(attrsMap)
	telemetry.AddSpanEvent(span, name, otelAttrs...)
}

// spanHandleImpl wraps trace.Span to implement cache.SpanHandle.
type spanHandleImpl struct {
	span      trace.Span
	eventName string // Event to add on End()
}

// End finishes the span.
func (s *spanHandleImpl) End() {
	if s.eventName != "" {
		s.span.AddEvent(s.eventName)
	}
	s.span.End()
}

// SetAttributes sets attributes on the span.
func (s *spanHandleImpl) SetAttributes(attrsMap map[string]interface{}) {
	otelAttrs := convertToOTELAttributes(attrsMap)
	s.span.SetAttributes(otelAttrs...)
}

// SetError records an error on the span.
func (s *spanHandleImpl) SetError(err error) {
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
	}
}

// SetWarning records a non-critical error/warning on the span without setting error status.
// This is used for cache misses or fallback scenarios where the error is expected.
func (s *spanHandleImpl) SetWarning(err error) {
	if err != nil {
		// Record the error as an event but don't set status to Error
		s.span.RecordError(err)
		// Set status to Ok with a warning message (cache failures are non-critical)
		s.span.SetStatus(codes.Ok, "warning: "+err.Error())
		// Add an attribute to indicate this was a warning
		s.span.SetAttributes(attribute.Bool("warning", true))
	}
}

// SetSuccess sets the span status to success.
func (s *spanHandleImpl) SetSuccess() {
	s.span.SetStatus(codes.Ok, "success")
}

// convertToOTELAttributes converts a map to OpenTelemetry attributes.
func convertToOTELAttributes(attrsMap map[string]interface{}) []attribute.KeyValue {
	if attrsMap == nil {
		return nil
	}

	result := make([]attribute.KeyValue, 0, len(attrsMap))
	for k, v := range attrsMap {
		switch val := v.(type) {
		case string:
			result = append(result, attribute.String(k, val))
		case int:
			result = append(result, attribute.Int(k, val))
		case int64:
			result = append(result, attribute.Int64(k, val))
		case float64:
			result = append(result, attribute.Float64(k, val))
		case bool:
			result = append(result, attribute.Bool(k, val))
		}
	}
	return result
}

// Ensure CacheTracingHooks implements cache.TracingHooks
var _ cache.TracingHooks = (*CacheTracingHooks)(nil)
