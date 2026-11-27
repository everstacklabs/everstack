package mcp

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/outboundguard"
)

// publicEndpointsOnly is the process-wide egress switch for MCP HTTP traffic.
// A tenant chooses an MCP server's URL, and on a managed gateway the request
// to it leaves from inside the platform's network, so without this switch a
// tenant could point a server (or its OAuth metadata, token endpoint, or the
// SSE endpoint event) at cluster services, peer pods, Tailscale peers or the
// cloud metadata address and read the replies back through CallTool. It is
// package state for the same reason as the stdio switch: it is a property of
// the process, and every HTTP client in this package consults it.
var publicEndpointsOnly atomic.Bool

// RequirePublicEndpoints restricts every MCP HTTP connection (transports,
// OAuth discovery and token requests) to public unicast addresses for the
// rest of the life of this process. Intended for gateways that serve more
// than one tenant. The switch is one-way.
func RequirePublicEndpoints() {
	publicEndpointsOnly.Store(true)
}

// PublicEndpointsRequired reports whether RequirePublicEndpoints has run.
func PublicEndpointsRequired() bool {
	return publicEndpointsOnly.Load()
}

// SetPublicEndpointsForTest overrides the switch and returns a restore func.
// Tests only; production code must use RequirePublicEndpoints.
func SetPublicEndpointsForTest(required bool) (restore func()) {
	prev := publicEndpointsOnly.Swap(required)
	return func() { publicEndpointsOnly.Store(prev) }
}

// guardDialConfig lets tests point the guarded dialer at a fake resolver.
var guardDialConfig outboundguard.Config

// newHTTPClient returns the client every MCP HTTP call uses. timeout 0 means
// no whole-request timeout (SSE streams). When public endpoints are required
// the client dials through the outbound guard, never through an environment
// proxy, and redirects are re-checked by the same dialer.
func newHTTPClient(timeout time.Duration) *http.Client {
	client := &http.Client{Timeout: timeout}
	if PublicEndpointsRequired() {
		client.Transport = &http.Transport{
			Proxy:                 nil,
			DialContext:           outboundguard.DialContext(guardDialConfig),
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	return client
}
