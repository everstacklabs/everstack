package middleware

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/propagation"
)

// traceContextInterceptor extracts W3C trace context at the Connect server
// boundary. The gateway's manually instrumented spans can then continue a
// caller-owned trace instead of starting an unrelated trace for every RPC.
type traceContextInterceptor struct {
	propagator propagation.TextMapPropagator
}

// NewTraceContextInterceptor returns a Connect interceptor that extracts
// traceparent and baggage headers for unary and streaming handlers.
func NewTraceContextInterceptor() connect.Interceptor {
	return &traceContextInterceptor{propagator: newW3CPropagator()}
}

func (i *traceContextInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return next(i.extract(ctx, req.Header()), req)
	}
}

func (i *traceContextInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *traceContextInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(i.extract(ctx, conn.RequestHeader()), conn)
	}
}

func (i *traceContextInterceptor) extract(ctx context.Context, header http.Header) context.Context {
	return i.propagator.Extract(ctx, propagation.HeaderCarrier(header))
}

// TraceContextHTTPHandler extracts the same W3C context for OpenAI-compatible
// HTTP requests before grpc-gateway forwards them to the in-process service.
func TraceContextHTTPHandler(next http.Handler) http.Handler {
	propagator := newW3CPropagator()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newW3CPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}
