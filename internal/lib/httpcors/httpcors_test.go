package httpcors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, p *Policy, method, origin string) *httptest.ResponseRecorder {
	t.Helper()
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, "https://billing.everstack.ai/v1/x", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// The vulnerability: billing, auth and self-hosted auth each reflected any
// Origin back with Access-Control-Allow-Credentials: true, so any page on the
// internet could make authenticated cross-origin requests with the victim's
// cookies and read the responses.
func TestArbitraryOriginIsNotReflected(t *testing.T) {
	p := NewPolicy([]string{"https://app.everstack.ai"})

	for _, evil := range []string{
		"https://evil.com",
		"http://app.everstack.ai.evil.com",
		"https://app-everstack.ai",
		"null",
		"https://app.everstack.ai.attacker.test",
	} {
		rr := serve(t, p, http.MethodGet, evil)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q was reflected as %q", evil, got)
		}
		if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("origin %q got credentials allowed", evil)
		}
	}
}

func TestAllowedOriginGetsCredentialedCORS(t *testing.T) {
	p := NewPolicy([]string{"https://app.everstack.ai"})
	rr := serve(t, p, http.MethodGet, "https://app.everstack.ai")

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.everstack.ai" {
		t.Errorf("expected the origin echoed, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected credentials allowed, got %q", got)
	}
}

// "*" with credentials is rejected by browsers, so the credentialed call just
// fails. It must never be emitted.
func TestWildcardIsNeverEmitted(t *testing.T) {
	for _, p := range []*Policy{
		NewPolicy([]string{"https://app.everstack.ai"}),
		NewPolicy(nil),
	} {
		for _, origin := range []string{"", "https://app.everstack.ai", "https://evil.com"} {
			rr := serve(t, p, http.MethodGet, origin)
			if rr.Header().Get("Access-Control-Allow-Origin") == "*" {
				t.Fatalf("wildcard emitted for origin %q", origin)
			}
		}
	}
}

// A request with no Origin is not a cross-origin browser request and needs no
// CORS headers. The old middlewares emitted "*" here.
func TestNoOriginGetsNoCORSHeaders(t *testing.T) {
	p := NewPolicy([]string{"https://app.everstack.ai"})
	rr := serve(t, p, http.MethodGet, "")

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO without an Origin, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("expected no credentials header without an Origin, got %q", got)
	}
}

// Vary: Origin must be set even on denied and origin-less requests, so a shared
// cache cannot serve one origin's response to another.
func TestVaryOriginAlwaysSet(t *testing.T) {
	p := NewPolicy([]string{"https://app.everstack.ai"})
	for _, origin := range []string{"", "https://app.everstack.ai", "https://evil.com"} {
		rr := serve(t, p, http.MethodGet, origin)
		if got := rr.Header().Get("Vary"); got != "Origin" {
			t.Errorf("origin %q: expected Vary: Origin, got %q", origin, got)
		}
	}
}

func TestPreflightFromDisallowedOriginGetsNoHeaders(t *testing.T) {
	p := NewPolicy([]string{"https://app.everstack.ai"})
	rr := serve(t, p, http.MethodOptions, "https://evil.com")

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("preflight leaked ACAO %q to a disallowed origin", got)
	}
}

func TestNormalization(t *testing.T) {
	p := NewPolicy([]string{"  https://App.Everstack.AI/  "})
	if !p.Allows("https://app.everstack.ai") {
		t.Error("configured origin should match after trimming/lowercasing")
	}
	if p.Allows("") {
		t.Error("empty origin must never be allowed")
	}
	if p.Allows("null") {
		t.Error("the null origin must never be allowed")
	}
}

func TestPolicyFromEnvPrecedence(t *testing.T) {
	t.Setenv("EVS_CORS_ALLOWED_ORIGINS", "https://a.test, https://b.test")
	t.Setenv("EVS_CLOUD_APP_URL", "https://ignored.test")
	p := PolicyFromEnv()
	if !p.Allows("https://a.test") || !p.Allows("https://b.test") {
		t.Error("explicit list should be honored, including after the comma space")
	}
	if p.Allows("https://ignored.test") {
		t.Error("EVS_CLOUD_APP_URL must not apply when the explicit list is set")
	}

	t.Setenv("EVS_CORS_ALLOWED_ORIGINS", "")
	p = PolicyFromEnv()
	if !p.Allows("https://ignored.test") {
		t.Error("EVS_CLOUD_APP_URL should be the fallback")
	}
}

func TestDefaultPolicyStillAllowsTheCloudApp(t *testing.T) {
	// The cloud frontend calls auth/billing/license on separate hosts with
	// cookies. An empty default would break sign-in on deployments that have
	// not set the variable yet.
	t.Setenv("EVS_CORS_ALLOWED_ORIGINS", "")
	t.Setenv("EVS_CLOUD_APP_URL", "")
	t.Setenv("EVS_SERVICES_EMAIL_CLOUD_URL", "")
	p := PolicyFromEnv()
	if !p.Allows("https://app.everstack.ai") {
		t.Error("default policy must still allow the cloud app origin")
	}
	if p.Allows("https://evil.com") {
		t.Error("default policy must not allow arbitrary origins")
	}
}
