package rphttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/everstacklabs/everstack/pkg/oidc"
	"github.com/everstacklabs/everstack/pkg/oidc/ophttp"
)

// --- fakes for the OP side -------------------------------------------------

type fakeAuth struct{ user ophttp.CurrentUser }

func (f fakeAuth) CurrentUser(*http.Request) (ophttp.CurrentUser, bool) { return f.user, true }
func (f fakeAuth) LoginURL(*http.Request) string                        { return "/login" }

type fakeAccess struct{ id ophttp.AccessIdentity }

func (f fakeAccess) Authorize(context.Context, ophttp.CurrentUser, oidc.Client) (ophttp.AccessResult, error) {
	return ophttp.AccessResult{
		Decision: ophttp.AccessAllowed,
		Identity: f.id,
	}, nil
}

// --- fake minter for the RP side -------------------------------------------

type captureMinter struct{ claims *oidc.IDClaims }

func (m *captureMinter) Mint(_ http.ResponseWriter, _ *http.Request, c *oidc.IDClaims) error {
	m.claims = c
	return nil
}

// TestFederationEndToEnd drives the full cloud-OP <-> instance-RP flow over real
// HTTP: RP login -> OP authorize -> RP callback -> code exchange -> id_token
// verification (via the OP's published JWKS) -> instance session mint.
func TestFederationEndToEnd(t *testing.T) {
	const (
		issuerClientID = "inst_acme_prod"
		rpRedirect     = "https://acme-prod.everstack.ai/auth/callback"
	)

	// --- Stand up the OP ---
	ks, err := oidc.GenerateKeySet(2048)
	if err != nil {
		t.Fatal(err)
	}
	clients := oidc.NewMemClientStore()
	clients.Register(oidc.Client{
		ID:           issuerClientID,
		RedirectURIs: []string{rpRedirect},
		OrgID:        "org-acme",
		InstanceID:   "inst-1",
	})

	mux := http.NewServeMux()
	opServer := httptest.NewServer(mux)
	defer opServer.Close()

	provider := oidc.NewProvider(oidc.ProviderConfig{Issuer: opServer.URL}, ks, oidc.NewMemCodeStore(), clients)
	opHandler := ophttp.New(provider, clients,
		fakeAuth{user: ophttp.CurrentUser{UserID: "user-alice", Email: "alice@acme.com", Name: "Alice", EmailVerified: true}},
		fakeAccess{id: ophttp.AccessIdentity{OrgID: "org-acme", OrgSlug: "acme", InstanceID: "inst-1"}},
	)
	opHandler.Register(mux)

	// --- Stand up the RP ---
	opClient := NewOPClient(opServer.URL, issuerClientID, rpRedirect, "openid profile email org", opServer.Client())
	verifier := oidc.NewVerifier(opServer.URL, issuerClientID, oidc.NewHTTPJWKSSource(opServer.URL+oidc.PathJWKS))
	minter := &captureMinter{}
	rp := New(opClient, verifier, minter, "/dashboard", false)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// 1. RP login -> capture authorize URL + flow cookie.
	loginRec := httptest.NewRecorder()
	rp.Login(loginRec, httptest.NewRequest(http.MethodGet, "https://acme-prod.everstack.ai/auth/login", nil))
	authorizeURL := loginRec.Result().Header.Get("Location")
	if authorizeURL == "" {
		t.Fatal("login did not redirect to authorize")
	}
	var flowCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == flowCookieName {
			flowCookie = c
		}
	}
	if flowCookie == nil {
		t.Fatal("login did not set the flow cookie")
	}

	// 2. Hit the OP authorize endpoint -> capture the redirect back to the RP.
	authResp, err := noRedirect.Get(authorizeURL)
	if err != nil {
		t.Fatalf("authorize request: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: expected 302, got %d", authResp.StatusCode)
	}
	cbURL, err := url.Parse(authResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("bad callback location: %v", err)
	}
	if cbURL.Query().Get("code") == "" {
		t.Fatalf("authorize returned no code: %s", cbURL.String())
	}

	// 3. RP callback -> exchange + verify + mint.
	cbReq := httptest.NewRequest(http.MethodGet, cbURL.String(), nil)
	cbReq.AddCookie(flowCookie)
	cbRec := httptest.NewRecorder()
	rp.Callback(cbRec, cbReq)

	if cbRec.Code != http.StatusFound {
		t.Fatalf("callback failed: status %d body %q", cbRec.Code, cbRec.Body.String())
	}
	if got := cbRec.Header().Get("Location"); got != "/dashboard" {
		t.Errorf("post-login redirect = %q, want /dashboard", got)
	}
	if minter.claims == nil {
		t.Fatal("session was not minted")
	}
	if minter.claims.Subject != "user-alice" || minter.claims.OrgID != "org-acme" || minter.claims.InstanceID != "inst-1" {
		t.Errorf("minted claims wrong: %+v", minter.claims)
	}
	if minter.claims.Email != "alice@acme.com" {
		t.Errorf("email claim missing: %+v", minter.claims)
	}
}

// TestCallbackStateMismatch ensures a forged/missing state is rejected.
func TestCallbackStateMismatch(t *testing.T) {
	opClient := NewOPClient("https://app.everstack.ai", "c", "https://i/cb", "", nil)
	rp := New(opClient, oidc.NewVerifier("https://app.everstack.ai", "c", oidc.NewStaticJWKSSource(mustKeys(t))), &captureMinter{}, "/", false)

	req := httptest.NewRequest(http.MethodGet, "https://i/cb?code=abc&state=evil", nil)
	// no flow cookie set
	rec := httptest.NewRecorder()
	rp.Callback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing flow cookie, got %d", rec.Code)
	}
}

func TestClearFlowPreservesCookieSecurityAttributes(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := &Handler{cookieSecure: true}

	handler.clearFlow(recorder)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("clearFlow() cookies = %d", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly {
		t.Fatalf("clearFlow() cookie security = Secure %v HttpOnly %v", cookie.Secure, cookie.HttpOnly)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("clearFlow() SameSite = %v", cookie.SameSite)
	}
}

func mustKeys(t *testing.T) *oidc.KeySet {
	t.Helper()
	ks, err := oidc.GenerateKeySet(2048)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}
