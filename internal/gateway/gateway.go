// Package gateway provides a unified ingress gateway that consolidates
// subdomain-based sandbox proxy, webhook routing, path-based routing,
// header-based routing, and session routing into a single listener.
package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"

	"golang.org/x/crypto/acme/autocert"
)

// Gateway is the unified ingress server.
type Gateway struct {
	config  Config
	server  *http.Server
	router  *RouteResolver
	pool    *TransportPool
	proxy   *ProxyHandler
	webhook *WebhookHandler
	session *SessionHandler

	// Lifecycle context — cancelled in Shutdown so background workers
	// the gateway owns (cert reloader, future watchers) can exit
	// promptly. Created in Start.
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Gateway with the given configuration and dependencies.
func New(cfg Config, lookup PortMappingLookup, webhookHandler http.Handler, sessionLookup SessionLookup) (*Gateway, error) {
	cfg.ApplyDefaults()

	pool, err := NewTransportPool(cfg.MTLS)
	if err != nil {
		return nil, err
	}

	router := NewRouteResolver(cfg.BaseDomain, cfg.EnableSessionRouting)
	proxy := NewProxyHandler(lookup, pool)
	proxy.SetRequirePreviewToken(cfg.RequirePreviewToken)
	if cfg.PreviewSigner != nil {
		proxy.SetPreviewSigner(cfg.PreviewSigner)
	}

	var webhook *WebhookHandler
	if webhookHandler != nil {
		webhook = NewWebhookHandler(webhookHandler)
	}

	var session *SessionHandler
	if sessionLookup != nil && cfg.EnableSessionRouting {
		session = NewSessionHandler(sessionLookup, pool)
	}

	return &Gateway{
		config:  cfg,
		router:  router,
		pool:    pool,
		proxy:   proxy,
		webhook: webhook,
		session: session,
	}, nil
}

// TLSEnabled returns whether TLS is configured.
func (g *Gateway) TLSEnabled() bool {
	return g.config.TLS.Enabled
}

// Proxy returns the underlying ProxyHandler so callers can configure it
// after construction (e.g. SetPreviewSigner).
func (g *Gateway) Proxy() *ProxyHandler {
	return g.proxy
}

// Config returns the gateway configuration (for callers that need base domain, port, etc.).
func (g *Gateway) Config() Config {
	return g.config
}

// Start builds the middleware chain, creates the HTTP server, and begins listening.
// It blocks until the server exits.
func (g *Gateway) Start() error {
	g.ctx, g.cancel = context.WithCancel(context.Background())
	handler := g.buildMiddlewareChain()

	g.server = &http.Server{
		Addr:         g.config.ListenAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // Managed per-request by TimeoutMiddleware; 0 allows WebSocket/SSE
		IdleTimeout:  120 * time.Second,
	}

	if g.config.TLS.Enabled {
		tlsCfg, acmeMgr, err := g.config.TLS.Config(g.ctx, g.config.BaseDomain)
		if err != nil {
			return err
		}
		g.server.TLSConfig = tlsCfg

		if acmeMgr != nil {
			go g.startACMEChallenge(acmeMgr)
		}

		logger.WithFields("addr", g.config.ListenAddr, "base_domain", g.config.BaseDomain, "tls", true).
			Info("gateway: starting with TLS")

		return g.server.ListenAndServeTLS("", "")
	}

	logger.WithFields("addr", g.config.ListenAddr, "base_domain", g.config.BaseDomain, "tls", false).
		Info("gateway: starting")

	return g.server.ListenAndServe()
}

// Shutdown gracefully shuts down the gateway.
func (g *Gateway) Shutdown(ctx context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	if g.pool != nil {
		g.pool.Close()
	}
	if g.server == nil {
		return nil
	}
	logger.Info("gateway: shutting down...")
	return g.server.Shutdown(ctx)
}

// buildMiddlewareChain composes the middleware stack (outermost -> innermost):
// request logging -> health check -> CORS -> security headers -> timeout -> body limit -> route handler
func (g *Gateway) buildMiddlewareChain() http.Handler {
	// Innermost: the route dispatch handler
	var handler http.Handler = http.HandlerFunc(g.serveRoute)

	// Body size limit
	maxBytes := int64(g.config.MaxRequestBodyMB) * 1024 * 1024
	handler = BodyLimitMiddleware(maxBytes)(handler)

	// Request timeout
	timeout := time.Duration(g.config.RequestTimeoutSecs) * time.Second
	handler = TimeoutMiddleware(timeout)(handler)

	// Security headers
	handler = SecurityHeadersMiddleware(g.config.TLS.Enabled)(handler)

	// CORS
	if g.config.CORS.IsEnabled() {
		handler = CORSMiddleware(g.config.CORS)(handler)
	}

	// Health check
	handler = HealthCheckMiddleware(handler)

	// Request logging (outermost)
	handler = LoggingMiddleware(handler)

	return handler
}

// serveRoute is the core dispatch handler. It resolves the route and delegates
// to the appropriate handler.
func (g *Gateway) serveRoute(w http.ResponseWriter, r *http.Request) {
	result := g.router.Resolve(r)

	switch result.Type {
	case RouteWebhook:
		if g.webhook != nil {
			g.webhook.ServeHTTP(w, r, result.WebhookPath)
			return
		}
		http.Error(w, `{"error":"webhooks not configured"}`, http.StatusNotFound)

	case RouteSession:
		if g.session != nil {
			g.session.ServeSession(w, r, result.SessionID)
			return
		}
		http.Error(w, `{"error":"session routing not configured"}`, http.StatusNotFound)

	case RouteSubdomain:
		g.proxy.ServeSubdomain(w, r, result.Subdomain)

	case RouteHeader:
		g.proxy.ServeByID(w, r, result.SandboxID, result.Port, "")

	case RoutePath:
		g.proxy.ServeByID(w, r, result.SandboxID, result.Port, result.Path)

	default:
		// Try cookie-based fallback first — handles all root-relative URLs from
		// sandbox pages (works for nested module imports unlike Referer).
		if g.proxy.ServeCookieFallback(w, r) {
			return
		}
		// Fall back to Referer-based routing for cases where cookie isn't set yet.
		if g.proxy.ServeRefererFallback(w, r) {
			return
		}
		ServeNotFound(w, r)
	}
}

func (g *Gateway) startACMEChallenge(mgr *autocert.Manager) {
	challengeSrv := &http.Server{
		Addr:    ":80",
		Handler: mgr.HTTPHandler(nil),
	}
	logger.Info("gateway: starting ACME HTTP-01 challenge listener on :80")
	if err := challengeSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.WithError(err).Error("gateway: ACME challenge listener failed")
	}
}
