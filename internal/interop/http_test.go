package interop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func withTestTenant(r *http.Request) context.Context {
	ctx := contextkeys.WithTenantID(r.Context(), "tenant-A")
	ctx = contextkeys.WithUserID(ctx, "dashboard-user")
	return contextkeys.WithTenantAuthenticated(ctx)
}

type muxAdapter struct{ r *mux.Router }

func (a muxAdapter) HandleFunc(path string, f http.HandlerFunc) { a.r.HandleFunc(path, f) }

func newRouter(adkCapable bool) *mux.Router {
	r := mux.NewRouter()
	// nil DB is safe: every handler fails closed on missing tenant BEFORE
	// touching the store, which is exactly what these tests assert.
	NewHandlers(NewStore(nil), false, adkCapable).Mount(muxAdapter{r})
	return r
}

// All admin endpoints must fail closed (401) when no tenant is in the request
// context - they must never fall back to a default/only tenant.
func TestAdminEndpointsFailClosedWithoutTenant(t *testing.T) {
	r := newRouter(false)
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/interop/a2a/published", ""},
		{http.MethodPut, "/api/interop/a2a/published/ag_1", `{"enabled":true}`},
		{http.MethodGet, "/api/interop/mcp/tools", ""},
		{http.MethodPut, "/api/interop/mcp/tools/run_agent", `{"enabled":false}`},
		{http.MethodGet, "/api/interop/remotes", ""},
		{http.MethodPost, "/api/interop/remotes", `{"name":"x","endpoint":"https://y"}`},
		{http.MethodDelete, "/api/interop/remotes/abc", ""},
		{http.MethodGet, "/api/interop/adk/status", ""},
		{http.MethodGet, "/api/interop/runs", ""},
		{http.MethodPost, "/api/interop/runs", `{"remote":"x","message":"m","idempotency_key":"k-123456"}`},
		{http.MethodGet, "/api/interop/runs/6f1c0b1e-0d5f-4a55-9a2b-0c4c1d2e3f40", ""},
		{http.MethodPost, "/api/interop/runs/6f1c0b1e-0d5f-4a55-9a2b-0c4c1d2e3f40/cancel", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401 without tenant, got %d", c.method, c.path, rr.Code)
		}
	}
}

// Writes must be application/json so a cross-site form or text/plain post
// (no CORS preflight) cannot reach a handler. Reads are unaffected.
func TestWritesRequireJSON(t *testing.T) {
	r := newRouter(false)
	for _, c := range []struct{ method, path, contentType string }{
		{http.MethodPost, "/api/interop/runs", "text/plain"},
		{http.MethodPost, "/api/interop/runs", "application/x-www-form-urlencoded"},
		{http.MethodPost, "/api/interop/remotes", ""},
		{http.MethodPut, "/api/interop/mcp/tools/run_agent", "multipart/form-data; boundary=x"},
		{http.MethodDelete, "/api/interop/remotes/abc", "text/plain"},
		{http.MethodPost, "/api/interop/runs/6f1c0b1e-0d5f-4a55-9a2b-0c4c1d2e3f40/refresh", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{"remote":"x"}`))
		if c.contentType != "" {
			req.Header.Set("Content-Type", c.contentType)
		}
		req = req.WithContext(withTestTenant(req))
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s %s (%q): want 415, got %d", c.method, c.path, c.contentType, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/interop/adk/status", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code == http.StatusUnsupportedMediaType {
		t.Error("GET must not require a JSON content type")
	}
}

func TestAdkStatusReportsCapability(t *testing.T) {
	t.Setenv("EVS_ADK_NETWORK_MODE", "whitelist")
	// ADK is ungated: when the instance is capable (sandbox backend present), the
	// status reports enabled. Inject a tenant to get past the fail-closed gate.
	r := newRouter(true)
	req := httptest.NewRequest(http.MethodGet, "/api/interop/adk/status", nil)
	req = req.WithContext(withTestTenant(req))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"enabled":true`) || !strings.Contains(rr.Body.String(), "whitelist") {
		t.Errorf("unexpected adk status: %s", rr.Body.String())
	}
}
