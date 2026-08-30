package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
)

const routeCookieName = "_sbx_route"

// PortMappingLookup is the interface the gateway uses to resolve routes to backend targets.
type PortMappingLookup interface {
	// LookupPortMapping resolves a subdomain to a port mapping.
	LookupPortMapping(ctx context.Context, subdomain string) (*sandbox.PortMapping, error)

	// LookupPortMappingByID resolves a sandbox ID + port to a port mapping.
	LookupPortMappingByID(ctx context.Context, sandboxID string, port int) (*sandbox.PortMapping, error)
}

// ProxyHandler handles reverse proxy requests for sandbox ports.
type ProxyHandler struct {
	lookup    PortMappingLookup
	transport *TransportPool
	// requirePreviewToken makes signed preview tokens mandatory for subdomain
	// access. Path/direct local routing remains available through ServeByID.
	requirePreviewToken bool
	// signer, when non-nil, validates signed preview URL tokens. If nil,
	// token validation is skipped (all requests pass through as before).
	signer *previewtoken.Signer
}

// NewProxyHandler creates a new reverse proxy handler.
func NewProxyHandler(lookup PortMappingLookup, transport *TransportPool) *ProxyHandler {
	return &ProxyHandler{
		lookup:    lookup,
		transport: transport,
	}
}

// SetPreviewSigner configures the HMAC signer used to validate signed preview
// URL tokens. Passing nil keeps unsigned direct preview access enabled.
func (h *ProxyHandler) SetPreviewSigner(s *previewtoken.Signer) {
	h.signer = s
}

// SetRequirePreviewToken rejects unsigned subdomain preview access when true.
func (h *ProxyHandler) SetRequirePreviewToken(require bool) {
	h.requirePreviewToken = require
}

// ServeSubdomain handles subdomain-based routing.
//
// If a preview signer is configured, signed preview tokens are validated against
// the resolved port mapping: subdomain, sandbox ID, tenant ID, and port must all
// match. Requests with neither token nor preview cookie continue to serve the
// standard direct preview URL path unless require_preview_token is enabled.
func (h *ProxyHandler) ServeSubdomain(w http.ResponseWriter, r *http.Request, subdomain string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	mapping, err := h.lookup.LookupPortMapping(ctx, subdomain)
	if err != nil {
		logger.WithFields("subdomain", subdomain, "error", err.Error()).
			Debug("gateway: port mapping not found")
		http.Error(w, `{"error":"port mapping not found"}`, http.StatusNotFound)
		return
	}
	if !h.validatePreviewAccess(w, r, subdomain, mapping) {
		return
	}

	h.proxyToBackend(w, r, mapping)
}

func (h *ProxyHandler) validatePreviewAccess(w http.ResponseWriter, r *http.Request, subdomain string, mapping *sandbox.PortMapping) bool {
	if h.signer == nil {
		if h.requirePreviewToken {
			logPreviewProxyAudit("preview_access_denied", subdomain, mapping, "").
				Warn("preview audit: signed preview enforcement unavailable")
			http.Error(w, `{"error":"signed preview enforcement unavailable"}`, http.StatusServiceUnavailable)
			return false
		}
		return true
	}

	tokenStr := r.URL.Query().Get(previewtoken.QueryParam)
	tokenSource := "query"
	if tokenStr == "" {
		cookie, err := r.Cookie(previewtoken.CookiePrefix + subdomain)
		if err == nil && cookie.Value != "" {
			tokenStr = cookie.Value
			tokenSource = "cookie"
		}
	}
	if tokenStr == "" {
		if h.requirePreviewToken {
			logPreviewProxyAudit("preview_access_denied", subdomain, mapping, "").
				Warn("preview audit: token required")
			http.Error(w, `{"error":"preview token required"}`, http.StatusUnauthorized)
			return false
		}
		logPreviewProxyAudit("preview_access_unsigned", subdomain, mapping, "").Debug("preview audit: unsigned access")
		return true
	}

	claims, err := h.signer.Verify(tokenStr)
	if err != nil {
		entry := logPreviewProxyAudit("preview_access_denied", subdomain, mapping, tokenSource)
		if errors.Is(err, previewtoken.ErrTokenExpired) {
			entry.Warn("preview audit: token expired")
			http.Error(w, `{"error":"preview token expired"}`, http.StatusUnauthorized)
			return false
		}
		entry.Warn("preview audit: token invalid")
		http.Error(w, `{"error":"preview token invalid"}`, http.StatusUnauthorized)
		return false
	}
	if err := validatePreviewClaimsAgainstMapping(claims, subdomain, mapping); err != nil {
		logPreviewProxyAudit("preview_access_denied", subdomain, mapping, tokenSource).
			WithField("error", err.Error()).Warn("preview audit: token scope mismatch")
		http.Error(w, `{"error":"preview token scope mismatch"}`, http.StatusForbidden)
		return false
	}
	if tokenSource == "query" {
		http.SetCookie(w, &http.Cookie{
			Name:     previewtoken.CookiePrefix + subdomain,
			Value:    tokenStr,
			Path:     "/",
			MaxAge:   int(previewtoken.CookieMaxAge.Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			Secure:   r.TLS != nil,
		})
		stripPreviewTokenQuery(r)
	}
	logPreviewProxyAudit("preview_access_allowed", subdomain, mapping, tokenSource).Info("preview audit: access allowed")
	return true
}

func validatePreviewClaimsAgainstMapping(claims previewtoken.Claims, subdomain string, mapping *sandbox.PortMapping) error {
	if mapping == nil {
		return fmt.Errorf("mapping is required")
	}
	if claims.Subdomain != subdomain {
		return fmt.Errorf("subdomain claim %q does not match %q", claims.Subdomain, subdomain)
	}
	if claims.SandboxID != mapping.SandboxID {
		return fmt.Errorf("sandbox claim %q does not match %q", claims.SandboxID, mapping.SandboxID)
	}
	if claims.Port != mapping.Port {
		return fmt.Errorf("port claim %d does not match %d", claims.Port, mapping.Port)
	}
	if claims.TenantID == "" || mapping.TenantID == "" || claims.TenantID != mapping.TenantID {
		return fmt.Errorf("tenant claim does not match mapping tenant")
	}
	return nil
}

func stripPreviewTokenQuery(r *http.Request) {
	q := r.URL.Query()
	if _, ok := q[previewtoken.QueryParam]; !ok {
		return
	}
	q.Del(previewtoken.QueryParam)
	r.URL.RawQuery = q.Encode()
}

func logPreviewProxyAudit(event, subdomain string, mapping *sandbox.PortMapping, tokenSource string) *logger.Entry {
	fields := []interface{}{
		"subdomain", subdomain,
		"token_source", tokenSource,
	}
	if mapping != nil {
		fields = append(fields,
			"sandbox_id", mapping.SandboxID,
			"tenant_id", mapping.TenantID,
			"port", mapping.Port,
		)
	}
	return logger.WithFields(fields...).WithLogEvent(event)
}

// ServeByID handles header-based and path-based routing using sandbox ID + port.
func (h *ProxyHandler) ServeByID(w http.ResponseWriter, r *http.Request, sandboxID, portStr, rewritePath string) {
	port, err := strconv.Atoi(portStr)
	if err != nil {
		http.Error(w, `{"error":"invalid port"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	mapping, err := h.lookup.LookupPortMappingByID(ctx, sandboxID, port)
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "port", port, "error", err.Error()).
			Debug("gateway: port mapping not found")
		http.Error(w, `{"error":"port mapping not found"}`, http.StatusNotFound)
		return
	}
	if rewritePath != "" && h.signer != nil && !isLocalPreviewHost(r.Host) && !h.routeCookieMatchesMapping(r, mapping) {
		http.Error(w, `{"error":"path preview route requires signed route cookie"}`, http.StatusForbidden)
		return
	}

	// Rewrite path for path-based routing — strip the /_sandbox/{id}/port/{port} prefix.
	if rewritePath != "" {
		r.URL.Path = rewritePath

		// Set a cookie so subsequent root-relative requests (e.g. /@vite/client,
		// /src/main.js) from the same browser can be routed to this sandbox.
		h.setRouteCookie(w, r, mapping)
	}

	h.proxyToBackend(w, r, mapping)
}

// proxyToBackend creates a reverse proxy and forwards the request.
func (h *ProxyHandler) proxyToBackend(w http.ResponseWriter, r *http.Request, mapping *sandbox.PortMapping) {
	target, err := url.Parse("http://" + mapping.BackendTarget)
	if err != nil {
		http.Error(w, `{"error":"invalid backend target"}`, http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // SSE/streaming support
	proxy.Transport = h.transport.Transport()

	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
		logger.WithFields("target", mapping.BackendTarget, "sandbox_id", mapping.SandboxID, "error", proxyErr.Error()).
			Warn("gateway: proxy error")
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

// ServeCookieFallback handles unmatched requests using the _sbx_route cookie
// set by path-based routing. This reliably catches all root-relative URLs
// (/@vite/client, /src/main.js, /node_modules/...) regardless of nesting
// depth, because the cookie persists across all requests from the same origin.
func (h *ProxyHandler) ServeCookieFallback(w http.ResponseWriter, r *http.Request) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	mapping, ok := h.lookupRouteCookieMapping(ctx, r)
	if !ok {
		return false
	}

	h.proxyToBackend(w, r, mapping)
	return true
}

// ServeRefererFallback handles requests that don't match any route but have a
// Referer pointing to a sandbox path. This catches root-relative asset URLs
// like /@vite/client or /src/main.jsx that the browser requests because Vite
// uses absolute paths in its HTML output.
func (h *ProxyHandler) ServeRefererFallback(w http.ResponseWriter, r *http.Request) bool {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return false
	}

	refURL, err := url.Parse(ref)
	if err != nil || !strings.HasPrefix(refURL.Path, "/_sandbox/") {
		return false
	}

	// Parse the sandbox route from the referer path
	result, ok := parsePathRoute(refURL.Path)
	if !ok {
		return false
	}

	// Proxy to the same sandbox with the original request path
	port, err := strconv.Atoi(result.Port)
	if err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	mapping, err := h.lookup.LookupPortMappingByID(ctx, result.SandboxID, port)
	if err != nil {
		return false
	}
	if h.signer != nil && !h.routeCookieMatchesMapping(r, mapping) {
		return false
	}

	h.proxyToBackend(w, r, mapping)
	return true
}

func (h *ProxyHandler) setRouteCookie(w http.ResponseWriter, r *http.Request, mapping *sandbox.PortMapping) {
	value := mapping.SandboxID + ":" + strconv.Itoa(mapping.Port)
	if h.signer != nil {
		token, err := h.signer.Sign(previewtoken.Claims{
			SandboxID: mapping.SandboxID,
			Subdomain: mapping.Subdomain,
			TenantID:  mapping.TenantID,
			Port:      mapping.Port,
		}, previewtoken.CookieMaxAge)
		if err != nil {
			logger.WithFields("sandbox_id", mapping.SandboxID, "port", mapping.Port, "error", err.Error()).
				Warn("gateway: failed to sign sandbox route cookie")
			return
		}
		value = token
	}
	http.SetCookie(w, &http.Cookie{
		Name:     routeCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(previewtoken.CookieMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

func (h *ProxyHandler) lookupRouteCookieMapping(ctx context.Context, r *http.Request) (*sandbox.PortMapping, bool) {
	cookie, err := r.Cookie(routeCookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}

	if h.signer != nil {
		claims, err := h.signer.Verify(cookie.Value)
		if err != nil {
			return nil, false
		}
		mapping, err := h.lookup.LookupPortMappingByID(ctx, claims.SandboxID, claims.Port)
		if err != nil {
			return nil, false
		}
		if err := validatePreviewClaimsAgainstMapping(claims, claims.Subdomain, mapping); err != nil {
			return nil, false
		}
		return mapping, true
	}

	parts := strings.SplitN(cookie.Value, ":", 2)
	if len(parts) != 2 {
		return nil, false
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, false
	}
	mapping, err := h.lookup.LookupPortMappingByID(ctx, parts[0], port)
	if err != nil {
		return nil, false
	}
	return mapping, true
}

func (h *ProxyHandler) routeCookieMatchesMapping(r *http.Request, mapping *sandbox.PortMapping) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cookieMapping, ok := h.lookupRouteCookieMapping(ctx, r)
	return ok && cookieMapping.SandboxID == mapping.SandboxID && cookieMapping.Port == mapping.Port
}

func isLocalPreviewHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" || strings.HasSuffix(host, ".localhost")
}

// ServeNotFound returns a 400 for unresolvable requests.
func ServeNotFound(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, `{"error":"no matching route"}`, http.StatusBadRequest)
}

// LookupPortMappingByIDAdapter adapts a SandboxManager that only has LookupPortMapping
// to also support LookupPortMappingByID via the sandbox_ports table.
type LookupPortMappingByIDAdapter struct {
	manager interface {
		LookupPortMapping(ctx context.Context, subdomain string) (*sandbox.PortMapping, error)
		LookupPortMappingByPort(ctx context.Context, sandboxID string, port int) (*sandbox.PortMapping, error)
	}
}

// LookupPortMapping delegates to the underlying manager.
func (a *LookupPortMappingByIDAdapter) LookupPortMapping(ctx context.Context, subdomain string) (*sandbox.PortMapping, error) {
	return a.manager.LookupPortMapping(ctx, subdomain)
}

// LookupPortMappingByID delegates to the underlying manager's port-based lookup.
func (a *LookupPortMappingByIDAdapter) LookupPortMappingByID(ctx context.Context, sandboxID string, port int) (*sandbox.PortMapping, error) {
	return a.manager.LookupPortMappingByPort(ctx, sandboxID, port)
}

// SimpleLookup wraps a SandboxManager to implement PortMappingLookup.
// For LookupPortMappingByID, it constructs the subdomain and delegates.
type SimpleLookup struct {
	lookupBySubdomain func(ctx context.Context, subdomain string) (*sandbox.PortMapping, error)
	lookupByPort      func(ctx context.Context, sandboxID string, port int) (*sandbox.PortMapping, error)
}

// NewSimpleLookup creates a SimpleLookup from function references.
func NewSimpleLookup(
	bySubdomain func(ctx context.Context, subdomain string) (*sandbox.PortMapping, error),
	byPort func(ctx context.Context, sandboxID string, port int) (*sandbox.PortMapping, error),
) *SimpleLookup {
	return &SimpleLookup{
		lookupBySubdomain: bySubdomain,
		lookupByPort:      byPort,
	}
}

func (s *SimpleLookup) LookupPortMapping(ctx context.Context, subdomain string) (*sandbox.PortMapping, error) {
	return s.lookupBySubdomain(ctx, subdomain)
}

func (s *SimpleLookup) LookupPortMappingByID(ctx context.Context, sandboxID string, port int) (*sandbox.PortMapping, error) {
	if s.lookupByPort != nil {
		return s.lookupByPort(ctx, sandboxID, port)
	}
	return nil, fmt.Errorf("lookup by sandbox ID not supported")
}
