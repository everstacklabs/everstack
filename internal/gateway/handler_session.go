package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// SessionLookup resolves a session ID prefix to a backend target for proxying.
type SessionLookup interface {
	// LookupSessionBackend returns the backend target (host:port) for a session.
	LookupSessionBackend(ctx context.Context, sessionIDPrefix string) (backendTarget string, err error)
}

// SessionHandler handles session-based subdomain routing.
// Pattern: {session_id_prefix}.session.{baseDomain} -> sandbox backend
type SessionHandler struct {
	lookup    SessionLookup
	transport *TransportPool
}

// NewSessionHandler creates a session routing handler.
func NewSessionHandler(lookup SessionLookup, transport *TransportPool) *SessionHandler {
	return &SessionHandler{
		lookup:    lookup,
		transport: transport,
	}
}

// ServeSession resolves a session ID prefix and proxies the request.
func (h *SessionHandler) ServeSession(w http.ResponseWriter, r *http.Request, sessionIDPrefix string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	backendTarget, err := h.lookup.LookupSessionBackend(ctx, sessionIDPrefix)
	if err != nil {
		logger.WithFields("session_prefix", sessionIDPrefix, "error", err.Error()).
			Debug("gateway: session backend not found")
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	target, err := url.Parse("http://" + backendTarget)
	if err != nil {
		http.Error(w, `{"error":"invalid backend target"}`, http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.Transport = h.transport.Transport()

	proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, proxyErr error) {
		logger.WithFields("session_prefix", sessionIDPrefix, "target", backendTarget, "error", proxyErr.Error()).
			Warn("gateway: session proxy error")
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(rw).Encode(map[string]string{
			"error":  "proxy error",
			"detail": proxyErr.Error(),
		})
	}

	proxy.ServeHTTP(w, r)
}

// NoopSessionLookup is a placeholder that always returns an error.
// Used when session routing is not configured.
type NoopSessionLookup struct{}

func (n *NoopSessionLookup) LookupSessionBackend(_ context.Context, sessionIDPrefix string) (string, error) {
	return "", fmt.Errorf("session routing not configured for session %s", sessionIDPrefix)
}
