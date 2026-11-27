package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/lib/requesthost"
)

// contextFor runs a request through requesthost.Middleware, as the gateway's
// Connect registration does, and returns the context a handler would get.
func contextFor(t *testing.T, target string, forwardedProto string) (context.Context, http.Header) {
	t.Helper()
	var ctx context.Context
	var header http.Header
	h := requesthost.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, header = r.Context(), r.Header
	}))
	req := httptest.NewRequest(http.MethodPost, target, nil)
	if forwardedProto != "" {
		req.Header.Set("X-Forwarded-Proto", forwardedProto)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return ctx, header
}

// The gateway Connect Login/Register cookie used cfg.Session.Secure as-is, so
// with session_secure: true an admin reaching the gateway over plain HTTP on
// a Tailscale or private address got a cookie the browser refused to store
// and looped back to login. It now follows the same rule as the HTTP routes.
func TestConnectSessionCookie_FollowsRequestTransport(t *testing.T) {
	cfg := &domain.InternalConfig{Session: domain.SessionConfig{
		CookieName: "everstack_session",
		Secure:     true,
		HTTPOnly:   true,
		SameSite:   "lax",
	}}
	h := &AuthHandler{svc: service.NewAuthService(cfg, nil, nil), cfg: cfg}
	session := &domain.Session{Token: "t", ExpiresAt: time.Now().Add(time.Hour)}

	for _, tc := range []struct {
		name       string
		target     string
		proto      string
		wantSecure bool
	}{
		{"tailscale plain HTTP", "http://100.64.0.1:8089/everstack.auth.v1.AuthService/Login", "", false},
		{"private plain HTTP", "http://10.0.0.5:8089/everstack.auth.v1.AuthService/Login", "", false},
		{"localhost plain HTTP", "http://localhost:8089/everstack.auth.v1.AuthService/Login", "", false},
		{"public host behind TLS proxy", "http://gateway.everstack.ai/everstack.auth.v1.AuthService/Login", "https", true},
		{"public host plain HTTP", "http://gateway.everstack.ai/everstack.auth.v1.AuthService/Login", "", true},
		{"tailscale via TLS proxy", "http://100.64.0.1:8089/everstack.auth.v1.AuthService/Login", "https", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, header := contextFor(t, tc.target, tc.proto)
			value := h.buildSessionCookie(ctx, header, session)
			if got := strings.Contains(value, "; Secure"); got != tc.wantSecure {
				t.Fatalf("Secure = %v, want %v: %q", got, tc.wantSecure, value)
			}
		})
	}

	// Without the middleware there is no Host, so the cookie keeps the
	// configured Secure flag rather than guessing.
	if value := h.buildSessionCookie(context.Background(), http.Header{}, session); !strings.Contains(value, "; Secure") {
		t.Fatalf("expected the configured Secure flag with no host: %q", value)
	}
}
