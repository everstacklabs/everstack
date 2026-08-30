package tools

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsDeniedDialIP(t *testing.T) {
	cases := []struct {
		ip     string
		denied bool
	}{
		// Denied — internal / metadata / private / loopback.
		{"127.0.0.1", true},
		{"::1", true},
		{"169.254.169.254", true}, // cloud metadata endpoint
		{"169.254.0.1", true},     // link-local
		{"10.0.0.5", true},        // RFC1918
		{"172.16.4.4", true},      // RFC1918
		{"172.31.255.255", true},  // RFC1918 upper bound
		{"192.168.1.1", true},     // RFC1918
		{"100.64.0.1", true},      // CGNAT / Tailscale
		{"100.127.255.254", true}, // CGNAT upper bound
		{"0.0.0.0", true},         // unspecified
		{"fe80::1", true},         // IPv6 link-local
		{"fc00::1", true},         // IPv6 unique-local
		{"fd12:3456::1", true},    // IPv6 unique-local
		{"224.0.0.1", true},       // multicast
		{"::ffff:10.0.0.1", true}, // IPv4-mapped private
		// Allowed — genuine public addresses.
		{"93.184.216.34", false},        // example.com
		{"8.8.8.8", false},              // public DNS
		{"1.1.1.1", false},              // public DNS
		{"172.32.0.1", false},           // just outside RFC1918 172.16/12
		{"100.63.255.255", false},       // just below CGNAT 100.64/10
		{"100.128.0.0", false},          // just above CGNAT 100.64/10
		{"2606:4700:4700::1111", false}, // public IPv6 (Cloudflare)
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("bad test IP %q", c.ip)
		}
		if got := isDeniedDialIP(ip); got != c.denied {
			t.Errorf("isDeniedDialIP(%s) = %v, want %v", c.ip, got, c.denied)
		}
	}
	if !isDeniedDialIP(nil) {
		t.Error("isDeniedDialIP(nil) = false, want true (fail closed)")
	}
}

// TestGuardedClientBlocksLoopback proves the guarded client refuses to connect
// to an internal address end-to-end: it points at a real httptest server on
// loopback and expects the dial to be rejected by the Control hook, not served.
func TestGuardedClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secret-internal-content"))
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := guardedHTTPClient().Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("guarded client reached loopback server %s; SSRF guard did not block", srv.URL)
	}
	if !strings.Contains(err.Error(), "ssrf guard") {
		t.Errorf("expected ssrf guard error, got: %v", err)
	}
}

// TestWebFetchNilClientIsGuarded confirms that a WebFetchHandler constructed the
// production way (no injected client) refuses to fetch an internal URL.
func TestWebFetchNilClientIsGuarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("internal-only"))
	}))
	defer srv.Close()

	h := &WebFetchHandler{} // nil HTTPClient -> guarded client
	out, err := h.Execute(context.Background(), map[string]interface{}{"url": srv.URL})
	if err != nil {
		// fetchLocal returns the dial error folded into the string result,
		// not as an error, so also accept the guarded-message-in-output form.
		if strings.Contains(err.Error(), "ssrf guard") {
			return
		}
		t.Fatalf("unexpected error type: %v", err)
	}
	if strings.Contains(out, "internal-only") {
		t.Fatalf("web_fetch with nil client reached internal server; got content: %q", out)
	}
	if !strings.Contains(out, "Failed to fetch") && !strings.Contains(out, "ssrf guard") {
		t.Errorf("expected a blocked-fetch result, got: %q", out)
	}
}
