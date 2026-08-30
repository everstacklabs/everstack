package tools

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"
)

// SSRF guard for untrusted-URL fetch tools (web_fetch).
//
// web_fetch resolves arbitrary agent-supplied URLs and fetches them from the
// gateway process, so without a guard it is a server-side request forgery
// vector: an agent could ask it to read the cloud metadata service
// (169.254.169.254), a peer pod on the cluster network, or a Tailscale peer.
// The guard refuses to *connect* to internal address ranges. It lives in the
// dialer's Control hook, which runs after DNS resolution with the concrete
// IP:port about to be dialed — so it also defeats DNS rebinding (the
// resolved-at-connect IP is what we validate) and redirect-based SSRF (each
// redirect hop re-dials through the same guarded transport).
//
// The SearXNG call in web_search is deliberately NOT routed through this guard:
// SearXNG is an operator-configured internal Service on a private cluster IP,
// so a private-range block would break it, and its URL is not agent-controlled.

// isDeniedDialIP reports whether an outbound connection to ip must be blocked.
// It denies loopback, the link-local range (169.254/16, which includes the
// cloud metadata endpoint, plus IPv6 fe80::/10), RFC1918 / IPv6 unique-local
// (via IsPrivate), carrier-grade NAT / Tailscale (100.64.0.0/10), and
// unspecified/multicast targets.
func isDeniedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Normalize IPv4-mapped IPv6 (::ffff:10.0.0.1) to its 4-byte form so the
	// v4 range checks below apply.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// 10/8, 172.16/12, 192.168/16, and IPv6 unique-local fc00::/7.
	if ip.IsPrivate() {
		return true
	}
	// 100.64.0.0/10 — carrier-grade NAT, also the Tailscale CGNAT range this
	// cluster is reached over. Never a legitimate web_fetch target.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

// ssrfSafeControl is a net.Dialer.Control hook that rejects dials to internal
// addresses. address is "IP:port" (the dialer has already resolved any host).
func ssrfSafeControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf guard: malformed dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssrf guard: unresolved dial target %q", host)
	}
	if isDeniedDialIP(ip) {
		return fmt.Errorf("ssrf guard: refusing to connect to internal address %s", ip)
	}
	return nil
}

var (
	guardedFetchClientOnce sync.Once
	guardedFetchClient     *http.Client
)

// guardedHTTPClient returns a process-wide http.Client whose dialer refuses to
// connect to internal/metadata/private addresses. web_fetch uses it whenever no
// explicit client is injected (production wiring passes nil; tests inject their
// own client pointing at httptest servers on loopback).
func guardedHTTPClient() *http.Client {
	guardedFetchClientOnce.Do(func() {
		dialer := &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   ssrfSafeControl,
		}
		guardedFetchClient = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}
	})
	return guardedFetchClient
}
