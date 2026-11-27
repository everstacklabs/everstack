package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/lib/requesthost"
)

// A cookie is only cleared by a Set-Cookie with the same Name, Path and
// Domain. The Connect Login cookie carried no Domain while sign-out cleared
// the configured one, so with session.domain set a session minted through
// Connect Login survived sign-out in the browser. Set and clear must agree
// on every attribute that identifies or scopes the cookie.
func TestConnectSessionCookieMatchesTheClearedCookie(t *testing.T) {
	for _, tc := range []struct {
		name, domain, target, proto string
	}{
		{"parent domain over TLS", ".everstack.example", "http://gw.everstack.example/everstack.auth.v1.AuthService/Login", "https"},
		{"host-only", "", "http://gw.everstack.example/everstack.auth.v1.AuthService/Login", "https"},
		{"plain HTTP on a private address", ".everstack.example", "http://10.0.0.5:8089/everstack.auth.v1.AuthService/Login", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &domain.InternalConfig{Session: domain.SessionConfig{
				CookieName: "everstack_session",
				Secure:     true,
				HTTPOnly:   true,
				SameSite:   "lax",
				Domain:     tc.domain,
			}}
			svc := service.NewAuthService(cfg, nil, nil)
			h := &AuthHandler{svc: svc, cfg: cfg}

			ctx, header := contextFor(t, tc.target, tc.proto)
			set := parseSetCookie(t, h.buildSessionCookie(ctx, header, &domain.Session{Token: "tok", ExpiresAt: time.Now().Add(time.Hour)}))

			// The SignOut RPC clears with ExpiredSessionCookie; the HTTP
			// routes with ClearSessionCookieForRequest. Both must match.
			cleared := parseSetCookie(t, svc.ExpiredSessionCookie(requesthost.Request(ctx, header)).String())
			rec := httptest.NewRecorder()
			svc.ClearSessionCookieForRequest(rec, requesthost.Request(ctx, header))
			if httpCleared := parseSetCookie(t, rec.Header().Get("Set-Cookie")); httpCleared.String() != cleared.String() {
				t.Fatalf("HTTP clear %s differs from RPC clear %s", httpCleared, cleared)
			}

			if cleared.MaxAge >= 0 || cleared.Value != "" {
				t.Fatalf("clear is not a delete: %+v", cleared)
			}
			if set.Name != cleared.Name || set.Path != cleared.Path || set.Domain != cleared.Domain ||
				set.HttpOnly != cleared.HttpOnly || set.Secure != cleared.Secure || set.SameSite != cleared.SameSite {
				t.Fatalf("set and clear disagree:\n set   %s\n clear %s", set.String(), cleared.String())
			}
			if want := tc.domain; want != "" && set.Domain != "everstack.example" {
				t.Fatalf("session cookie Domain = %q, want the configured %q", set.Domain, want)
			}
		})
	}
}

func parseSetCookie(t *testing.T, value string) *http.Cookie {
	t.Helper()
	c, err := http.ParseSetCookie(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return c
}
