package outboundguard

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

// publicIP is a routable address the tests pretend a host resolves to. The
// injected dialer never connects to it; it connects to the httptest server.
var publicIP = netip.MustParseAddr("8.8.8.8")

func toServer(srv *httptest.Server) DialFunc {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
}

func staticResolver(addrs ...netip.Addr) Resolver {
	return func(context.Context, string) ([]netip.Addr, error) { return addrs, nil }
}

func TestIsPublicUnicastRejectsBlockedRanges(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "100.64.0.1", "100.127.255.254", "0.0.0.0",
		"224.0.0.1", "239.255.255.250", "255.255.255.255", "240.0.0.1",
		"::", "::1", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:8.8.8.8",
		"64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1", "2001:db8::1", "198.18.0.1",
	}
	for _, s := range blocked {
		if IsPublicUnicast(netip.MustParseAddr(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !IsPublicUnicast(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

func TestValidateURL(t *testing.T) {
	bad := []string{
		"http://gateway.example.com",          // http without dev flag
		"ftp://gateway.example.com",           // scheme
		"https://user:pw@gateway.example.com", // userinfo
		"https://",                            // no host
		"https://127.0.0.1",                   // loopback literal
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]:8080",
		"https://[::ffff:169.254.169.254]",
		"https://[fe80::1%25eth0]",
		"https://2130706433", // decimal 127.0.0.1
		"https://0x7f000001", // hex
		"https://0177.0.0.1", // octal-ish
		"https://10.1",       // short dotted
	}
	for _, u := range bad {
		if err := ValidateURL(u, false); !errors.Is(err, ErrBlocked) {
			t.Errorf("ValidateURL(%q) = %v, want ErrBlocked", u, err)
		}
	}
	if err := ValidateURL("https://gateway.example.com/x", false); err != nil {
		t.Errorf("public https URL rejected: %v", err)
	}
	if err := ValidateURL("http://gateway.example.com", true); err != nil {
		t.Errorf("http with dev flag rejected: %v", err)
	}
	if err := ValidateURL("http://127.0.0.1", true); !errors.Is(err, ErrBlocked) {
		t.Errorf("dev flag must not relax address checks: %v", err)
	}
}

func TestClientRejectsHostsResolvingToBlockedAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()

	for _, ip := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "100.100.100.100", "::1", "fe80::1", "fd00::1", "224.0.0.1", "0.0.0.0"} {
		c := NewClient(Config{AllowHTTP: true, Resolver: staticResolver(netip.MustParseAddr(ip)), Dial: toServer(srv)}, nil)
		_, err := c.Get("http://gateway.example.com/")
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("resolve->%s: err=%v, want ErrBlocked", ip, err)
		}
	}
	// A mix of public and private answers is refused as a whole.
	c := NewClient(Config{AllowHTTP: true, Resolver: staticResolver(publicIP, netip.MustParseAddr("10.0.0.1")), Dial: toServer(srv)}, nil)
	if _, err := c.Get("http://gateway.example.com/"); !errors.Is(err, ErrBlocked) {
		t.Errorf("mixed answer: err=%v, want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("blocked requests reached the server %d times", hits.Load())
	}
}

func TestClientAllowsPublicAndRefusesHTTPWithoutFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()

	c := NewClient(Config{AllowHTTP: true, Resolver: staticResolver(publicIP), Dial: toServer(srv)}, nil)
	resp, err := c.Get("http://gateway.example.com/")
	if err != nil {
		t.Fatalf("public target refused: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}

	strict := NewClient(Config{Resolver: staticResolver(publicIP), Dial: toServer(srv)}, nil)
	if _, err := strict.Get("http://gateway.example.com/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("http without dev flag: err=%v, want ErrBlocked", err)
	}
}

func TestClientHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "tls") }))
	defer srv.Close()
	tlsCfg := srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	c := NewClient(Config{Resolver: staticResolver(publicIP), Dial: toServer(srv), TLSClientConfig: tlsCfg}, nil)
	// httptest certificates are valid for example.com.
	resp, err := c.Get("https://example.com/")
	if err != nil {
		t.Fatalf("https public target refused: %v", err)
	}
	resp.Body.Close()
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var internalHit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal" {
			internalHit.Store(true)
			return
		}
		http.Redirect(w, r, "/internal", http.StatusFound)
	}))
	defer srv.Close()
	c := NewClient(Config{AllowHTTP: true, Resolver: staticResolver(publicIP), Dial: toServer(srv)}, nil)
	_, err := c.Get("http://gateway.example.com/start")
	if !errors.Is(err, ErrRedirect) {
		t.Fatalf("err=%v, want ErrRedirect", err)
	}
	if internalHit.Load() {
		t.Fatal("redirect was followed")
	}
}

// DNS rebinding: the first lookup returns a public address, the next one a
// private address. Every dial must re-resolve and re-check, so the second
// connection is refused.
func TestClientBlocksDNSRebinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	var lookups atomic.Int32
	resolver := func(context.Context, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{publicIP}, nil
		}
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	}
	var dialed []string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return toServer(srv)(ctx, network, addr)
	}
	c := NewClient(Config{AllowHTTP: true, Resolver: resolver, Dial: dial}, nil)
	resp, err := c.Get("http://rebind.example.com/")
	if err != nil {
		t.Fatalf("first (public) request: %v", err)
	}
	resp.Body.Close()
	c.CloseIdleConnections() // force a fresh dial, as a new connection would
	if _, err := c.Get("http://rebind.example.com/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("rebound request: err=%v, want ErrBlocked", err)
	}
	if len(dialed) != 1 || !strings.HasPrefix(dialed[0], "8.8.8.8:") {
		t.Fatalf("dialed %v; want only the validated public IP", dialed)
	}
}

func TestClientCapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", 4096))
	}))
	defer srv.Close()
	c := NewClient(Config{AllowHTTP: true, MaxResponseBytes: 1024, Resolver: staticResolver(publicIP), Dial: toServer(srv)}, nil)
	resp, err := c.Get("http://gateway.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err=%v, want ErrResponseTooLarge", err)
	}
}

func TestWrapLayersOnTopOfGuard(t *testing.T) {
	var sawHeader atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader.Store(r.Header.Get("X-Test") == "1")
	}))
	defer srv.Close()
	wrap := func(next http.RoundTripper) http.RoundTripper {
		return rtFunc(func(r *http.Request) (*http.Response, error) {
			r = r.Clone(r.Context())
			r.Header.Set("X-Test", "1")
			return next.RoundTrip(r)
		})
	}
	c := NewClient(Config{AllowHTTP: true, Resolver: staticResolver(publicIP), Dial: toServer(srv)}, wrap)
	resp, err := c.Get("http://gateway.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !sawHeader.Load() {
		t.Fatal("wrapper not applied")
	}
	// The wrapper does not bypass the guard.
	blocked := NewClient(Config{AllowHTTP: true, Resolver: staticResolver(netip.MustParseAddr("127.0.0.1")), Dial: toServer(srv)}, wrap)
	if _, err := blocked.Get("http://gateway.example.com/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err=%v, want ErrBlocked", err)
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
