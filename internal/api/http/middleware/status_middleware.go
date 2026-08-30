package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// StatusTrackingResponseWriter is a wrapper for http.ResponseWriter that tracks the status code
type StatusTrackingResponseWriter struct {
	http.ResponseWriter
	StatusCode int
}

// WriteHeader captures the status code before calling the underlying WriteHeader
func (w *StatusTrackingResponseWriter) WriteHeader(code int) {
	w.StatusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Write calls the underlying Write after ensuring headers are written
func (w *StatusTrackingResponseWriter) Write(b []byte) (int, error) {
	if w.StatusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// StatusCodeMiddleware adds status code tracking to the response writer
// and sets the span status based on the HTTP status code
func StatusCodeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a response writer that will track the status code
		trackingWriter := &StatusTrackingResponseWriter{
			ResponseWriter: w,
			StatusCode:     0,
		}

		// Call the next handler with the tracking writer
		next.ServeHTTP(trackingWriter, r)

		// Get the current span from context
		span := trace.SpanFromContext(r.Context())

		// Set the span status based on the HTTP status code
		if trackingWriter.StatusCode >= 400 {
			span.SetStatus(codes.Error, http.StatusText(trackingWriter.StatusCode))
		} else {
			span.SetStatus(codes.Ok, "")
		}

		// Add the status code as an attribute to the span
		// This is already done by the otelhttp handler, but we add it here for consistency
		// span.SetAttributes(attribute.Int("http.status_code", trackingWriter.StatusCode))
	})
}
