package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/internalauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// Until 2026-08-19 the interceptor treated a request as authenticated when it
// carried `Sec-Fetch-Site: same-origin` (or a matching Origin/Referer), and
// then installed whatever `x-tenant-id` the caller supplied. The header is
// forbidden to scripts, which is what the code's comment leaned on, but it is
// an ordinary request header that curl or any Go client sets freely. That made
// this an unauthenticated cross-tenant read/write primitive.
//
// Every header combination below is attacker-controllable and must now 401.
func TestSpoofedSameOriginHeadersDoNotAuthenticate(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"sec-fetch-site same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}},
		{"sec-fetch-site none", map[string]string{"Sec-Fetch-Site": "none"}},
		{"sec-fetch-site same-site", map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"forged origin", map[string]string{"Origin": "http://example.com"}},
		{"forged referer", map[string]string{"Referer": "http://example.com/"}},
		{"forged everything plus victim tenant", map[string]string{
			"Sec-Fetch-Site": "same-origin",
			"Origin":         "http://example.com",
			"Referer":        "http://example.com/",
			"x-tenant-id":    "victim-tenant",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interceptor := NewAPIKeyInterceptor(false)
			reached := false
			handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if reached {
				t.Fatal("handler was reached: an unauthenticated request got through")
			}
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rr.Code)
			}
		})
	}
}

// The legitimate consumer of the old bypass was this process calling its own
// API. It now presents a token an external caller cannot produce, and the
// caller-supplied tenant is honored only on that path.
func TestInternalTokenAuthenticatesAndCarriesTenant(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)

	var gotTenant string
	var gotAuthenticated bool
	handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenant = contextkeys.ExtractTenantID(r.Context())
		gotAuthenticated = contextkeys.IsTenantAuthenticated(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	internalauth.SetHeader(req.Header)
	req.Header.Set("x-tenant-id", "tenant-abc")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("internal call should be authenticated, got %d", rr.Code)
	}
	if !gotAuthenticated {
		t.Error("internal call should be marked tenant-authenticated")
	}
	if gotTenant != "tenant-abc" {
		t.Errorf("expected tenant-abc from x-tenant-id, got %q", gotTenant)
	}
}

// A wrong or absent internal token must not authenticate, and must not let the
// x-tenant-id through either.
func TestWrongInternalTokenIsRejected(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	reached := false
	handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(internalauth.Header, "0000000000000000000000000000000000000000000000000000000000000000")
	req.Header.Set("x-tenant-id", "victim-tenant")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if reached {
		t.Fatal("a forged internal token authenticated the request")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
