// Deprecated: This file is superseded by internal/gateway/.
// The unified gateway package consolidates subdomain routing, webhook handling,
// path-based routing, header-based routing, and session routing into a single listener.
// This file is kept for reference during migration.
package sandbox

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/cors"

	"github.com/everstacklabs/everstack/internal/lib/logger"

	"golang.org/x/crypto/acme/autocert"
)

// PortExposureConfig configures the sandbox port exposure reverse proxy.
type PortExposureConfig struct {
	Enabled            bool   `json:"enabled" mapstructure:"enabled"`
	BaseDomain         string `json:"base_domain" mapstructure:"base_domain"`
	ListenAddr         string `json:"listen_addr" mapstructure:"listen_addr"`
	MaxPortsPerSandbox int    `json:"max_ports_per_sandbox" mapstructure:"max_ports_per_sandbox"`

	TLS  PortExposureTLSConfig  `json:"tls" mapstructure:"tls"`
	CORS PortExposureCORSConfig `json:"cors" mapstructure:"cors"`

	RequestTimeoutSecs int `json:"request_timeout_seconds" mapstructure:"request_timeout_seconds"`
	MaxRequestBodyMB   int `json:"max_request_body_mb" mapstructure:"max_request_body_mb"`
}

// PortExposureTLSConfig configures TLS for the sandbox proxy.
type PortExposureTLSConfig struct {
	Enabled     bool   `json:"enabled" mapstructure:"enabled"`
	CertPath    string `json:"cert_path" mapstructure:"cert_path"`
	KeyPath     string `json:"key_path" mapstructure:"key_path"`
	Autocert    bool   `json:"autocert" mapstructure:"autocert"`
	AutocertDir string `json:"autocert_dir" mapstructure:"autocert_dir"`
}

// Config builds a *tls.Config for the proxy.
// In autocert mode it configures an ACME manager; in manual mode it loads from paths.
func (t *PortExposureTLSConfig) Config(baseDomain string) (*tls.Config, *autocert.Manager, error) {
	if !t.Enabled {
		return nil, nil, nil
	}

	if t.Autocert {
		dir := t.AutocertDir
		if dir == "" {
			dir = "~/.everstack/autocert"
		}
		mgr := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(dir),
			HostPolicy: autocert.HostWhitelist(), // accept any host — wildcard matching done in proxy
		}
		// Override host policy to accept *.baseDomain
		mgr.HostPolicy = func(_ context.Context, host string) error {
			if strings.HasSuffix(host, "."+baseDomain) || host == baseDomain {
				return nil
			}
			return fmt.Errorf("acme/autocert: host %q not allowed", host)
		}
		return mgr.TLSConfig(), mgr, nil
	}

	// Manual cert/key mode
	if t.CertPath == "" || t.KeyPath == "" {
		return nil, nil, fmt.Errorf("sandbox_proxy: TLS enabled but cert_path or key_path is empty")
	}
	cert, err := tls.LoadX509KeyPair(t.CertPath, t.KeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox_proxy: failed to load TLS cert/key: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil, nil
}

// PortExposureCORSConfig configures CORS for the sandbox proxy.
type PortExposureCORSConfig struct {
	Enabled        *bool    `json:"enabled" mapstructure:"enabled"`
	AllowedOrigins []string `json:"allowed_origins" mapstructure:"allowed_origins"`
	AllowedMethods []string `json:"allowed_methods" mapstructure:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers" mapstructure:"allowed_headers"`
	MaxAgeSecs     int      `json:"max_age_seconds" mapstructure:"max_age_seconds"`
}

// IsEnabled returns whether CORS is enabled (default: true).
func (c *PortExposureCORSConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// DefaultPortExposureConfig returns sensible defaults for port exposure.
func DefaultPortExposureConfig() PortExposureConfig {
	return PortExposureConfig{
		Enabled:            false,
		BaseDomain:         "everstack.localhost",
		ListenAddr:         ":8443",
		MaxPortsPerSandbox: 5,
		CORS: PortExposureCORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			AllowedHeaders: []string{"*"},
			MaxAgeSecs:     3600,
		},
		RequestTimeoutSecs: 120,
		MaxRequestBodyMB:   50,
	}
}

// SandboxProxy is an HTTP reverse proxy that routes subdomain-based requests
// to exposed sandbox ports. It provides TLS, CORS, security headers, health
// checks, request logging, timeouts, and body size limits.
type SandboxProxy struct {
	manager    *SandboxManager
	config     PortExposureConfig
	server     *http.Server
	tlsEnabled bool
}

// NewSandboxProxy creates a new reverse proxy with full configuration.
func NewSandboxProxy(manager *SandboxManager, config PortExposureConfig) *SandboxProxy {
	// Apply defaults for zero-value fields
	if config.BaseDomain == "" {
		config.BaseDomain = "everstack.localhost"
	}
	if config.ListenAddr == "" {
		config.ListenAddr = ":8443"
	}
	if config.RequestTimeoutSecs == 0 {
		config.RequestTimeoutSecs = 120
	}
	if config.MaxRequestBodyMB == 0 {
		config.MaxRequestBodyMB = 50
	}
	if config.CORS.MaxAgeSecs == 0 {
		config.CORS.MaxAgeSecs = 3600
	}
	if len(config.CORS.AllowedOrigins) == 0 {
		config.CORS.AllowedOrigins = []string{"*"}
	}
	if len(config.CORS.AllowedMethods) == 0 {
		config.CORS.AllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	}

	return &SandboxProxy{
		manager:    manager,
		config:     config,
		tlsEnabled: config.TLS.Enabled,
	}
}

// TLSEnabled returns whether TLS is configured.
func (p *SandboxProxy) TLSEnabled() bool {
	return p.tlsEnabled
}

// Start builds the middleware chain, creates the HTTP server, and begins listening.
// It blocks until the server exits.
func (p *SandboxProxy) Start() error {
	handler := p.buildMiddlewareChain()

	p.server = &http.Server{
		Addr:         p.config.ListenAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // Managed per-request by TimeoutHandler; 0 allows WebSocket/SSE
		IdleTimeout:  120 * time.Second,
	}

	if p.config.TLS.Enabled {
		tlsCfg, acmeMgr, err := p.config.TLS.Config(p.config.BaseDomain)
		if err != nil {
			return fmt.Errorf("sandbox_proxy: TLS config error: %w", err)
		}
		p.server.TLSConfig = tlsCfg

		// Start HTTP-01 challenge listener for autocert
		if acmeMgr != nil {
			go func() {
				challengeSrv := &http.Server{
					Addr:    ":80",
					Handler: acmeMgr.HTTPHandler(nil),
				}
				logger.Info("sandbox_proxy: starting ACME HTTP-01 challenge listener on :80")
				if err := challengeSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.WithError(err).Error("sandbox_proxy: ACME challenge listener failed")
				}
			}()
		}

		logger.WithFields("addr", p.config.ListenAddr, "base_domain", p.config.BaseDomain, "tls", true).
			Info("sandbox_proxy: starting reverse proxy server with TLS")

		// ListenAndServeTLS with empty cert/key paths since TLSConfig is set
		return p.server.ListenAndServeTLS("", "")
	}

	logger.WithFields("addr", p.config.ListenAddr, "base_domain", p.config.BaseDomain, "tls", false).
		Info("sandbox_proxy: starting reverse proxy server")

	return p.server.ListenAndServe()
}

// Shutdown gracefully shuts down the proxy server.
func (p *SandboxProxy) Shutdown(ctx context.Context) error {
	if p.server == nil {
		return nil
	}
	logger.Info("sandbox_proxy: shutting down...")
	return p.server.Shutdown(ctx)
}

// buildMiddlewareChain composes the middleware stack (outermost → innermost):
// request logging → health check → CORS → security headers → timeout → body limit → proxy handler
func (p *SandboxProxy) buildMiddlewareChain() http.Handler {
	// Innermost: the actual reverse proxy handler
	var handler http.Handler = http.HandlerFunc(p.serveProxy)

	// Body size limit (skip for WebSocket upgrades)
	maxBytes := int64(p.config.MaxRequestBodyMB) * 1024 * 1024
	handler = p.bodyLimitMiddleware(handler, maxBytes)

	// Request timeout (skip for WebSocket upgrades)
	timeout := time.Duration(p.config.RequestTimeoutSecs) * time.Second
	handler = p.timeoutMiddleware(handler, timeout)

	// Security headers
	handler = p.securityHeadersMiddleware(handler)

	// CORS
	if p.config.CORS.IsEnabled() {
		handler = p.corsMiddleware(handler)
	}

	// Health check (short-circuits before logging to reduce noise)
	handler = p.healthCheckMiddleware(handler)

	// Request logging (outermost)
	handler = p.loggingMiddleware(handler)

	return handler
}

// serveProxy is the core reverse proxy handler.
func (p *SandboxProxy) serveProxy(w http.ResponseWriter, r *http.Request) {
	host := r.Host

	// Strip port from host if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Extract subdomain by stripping the base domain suffix
	subdomain := ""
	if strings.HasSuffix(host, "."+p.config.BaseDomain) {
		subdomain = strings.TrimSuffix(host, "."+p.config.BaseDomain)
	} else if strings.HasSuffix(host, "."+p.config.BaseDomain+".") {
		subdomain = strings.TrimSuffix(host, "."+p.config.BaseDomain+".")
	}

	if subdomain == "" {
		http.Error(w, `{"error":"invalid subdomain"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	mapping, err := p.manager.LookupPortMapping(ctx, subdomain)
	if err != nil {
		logger.WithFields("subdomain", subdomain, "error", err.Error()).
			Debug("sandbox_proxy: port mapping not found")
		http.Error(w, `{"error":"port mapping not found"}`, http.StatusNotFound)
		return
	}

	// Preview traffic is user activity (Daytona parity): a request to
	// an exposed port resets the sandbox's auto-stop clock. Internally
	// rate-limited, so high-traffic previews don't hammer the DB.
	p.manager.TouchActivityBySandboxID(mapping.SandboxID, "preview_request")

	target, err := url.Parse("http://" + mapping.BackendTarget)
	if err != nil {
		http.Error(w, `{"error":"invalid backend target"}`, http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// FlushInterval = -1 for SSE/streaming support
	proxy.FlushInterval = -1

	proxy.Transport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
		logger.WithFields("subdomain", subdomain, "target", mapping.BackendTarget, "error", proxyErr.Error()).
			Warn("sandbox_proxy: proxy error")
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(rw).Encode(map[string]string{
			"error":   "proxy error",
			"detail":  proxyErr.Error(),
			"target":  mapping.BackendTarget,
			"sandbox": mapping.SandboxID,
		})
	}

	proxy.ServeHTTP(w, r)
}

// ─── Middleware ──────────────────────────────────────────────────────────────

// proxyResponseWriter captures status code for logging and implements http.Hijacker
// for WebSocket compatibility.
type proxyResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *proxyResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *proxyResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Hijack implements http.Hijacker for WebSocket support.
func (w *proxyResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("sandbox_proxy: underlying ResponseWriter does not support hijacking")
}

// Flush implements http.Flusher for SSE/streaming support.
func (w *proxyResponseWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// loggingMiddleware logs each request with method, path, status, duration, and request ID.
func (p *SandboxProxy) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &proxyResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.WithFields(
			"method", r.Method,
			"path", r.URL.Path,
			"host", r.Host,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", w.Header().Get("X-Request-ID"),
		).Debug("sandbox_proxy: request")
	})
}

// healthCheckMiddleware short-circuits /healthz before the main handler.
func (p *SandboxProxy) healthCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"service": "sandbox-proxy",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware applies CORS headers using rs/cors.
func (p *SandboxProxy) corsMiddleware(next http.Handler) http.Handler {
	opts := cors.Options{
		AllowedOrigins: p.config.CORS.AllowedOrigins,
		AllowedMethods: p.config.CORS.AllowedMethods,
		AllowedHeaders: p.config.CORS.AllowedHeaders,
		MaxAge:         p.config.CORS.MaxAgeSecs,
	}
	// Only enable credentials if not using wildcard origins
	if len(opts.AllowedOrigins) > 0 && opts.AllowedOrigins[0] != "*" {
		opts.AllowCredentials = true
	}
	return cors.New(opts).Handler(next)
}

// securityHeadersMiddleware adds standard security headers and generates X-Request-ID.
func (p *SandboxProxy) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate X-Request-ID if not present
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", requestID)

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// HSTS when TLS is enabled
		if p.tlsEnabled {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}

		next.ServeHTTP(w, r)
	})
}

// timeoutMiddleware applies http.TimeoutHandler, skipping WebSocket upgrades.
func (p *SandboxProxy) timeoutMiddleware(next http.Handler, timeout time.Duration) http.Handler {
	timedHandler := http.TimeoutHandler(next, timeout, `{"error":"request timeout"}`)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isWebSocketUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		timedHandler.ServeHTTP(w, r)
	})
}

// bodyLimitMiddleware applies MaxBytesReader to the request body, skipping WebSocket upgrades.
func (p *SandboxProxy) bodyLimitMiddleware(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWebSocketUpgrade(r) && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// isWebSocketUpgrade detects WebSocket upgrade requests.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}
