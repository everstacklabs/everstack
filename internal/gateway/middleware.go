package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/cors"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// responseWriter captures status code for logging and implements http.Hijacker
// for WebSocket compatibility.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Hijack implements http.Hijacker for WebSocket support.
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("gateway: underlying ResponseWriter does not support hijacking")
}

// Flush implements http.Flusher for SSE/streaming support.
func (w *responseWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// LoggingMiddleware logs each request with method, path, status, duration, and request ID.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.WithFields(
			"method", r.Method,
			"path", r.URL.Path,
			"host", r.Host,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", w.Header().Get("X-Request-ID"),
		).Debug("gateway: request")
	})
}

// HealthCheckMiddleware short-circuits /healthz before the main handler.
func HealthCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"service": "gateway",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware applies CORS headers using rs/cors.
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		opts := cors.Options{
			AllowedOrigins: cfg.AllowedOrigins,
			AllowedMethods: cfg.AllowedMethods,
			AllowedHeaders: cfg.AllowedHeaders,
			MaxAge:         cfg.MaxAgeSecs,
		}
		if len(opts.AllowedOrigins) > 0 && opts.AllowedOrigins[0] != "*" {
			opts.AllowCredentials = true
		}
		return cors.New(opts).Handler(next)
	}
}

// SecurityHeadersMiddleware adds standard security headers and generates X-Request-ID.
func SecurityHeadersMiddleware(tlsEnabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}
			w.Header().Set("X-Request-ID", requestID)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			if tlsEnabled {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// TimeoutMiddleware applies http.TimeoutHandler, skipping WebSocket upgrades.
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		timedHandler := http.TimeoutHandler(next, timeout, `{"error":"request timeout"}`)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsWebSocketUpgrade(r) {
				next.ServeHTTP(w, r)
				return
			}
			timedHandler.ServeHTTP(w, r)
		})
	}
}

// BodyLimitMiddleware applies MaxBytesReader to the request body, skipping WebSocket upgrades.
func BodyLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsWebSocketUpgrade(r) && r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IsWebSocketUpgrade detects WebSocket upgrade requests.
func IsWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}
