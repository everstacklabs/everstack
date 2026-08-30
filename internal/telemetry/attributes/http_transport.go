package attributes

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"go.opentelemetry.io/otel/trace"
)

// InstrumentedTransport wraps an http.RoundTripper to automatically capture
// HTTP request/response metadata and add it to the active span
type InstrumentedTransport struct {
	base         http.RoundTripper
	providerName string // Provider name for rate limit parsing
}

// NewInstrumentedTransport creates a new HTTP transport wrapper that automatically
// instruments requests with span attributes
func NewInstrumentedTransport(base http.RoundTripper, providerName string) *InstrumentedTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &InstrumentedTransport{
		base:         base,
		providerName: providerName,
	}
}

// RoundTrip implements http.RoundTripper and automatically adds HTTP metadata to the span
func (t *InstrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	span := trace.SpanFromContext(req.Context())
	if !span.IsRecording() {
		// No active span, just pass through
		return t.base.RoundTrip(req)
	}

	// Capture request metadata
	requestBodySize := 0
	if req.Body != nil && req.ContentLength > 0 {
		requestBodySize = int(req.ContentLength)
	}

	// Sanitize URL (remove API keys from query params or headers)
	sanitizedURL := sanitizeURL(req.URL)
	SetHTTPRequest(span, req.Method, sanitizedURL, requestBodySize)

	// Record request start time
	start := time.Now()

	// Perform the actual HTTP request
	resp, err := t.base.RoundTrip(req)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		// Error occurred, just record latency
		span.SetAttributes()
		return nil, err
	}

	// Capture response metadata
	responseBodySize := 0
	if resp.ContentLength > 0 {
		responseBodySize = int(resp.ContentLength)
	}

	SetHTTPResponse(span, resp.StatusCode, responseBodySize, latencyMs)

	// Parse and set rate limit information from response headers
	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, t.providerName, "")
	SetRateLimitInfo(span, &rateLimitInfo)

	return resp, nil
}

// sanitizeURL removes sensitive information from URLs (API keys in query params)
func sanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}

	// Clone the URL to avoid modifying the original
	sanitized := &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   u.Path,
	}

	// Check if there are query parameters
	if len(u.Query()) > 0 {
		query := u.Query()
		// Remove common API key parameters
		sensitiveParams := []string{"api_key", "apikey", "key", "token", "authorization"}
		for _, param := range sensitiveParams {
			if query.Has(param) {
				query.Set(param, "[REDACTED]")
			}
		}
		sanitized.RawQuery = query.Encode()
	}

	return sanitized.String()
}

// CaptureRequestBody reads and captures the request body for logging/tracing
// Returns a new io.ReadCloser that can be used as req.Body
// NOTE: This is expensive and should only be used when detailed tracing is enabled
func CaptureRequestBody(body io.ReadCloser) ([]byte, io.ReadCloser, error) {
	if body == nil {
		return nil, nil, nil
	}

	// Read the body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, err
	}

	// Close the original body
	body.Close()

	// Return the bytes and a new reader
	return bodyBytes, io.NopCloser(bytes.NewReader(bodyBytes)), nil
}

// CaptureResponseBody reads and captures the response body for logging/tracing
// Returns a new io.ReadCloser that can be used as resp.Body
// NOTE: This is expensive and should only be used when detailed tracing is enabled
func CaptureResponseBody(body io.ReadCloser) ([]byte, io.ReadCloser, error) {
	if body == nil {
		return nil, nil, nil
	}

	// Read the body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, err
	}

	// Close the original body
	body.Close()

	// Return the bytes and a new reader
	return bodyBytes, io.NopCloser(bytes.NewReader(bodyBytes)), nil
}

// SanitizeAuthHeader removes or redacts authorization headers for logging
func SanitizeAuthHeader(header string) string {
	if header == "" {
		return ""
	}

	// Common auth header formats
	if strings.HasPrefix(header, "Bearer ") {
		return "Bearer [REDACTED]"
	}
	if strings.HasPrefix(header, "Basic ") {
		return "Basic [REDACTED]"
	}
	if strings.HasPrefix(header, "Token ") {
		return "Token [REDACTED]"
	}

	return "[REDACTED]"
}

