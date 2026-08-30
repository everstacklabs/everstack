package middleware

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
)

// ResponseWrapper wraps Connect responses to add correlation IDs and other headers
type ResponseWrapper struct {
	handler http.Handler
}

// NewResponseWrapper creates a new response wrapper
func NewResponseWrapper(handler http.Handler) *ResponseWrapper {
	return &ResponseWrapper{
		handler: handler,
	}
}

// ServeHTTP implements http.Handler interface
func (rw *ResponseWrapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Create a response writer that captures headers
	wrappedWriter := &responseWriter{
		ResponseWriter: w,
		headers:        make(http.Header),
	}

	// Call the original handler
	rw.handler.ServeHTTP(wrappedWriter, r)

	// Add correlation ID to response headers if present in context
	if correlationID := correlation.GetCorrelationID(r.Context()); correlationID != "" {
		w.Header().Set(correlation.CorrelationIDHeader, correlationID)
	}
}

// responseWriter wraps http.ResponseWriter to capture headers
type responseWriter struct {
	http.ResponseWriter
	headers http.Header
	status  int
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.status = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	return rw.ResponseWriter.Write(data)
}

func (rw *responseWriter) Header() http.Header {
	return rw.ResponseWriter.Header()
}

// Flush proxies Flush to the underlying ResponseWriter if it supports http.Flusher
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// AddCorrelationIDToConnectResponse adds correlation ID to Connect response headers
// This is a helper function for Connect service implementations
func AddCorrelationIDToConnectResponse(ctx context.Context, response connect.AnyResponse) {
	if correlationID, ok := ctx.Value("correlation_id").(string); ok && correlationID != "" {
		// For Connect, we need to add headers to the response metadata
		// This is typically done through the response headers
		if headers := response.Header(); headers != nil {
			headers.Set(correlation.CorrelationIDHeader, correlationID)
		}
	}
}
