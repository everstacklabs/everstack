package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"

	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
)

// RateLimitHeadersMiddleware forwards rate limit headers from providers to clients
func RateLimitHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wrap the response writer to capture provider response headers
		wrapper := &rateLimitResponseWriter{
			ResponseWriter: w,
			headers:        make(http.Header),
		}

		next.ServeHTTP(wrapper, r)
	})
}

// rateLimitResponseWriter captures headers for forwarding
type rateLimitResponseWriter struct {
	http.ResponseWriter
	headers     http.Header
	written     bool
	headersSent bool
}

func (w *rateLimitResponseWriter) Header() http.Header {
	// Return the underlying writer's header
	return w.ResponseWriter.Header()
}

func (w *rateLimitResponseWriter) Write(data []byte) (int, error) {
	if !w.headersSent {
		// Before writing the first bytes, add rate limit headers
		w.forwardRateLimitHeaders()
		w.headersSent = true
	}
	w.written = true
	return w.ResponseWriter.Write(data)
}

func (w *rateLimitResponseWriter) WriteHeader(code int) {
	if !w.headersSent {
		// Before writing the header, add rate limit headers
		w.forwardRateLimitHeaders()
		w.headersSent = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher if the underlying ResponseWriter supports it
func (w *rateLimitResponseWriter) Flush() {
	if !w.headersSent {
		w.forwardRateLimitHeaders()
		w.headersSent = true
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker if the underlying ResponseWriter supports it
func (w *rateLimitResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacking not supported")
}

func (w *rateLimitResponseWriter) forwardRateLimitHeaders() {
	// Find the most recently updated provider status (by timestamp)
	// to avoid returning stale headers from the wrong provider.
	providers := ratelimit.GlobalMonitor.GetKnownProviders()

	var mostRecent *ratelimit.RateLimitInfo
	for _, provider := range providers {
		if info := ratelimit.GlobalMonitor.GetStatus(provider); info != nil {
			if mostRecent == nil || info.Timestamp.After(mostRecent.Timestamp) {
				mostRecent = info
			}
		}
	}

	if mostRecent == nil {
		return
	}

	if mostRecent.RequestLimit > 0 {
		w.ResponseWriter.Header().Set("X-RateLimit-Limit-Requests", fmt.Sprintf("%d", mostRecent.RequestLimit))
		w.ResponseWriter.Header().Set("X-RateLimit-Remaining-Requests", fmt.Sprintf("%d", mostRecent.RequestRemaining))
		w.ResponseWriter.Header().Set("X-RateLimit-Reset-Requests", fmt.Sprintf("%d", mostRecent.RequestReset))
	}
	if mostRecent.TokenLimit > 0 {
		w.ResponseWriter.Header().Set("X-RateLimit-Limit-Tokens", fmt.Sprintf("%d", mostRecent.TokenLimit))
		w.ResponseWriter.Header().Set("X-RateLimit-Remaining-Tokens", fmt.Sprintf("%d", mostRecent.TokenRemaining))
		w.ResponseWriter.Header().Set("X-RateLimit-Reset-Tokens", fmt.Sprintf("%d", mostRecent.TokenReset))
	}
	if mostRecent.IsRateLimited && mostRecent.RetryAfter > 0 {
		w.ResponseWriter.Header().Set("Retry-After", fmt.Sprintf("%d", mostRecent.RetryAfter))
	}
}
