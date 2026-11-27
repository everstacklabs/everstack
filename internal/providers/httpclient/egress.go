package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/outboundguard"
)

// publicEndpointsOnly is the process-wide egress switch for provider traffic.
// A tenant can set a provider's base URL, and on a managed gateway the request
// leaves from inside the platform's network, so without this switch a tenant
// could point a provider at cluster services, peer pods, Tailscale peers or
// the cloud metadata address and read the replies back as completions.
//
// It is checked at dial time rather than when a transport is built, because
// the shared transports are created once and the check must not depend on
// which happens first.
var publicEndpointsOnly atomic.Bool

// guardDialConfig lets tests point the guarded dialer at a fake resolver.
var guardDialConfig outboundguard.Config

// RequirePublicEndpoints restricts every provider HTTP connection to public
// unicast addresses for the rest of the life of this process. Intended for
// gateways that serve more than one tenant; self-hosted gateways legitimately
// reach private addresses such as a local Ollama. The switch is one-way.
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
	closeIdle := func() {
		Default()
		defaultTransport.CloseIdleConnections()
		Unary()
		unaryTransport.CloseIdleConnections()
	}
	closeIdle()
	return func() {
		publicEndpointsOnly.Store(prev)
		closeIdle()
	}
}

// egressDial dials through the outbound guard when public endpoints are
// required, and through the plain dialer otherwise. Redirects are covered too,
// since each hop dials through the same transport.
func egressDial(d *net.Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if PublicEndpointsRequired() {
			return outboundguard.DialContext(guardDialConfig)(ctx, network, address)
		}
		return d.DialContext(ctx, network, address)
	}
}

// egressProxy ignores any environment proxy when public endpoints are
// required: the proxy would make the connection, so the guard would only see
// the proxy's address and never the tenant's target.
func egressProxy(req *http.Request) (*url.URL, error) {
	if PublicEndpointsRequired() {
		return nil, nil
	}
	return http.ProxyFromEnvironment(req)
}

// NewEgressClient returns a client with its own transport under the same
// egress rules, for calls to tenant-supplied URLs outside the provider path.
// timeout 0 means no whole-request timeout.
func NewEgressClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 egressProxy,
			DialContext:           egressDial(&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}),
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}
