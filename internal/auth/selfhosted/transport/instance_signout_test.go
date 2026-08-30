package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/gorilla/mux"
)

func TestInstanceSignoutRedirect(t *testing.T) {
	t.Setenv("EVS_CLOUD_URL", "https://app.everstack.ai/")
	if got, want := instanceSignoutRedirect("acme"), "https://app.everstack.ai/acme/instances"; got != want {
		t.Errorf("with slug = %q, want %q", got, want)
	}
	if got, want := instanceSignoutRedirect(""), "https://app.everstack.ai"; got != want {
		t.Errorf("without slug = %q, want %q", got, want)
	}

	// No cloud configured: the FE must fall back to the instance's own
	// /login rather than being sent to an empty origin.
	t.Setenv("EVS_CLOUD_URL", "")
	if got := instanceSignoutRedirect("acme"); got != "" {
		t.Errorf("without cloud url = %q, want empty", got)
	}
}

// signoutTestHandler builds a handler with no repositories. Session deletion
// needs a DB, so these cases cover the no-cookie path, where the endpoint must
// still expire the cookie and answer 200.
func signoutTestHandler() *SelfHostedAuthHandler {
	cfg := &domain.InternalConfig{Session: domain.SessionConfig{
		CookieName: "everstack_session",
		HTTPOnly:   true,
		SameSite:   "lax",
	}}
	return NewSelfHostedAuthHandler(cfg, service.NewSelfHostedAuthService(cfg, nil, nil, nil, nil, nil, nil, nil), 0, nil)
}

func TestHandleInstanceSignoutWithoutSessionClearsCookie(t *testing.T) {
	t.Setenv("EVS_CLOUD_URL", "https://app.everstack.ai")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://acme-a1b2c3.eu-gra-1.everstack.ai/auth/instance-signout", nil)
	signoutTestHandler().handleInstanceSignout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"redirect_to":"https://app.everstack.ai"`) {
		t.Errorf("body = %q, want a cloud redirect_to", body)
	}

	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "everstack_session" && c.Value == "" && c.MaxAge < 0 {
			cleared = true
		}
		// An instance must never issue a delete for the cloud's
		// parent-domain cookie — that would sign the user out of the cloud
		// as a side effect of leaving one instance.
		if strings.HasPrefix(c.Domain, ".") {
			t.Errorf("signout set a parent-domain cookie %q (domain %q)", c.Name, c.Domain)
		}
	}
	if !cleared {
		t.Error("session cookie was not expired")
	}

	var marker *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == domain.InstanceSignedOutCookie {
			marker = c
		}
	}
	if marker == nil || marker.Value == "" || marker.MaxAge <= 0 {
		t.Fatalf("signed-out marker not set: %+v", marker)
	}
	if marker.Domain != "" {
		t.Errorf("marker domain = %q, want host-only", marker.Domain)
	}
}

// The whole point of the marker: with the cloud's parent-domain cookie still
// in the jar, a signed-out browser must resolve to no session so the SPA
// bounces it to the cloud instead of silently signing it back in.
func TestSignedOutMarkerSuppressesCloudFallback(t *testing.T) {
	header := http.Header{}
	if instanceSignedOut(header) {
		t.Error("empty header reported as signed out")
	}

	header.Set("Cookie", "es_everstack_session=cloud-token")
	if instanceSignedOut(header) {
		t.Error("cloud cookie alone reported as signed out")
	}

	header.Set("Cookie", "es_everstack_session=cloud-token; "+domain.InstanceSignedOutCookie+"=1")
	if !instanceSignedOut(header) {
		t.Error("marker present but not detected")
	}
}

// Re-entering through the relay must win over a stale marker, otherwise a
// signed-out user can never get back in.
func TestMintingSessionClearsSignedOutMarker(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://acme-a1b2c3.eu-gra-1.everstack.ai/auth/cloud-callback", nil)

	cfg := &domain.InternalConfig{Session: domain.SessionConfig{
		CookieName: "everstack_session",
		MaxAge:     time.Hour,
		HTTPOnly:   true,
		SameSite:   "lax",
	}}
	svc := service.NewSelfHostedAuthService(cfg, nil, nil, nil, nil, nil, nil, nil)
	svc.SetSessionCookieForRequest(rec, req, &domain.Session{
		Token:     "fresh-instance-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	var marker *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == domain.InstanceSignedOutCookie {
			marker = c
		}
	}
	if marker == nil {
		t.Fatal("minting a session did not touch the signed-out marker")
	}
	if marker.Value != "" || marker.MaxAge >= 0 {
		t.Errorf("marker not cleared: value=%q maxAge=%d", marker.Value, marker.MaxAge)
	}
}

// The FE POSTs this path; a GET-only or missing registration is what produced
// the "signout failed: HTTP 404" the endpoint was added to fix.
func TestInstanceSignoutRouteIsRegisteredForPOST(t *testing.T) {
	router := mux.NewRouter()
	signoutTestHandler().RegisterHTTPRoutes(router)

	var match mux.RouteMatch
	req := httptest.NewRequest(http.MethodPost, "https://acme-a1b2c3.eu-gra-1.everstack.ai/auth/instance-signout", nil)
	if !router.Match(req, &match) || match.MatchErr != nil {
		t.Fatalf("POST /auth/instance-signout did not match a route (err=%v)", match.MatchErr)
	}
}
