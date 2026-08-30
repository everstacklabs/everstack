// Package cache provides caching implementations for the gateway.
package cache

import "context"

// TracingHooks allows tracing to be injected into cache operations
// without the cache package importing telemetry directly.
// This avoids import cycles while enabling proper span hierarchy.
type TracingHooks interface {
	// StartEmbeddingSpan starts a span for embedding generation within semantic cache.
	// Returns the updated context and a handle to end the span.
	StartEmbeddingSpan(ctx context.Context, model string, inputLength int) (context.Context, SpanHandle)

	// StartSearchSpan starts a span for vector similarity search.
	// Returns the updated context and a handle to end the span.
	StartSearchSpan(ctx context.Context, backend string) (context.Context, SpanHandle)

	// AddEvent adds an event to the current span in context.
	AddEvent(ctx context.Context, name string, attrs map[string]interface{})
}

// SpanHandle allows ending spans and setting attributes without exposing trace.Span.
// This abstraction keeps the cache package decoupled from OpenTelemetry.
type SpanHandle interface {
	// End finishes the span. Must be called when the operation completes.
	End()

	// SetAttributes sets attributes on the span.
	// Supported value types: string, int, int64, float64, bool
	SetAttributes(attrs map[string]interface{})

	// SetError records an error on the span and sets error status.
	SetError(err error)

	// SetWarning records a non-critical error/warning on the span without setting error status.
	// Use this for cache misses or fallback scenarios where the error is expected.
	SetWarning(err error)

	// SetSuccess sets the span status to success.
	SetSuccess()
}

// NoopTracingHooks is a no-op implementation of TracingHooks for when tracing is disabled.
type NoopTracingHooks struct{}

// StartEmbeddingSpan returns a no-op span handle.
func (n *NoopTracingHooks) StartEmbeddingSpan(ctx context.Context, model string, inputLength int) (context.Context, SpanHandle) {
	return ctx, &noopSpanHandle{}
}

// StartSearchSpan returns a no-op span handle.
func (n *NoopTracingHooks) StartSearchSpan(ctx context.Context, backend string) (context.Context, SpanHandle) {
	return ctx, &noopSpanHandle{}
}

// AddEvent is a no-op.
func (n *NoopTracingHooks) AddEvent(ctx context.Context, name string, attrs map[string]interface{}) {}

// noopSpanHandle is a no-op implementation of SpanHandle.
type noopSpanHandle struct{}

func (n *noopSpanHandle) End()                                       {}
func (n *noopSpanHandle) SetAttributes(attrs map[string]interface{}) {}
func (n *noopSpanHandle) SetError(err error)                         {}
func (n *noopSpanHandle) SetWarning(err error)                       {}
func (n *noopSpanHandle) SetSuccess()                                {}
