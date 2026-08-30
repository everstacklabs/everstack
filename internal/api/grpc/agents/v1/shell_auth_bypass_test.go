package v1

import (
	"context"
	"net/http/httptest"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// Until 2026-08-19 authenticateShellRequest authorized any request carrying
// `Sec-Fetch-Site: same-origin`, a matching Origin/Referer, or a WebSocket
// Origin on the same host. Those are not credentials: the headers are
// forbidden to scripts, but any HTTP client sets them freely.
//
// Combined with handlers that resolve the sandbox from a caller-supplied id
// with no tenant scoping, that was an unauthenticated cross-tenant remote
// shell. Every case here must be refused.
func TestSpoofedHeadersDoNotAuthorizeShell(t *testing.T) {
	s := &Server{} // no db, so no single-tenant fallback

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"sec-fetch-site same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{"sec-fetch-site same-site", map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"sec-fetch-site none", map[string]string{"Sec-Fetch-Site": "none"}},
		{"forged origin", map[string]string{"Origin": "http://localhost:8089"}},
		{"forged referer", map[string]string{"Referer": "http://localhost:8089/"}},
		{"websocket upgrade with forged origin", map[string]string{
			"Upgrade": "websocket",
			"Origin":  "http://localhost:8089",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/sandboxes/sbx-victim/shell", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			// No authenticated tenant on the context: this is an outside caller.
			err := s.authenticateShellRequest(req, "sbx-victim", "victim-tenant")
			if err == nil {
				t.Fatal("spoofed headers authorized a shell into another tenant's sandbox")
			}
		})
	}
}

// The sandbox owner still gets a shell.
func TestOwnerIsAuthorizedForShell(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/v1/sandboxes/sbx-a/shell", nil)
	req = req.WithContext(contextkeys.WithTenantID(context.Background(), "tenant-a"))

	if err := s.authenticateShellRequest(req, "sbx-a", "tenant-a"); err != nil {
		t.Fatalf("owner should be authorized, got %v", err)
	}
}

// An authenticated caller from a different tenant is refused, and the error
// must not confirm that the sandbox exists.
func TestCrossTenantShellIsRefusedWithoutDisclosure(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/v1/sandboxes/sbx-b/shell", nil)
	req = req.WithContext(contextkeys.WithTenantID(context.Background(), "tenant-a"))

	err := s.authenticateShellRequest(req, "sbx-b", "tenant-b")
	if err == nil {
		t.Fatal("cross-tenant shell must be refused")
	}
	if got := err.Error(); got != "sandbox not found" {
		t.Errorf("error must not disclose existence or ownership, got %q", got)
	}
}

// Even an authenticated caller cannot use spoofed headers to reach another
// tenant: the headers are simply not consulted any more.
func TestSpoofedHeadersDoNotHelpAnAuthenticatedCaller(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/v1/sandboxes/sbx-b/shell", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://localhost:8089")
	req = req.WithContext(contextkeys.WithTenantID(context.Background(), "tenant-a"))

	if err := s.authenticateShellRequest(req, "sbx-b", "tenant-b"); err == nil {
		t.Fatal("spoofed headers let an authenticated caller cross tenants")
	}
}
