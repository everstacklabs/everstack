// Package outboundguard is the single egress guard for server-side calls to
// URLs that a customer or a gateway supplied (gateway callback URLs held by
// billing and license).
//
// Those URLs are attacker-influenced, so a plain http.Client is a server-side
// request forgery primitive: it would let a caller make our services POST to
// the cloud metadata endpoint (169.254.169.254), cluster DNS names, peer pods
// or Tailscale peers. The guard:
//
//   - accepts https only (http only when AllowHTTP is set, for dev and tests);
//   - resolves the host itself and dials the resolved IP literal, refusing the
//     whole request if ANY resolved address is not public unicast, so the
//     address that was checked is the address that is connected to (no DNS
//     rebinding window between check and dial);
//   - re-checks the concrete peer address in the dialer's Control hook;
//   - refuses redirects, applies short timeouts, and caps response bodies.
//
// It is modelled on internal/agents/runtime/tools/ssrf.go but stricter: that
// guard is a deny list on the agent fetch path, this one only allows public
// unicast.
package outboundguard

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is wrapped by every refusal the guard makes.
var ErrBlocked = errors.New("outbound guard: destination not allowed")

// ErrRedirect is returned when a guarded server answers with a redirect.
var ErrRedirect = errors.New("outbound guard: redirects are not followed")

// ErrResponseTooLarge is returned when a response body exceeds the cap.
var ErrResponseTooLarge = errors.New("outbound guard: response body exceeds limit")

const (
	defaultTimeout          = 15 * time.Second
	defaultDialTimeout      = 5 * time.Second
	defaultMaxResponseBytes = 1 << 20 // 1 MiB; gateway callback replies are tiny
)

// Resolver resolves a host name to addresses.
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// DialFunc opens a connection to an already validated "ip:port" address.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Config configures a guarded client. The zero value is production-safe.
type Config struct {
	// AllowHTTP permits plain http URLs. Dev and test only; it never relaxes
	// the address checks.
	AllowHTTP bool
	// Timeout bounds a whole request (default 15s).
	Timeout time.Duration
	// MaxResponseBytes caps each response body (default 1 MiB).
	MaxResponseBytes int64
	// Resolver overrides DNS resolution (tests).
	Resolver Resolver
	// Dial overrides the dialer used after validation (tests). It receives
	// the validated IP literal and port.
	Dial DialFunc
	// TLSClientConfig overrides TLS settings (tests trusting httptest certs).
	TLSClientConfig *tls.Config
}

// blockedPrefixes are non-public ranges that IsGlobalUnicast does not exclude
// on its own (or that embed an IPv4 address an attacker could choose).
var blockedPrefixes = func() []netip.Prefix {
	cidrs := []string{
		"0.0.0.0/8",       // "this network"
		"10.0.0.0/8",      // RFC1918
		"100.64.0.0/10",   // CGNAT, also Tailscale
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local, cloud metadata
		"172.16.0.0/12",   // RFC1918
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"192.88.99.0/24",  // 6to4 relay anycast
		"192.168.0.0/16",  // RFC1918
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, includes broadcast
		"::/128",          // unspecified
		"::1/128",         // loopback
		"::ffff:0:0/96",   // IPv4-mapped
		"64:ff9b::/96",    // NAT64, embeds an IPv4 target
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard-only
		"2001::/32",       // Teredo, embeds an IPv4 target
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4, embeds an IPv4 target
		"fc00::/7",        // unique local
		"fe80::/10",       // link-local
		"fec0::/10",       // deprecated site-local
		"ff00::/8",        // multicast
	}
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}()

// IsPublicUnicast reports whether addr is a public unicast address the guard
// may connect to. IPv4-mapped IPv6 addresses are refused outright.
func IsPublicUnicast(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	if addr.Is4In6() {
		return false
	}
	if !addr.IsGlobalUnicast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// ValidateURL checks a URL's shape before any network activity: scheme,
// host present, no userinfo, and a literal IP host must be public unicast.
// Host names are checked at dial time.
func ValidateURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: unparseable URL", ErrBlocked)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !allowHTTP {
			return fmt.Errorf("%w: https is required", ErrBlocked)
		}
	default:
		return fmt.Errorf("%w: unsupported scheme %q", ErrBlocked, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in URL", ErrBlocked)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrBlocked)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !IsPublicUnicast(addr) {
			return fmt.Errorf("%w: non-public address %s", ErrBlocked, addr)
		}
		return nil
	}
	// Reject numeric forms the URL parser keeps as names but some resolvers
	// interpret as IPv4 (decimal "2130706433", hex "0x7f000001", octal and
	// short dotted forms). Real gateway hosts always contain a letter in a
	// label that is not a hex literal.
	if looksNumericHost(host) {
		return fmt.Errorf("%w: numeric host %q", ErrBlocked, host)
	}
	return nil
}

func looksNumericHost(host string) bool {
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		l := strings.ToLower(label)
		if strings.HasPrefix(l, "0x") {
			continue
		}
		for _, r := range l {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func defaultResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// controlCheck re-validates the concrete peer just before connect(2).
func controlCheck(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: malformed dial address", ErrBlocked)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: unresolved dial target", ErrBlocked)
	}
	if !IsPublicUnicast(addr.Unmap()) {
		return fmt.Errorf("%w: non-public address %s", ErrBlocked, addr)
	}
	return nil
}

// DialContext returns the guard's validating dialer on its own, for callers
// that need the address checks (public unicast only, the checked address is
// the one dialed, re-checked at connect) but not NewTransport's request
// policy: long-lived streams such as SSE cannot live with its whole-request
// timeout, response header timeout or body cap. Only Resolver and Dial in
// cfg apply. Use it as an http.Transport DialContext with Proxy set to nil.
func DialContext(cfg Config) func(ctx context.Context, network, address string) (net.Conn, error) {
	return guardedDialContext(cfg)
}

func guardedDialContext(cfg Config) func(ctx context.Context, network, address string) (net.Conn, error) {
	resolve := cfg.Resolver
	if resolve == nil {
		resolve = defaultResolver
	}
	dial := cfg.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: defaultDialTimeout, KeepAlive: 30 * time.Second, Control: controlCheck}
		dial = d.DialContext
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("%w: malformed address", ErrBlocked)
		}
		var addrs []netip.Addr
		if lit, perr := netip.ParseAddr(host); perr == nil {
			if !IsPublicUnicast(lit) {
				return nil, fmt.Errorf("%w: non-public address %s", ErrBlocked, lit)
			}
			addrs = []netip.Addr{lit}
		} else {
			if looksNumericHost(host) {
				return nil, fmt.Errorf("%w: numeric host", ErrBlocked)
			}
			resolved, rerr := resolve(ctx, host)
			if rerr != nil {
				return nil, fmt.Errorf("outbound guard: resolve %q: %w", host, rerr)
			}
			if len(resolved) == 0 {
				return nil, fmt.Errorf("outbound guard: %q resolved to no addresses", host)
			}
			for _, a := range resolved {
				a = a.Unmap()
				if !IsPublicUnicast(a) {
					return nil, fmt.Errorf("%w: %q resolves to non-public address %s", ErrBlocked, host, a)
				}
				addrs = append(addrs, a)
			}
		}
		var lastErr error
		for _, a := range addrs {
			conn, derr := dial(ctx, network, net.JoinHostPort(a.String(), port))
			if derr == nil {
				return conn, nil
			}
			lastErr = derr
		}
		return nil, lastErr
	}
}

type guardTransport struct {
	base      http.RoundTripper
	allowHTTP bool
	maxBytes  int64
}

func (t *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ValidateURL(req.URL.String(), t.allowHTTP); err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.Body != nil {
		resp.Body = &cappedBody{rc: resp.Body, remaining: t.maxBytes}
	}
	return resp, nil
}

type cappedBody struct {
	rc        io.ReadCloser
	remaining int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		// Probe one byte so an exactly-at-limit body still ends cleanly.
		var one [1]byte
		n, err := b.rc.Read(one[:])
		if n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.rc.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// NewTransport returns a guarded RoundTripper.
func NewTransport(cfg Config) http.RoundTripper {
	maxBytes := cfg.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	base := &http.Transport{
		Proxy:                  nil, // never route through an env-configured proxy
		DialContext:            guardedDialContext(cfg),
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           32,
		IdleConnTimeout:        60 * time.Second,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		TLSClientConfig:        cfg.TLSClientConfig,
	}
	return &guardTransport{base: base, allowHTTP: cfg.AllowHTTP, maxBytes: maxBytes}
}

// NewClient returns a guarded http.Client. wrap, when non-nil, layers another
// RoundTripper (for example request signing) on top of the guard; the guard
// stays the transport that actually dials.
func NewClient(cfg Config, wrap func(http.RoundTripper) http.RoundTripper) *http.Client {
	var rt http.RoundTripper = NewTransport(cfg)
	if wrap != nil {
		rt = wrap(rt)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: rt,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirect
		},
	}
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the pool.
func (t *guardTransport) CloseIdleConnections() {
	if ci, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}
