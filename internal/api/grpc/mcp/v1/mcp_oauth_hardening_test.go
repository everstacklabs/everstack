package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp"
)

func oauthTestServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	return &Server{ctx: context.Background(), db: sqlx.NewDb(rawDB, "postgres"), registry: mcp.NewRegistry(nil)}, mock
}

// signedIn is the context the gateway's session middleware hands a signed-in
// member of tenantID.
func signedIn(tenantID string) context.Context {
	ctx := contextkeys.WithTenantID(context.Background(), tenantID)
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	return contextkeys.WithCloudUserID(ctx, "user-1")
}

func expectStateLookup(mock sqlmock.Sqlmock, tokenEndpoint string) {
	mock.ExpectQuery(regexp.QuoteMeta(`DELETE FROM mcp_oauth_states WHERE state = $1`)).
		WithArgs("st").
		WillReturnRows(sqlmock.NewRows([]string{"server_id", "tenant_id", "code_verifier", "redirect_uri", "token_endpoint", "client_id", "client_secret", "expires_at"}).
			AddRow(int64(7), "tenant-a", "verifier", "https://gw/mcp/oauth/callback", tokenEndpoint, "cid", "", time.Now().Add(time.Hour)))
}

func callback(s *Server, ctx context.Context, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp/oauth/callback?code=c&state=st", nil).WithContext(ctx)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	s.handleOAuthCallback(rec, req)
	return rec
}

// A tenant-chosen token endpoint controls its error body. None of it may reach
// the page rendered on the gateway's origin.
func TestOAuthCallback_TokenEndpointBodyNeverRendered(t *testing.T) {
	const payload = `</script><script>alert(document.cookie)</script>`
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(payload))
	}))
	defer token.Close()

	s, mock := oauthTestServer(t)
	expectStateLookup(mock, token.URL)

	rec := callback(s, context.Background())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "alert(") {
		t.Fatalf("token endpoint body reached the page:\n%s", body)
	}
}

func TestWriteOAuthCallbackError_EscapesBothContexts(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOAuthCallbackError(rec, `</script><img src=x onerror=alert(1)>`)
	body := rec.Body.String()
	if strings.Contains(body, "<img") || strings.Contains(body, "</script><") {
		t.Fatalf("message not escaped:\n%s", body)
	}
}

// On a shared gateway a browser that didn't start the flow can't finish it,
// even a signed-out one. The refusal text is the assertion: the egress guard
// would also stop this loopback token endpoint, so a request count proves nothing.
func TestOAuthCallback_RefusesBrowserThatDidNotInitiate(t *testing.T) {
	defer mcp.SetPublicEndpointsForTest(true)()

	for name, cookies := range map[string][]*http.Cookie{
		"no cookie":     nil,
		"other state":   {{Name: "__Host-evs_mcp_oauth", Value: "other"}},
		"non-host name": {{Name: "evs_mcp_oauth", Value: "st"}},
	} {
		s, mock := oauthTestServer(t)
		expectStateLookup(mock, "http://127.0.0.1:1/token")
		if got := callback(s, context.Background(), cookies...).Body.String(); !strings.Contains(got, "start the authorization again") {
			t.Fatalf("%s: not refused:\n%s", name, got)
		}
	}
}

// The initiating browser completes the flow on the real path: initiate sets the
// cookie and the callback accepts it. The switch flips after discovery because
// the egress guard it also controls would refuse this loopback test server.
func TestOAuthBinding_InitiatingBrowserCompletes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "oauth-authorization-server"):
			restore := mcp.SetPublicEndpointsForTest(true)
			t.Cleanup(restore)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
			})
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	s, mock := oauthTestServer(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT url FROM mcp_servers`)).
		WillReturnRows(sqlmock.NewRows([]string{"url"}).AddRow(srv.URL))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO mcp_oauth_states`)).WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://gw.example.com/mcp/oauth/initiate",
		strings.NewReader(`{"server_id":"7","callback_base_url":"https://gw.example.com"}`)).
		WithContext(signedIn("tenant-a"))
	s.handleOAuthInitiate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initiate status = %d: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	c := cookies[0]
	if c.Name != "__Host-evs_mcp_oauth" || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("binding cookie = %+v", c)
	}

	// The cookie passes when it carries the callback's state. The egress guard
	// then refuses the loopback token endpoint, so reaching the exchange is the
	// proof; token writes are covered by TestOAuthTokenWrites_ScopedToTenant.
	expectStateLookup(mock, srv.URL+"/token")
	got := callback(s, context.Background(), &http.Cookie{Name: c.Name, Value: "st"}).Body.String()
	if !strings.Contains(got, "Token exchange failed") {
		t.Fatalf("callback did not reach the token exchange:\n%s", got)
	}
}

func TestOAuthTokenWrites_ScopedToTenant(t *testing.T) {
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "a", "refresh_token": "r"})
	}))
	defer token.Close()

	s, mock := oauthTestServer(t)
	expectStateLookup(mock, token.URL)
	mock.ExpectExec(regexp.QuoteMeta(`WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL`)).
		WithArgs(sqlmock.AnyArg(), int64(7), "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`FROM mcp_servers WHERE id = $1 AND tenant_id = $2`)).
		WithArgs(int64(7), "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"name"}))

	if rec := callback(s, contextkeys.WithTenantID(context.Background(), "tenant-a")); rec.Code != http.StatusOK {
		t.Fatalf("owner's callback status = %d: %s", rec.Code, rec.Body.String())
	}

	mock.ExpectExec(regexp.QuoteMeta(`WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL`)).
		WithArgs(sqlmock.AnyArg(), int64(7), "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.makeTokenUpdateCallback(7, "tenant-a")(&mcp.AuthConfig{AccessToken: "a2"})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthInitiate_RefusesNonWebAuthorizationEndpoint(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "oauth-authorization-server") {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": "javascript:alert(1)",
				"token_endpoint":         srv.URL + "/token",
			})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	s, mock := oauthTestServer(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT url FROM mcp_servers WHERE id = $1 AND tenant_id = $2`)).
		WithArgs(int64(7), "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"url"}).AddRow(srv.URL))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp/oauth/initiate", strings.NewReader(`{"server_id":"7"}`)).
		WithContext(signedIn("tenant-a"))
	s.handleOAuthInitiate(rec, req)

	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "javascript:") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestCallbackBaseAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://gw.example.com/mcp/oauth/initiate", nil)

	for _, tc := range []struct {
		base            string
		managed, selfed bool
	}{
		{"https://gw.example.com", true, true},
		{"https://GW.example.com", true, true},
		{"https://app.example.com", false, true},
		{"https://attacker.example", false, true},
		{"HTTPS://gw.example.com", false, true},
		{"http://gw.example.com", false, true},
		{"https://gw.example.com/x", false, true},
		{"http://localhost:3000", false, true},
		{"javascript:alert(1)", false, false},
		{"https://user@gw.example.com", false, false},
		{"https://gw.example.com?x=1", false, false},
		{"https://gw.example.com#x", false, false},
		{"", false, false},
	} {
		restore := mcp.SetPublicEndpointsForTest(true)
		if got := callbackBaseAllowed(req, tc.base); got != tc.managed {
			t.Errorf("managed %q = %v, want %v", tc.base, got, tc.managed)
		}
		restore()
		restore = mcp.SetPublicEndpointsForTest(false)
		if got := callbackBaseAllowed(req, tc.base); got != tc.selfed {
			t.Errorf("self-hosted %q = %v, want %v", tc.base, got, tc.selfed)
		}
		restore()
	}
}

// A self-hosted gateway stamps its local tenant on anonymous requests too, so
// the tenant alone must not open an OAuth flow: only a signed-in user or an
// API key may. Refusal happens before the server row is even looked up.
func TestOAuthInitiate_RequiresSignedInCaller(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
	}{
		{"local tenant only", contextkeys.WithTenantID(context.Background(), "tenant-a")},
		{"auth flag without identity", contextkeys.WithTenantAuthenticated(contextkeys.WithTenantID(context.Background(), "tenant-a"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := oauthTestServer(t)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mcp/oauth/initiate", strings.NewReader(`{"server_id":"7"}`)).WithContext(tc.ctx)
			s.handleOAuthInitiate(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}

	// A signed-in member and an API key both get past the gate to the lookup.
	for name, ctx := range map[string]context.Context{
		"session": signedIn("tenant-a"),
		"api key": contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-a", "hash"),
	} {
		t.Run(name, func(t *testing.T) {
			s, mock := oauthTestServer(t)
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT url FROM mcp_servers WHERE id = $1 AND tenant_id = $2`)).
				WithArgs(int64(7), "tenant-a").
				WillReturnError(sql.ErrNoRows)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mcp/oauth/initiate", strings.NewReader(`{"server_id":"7"}`)).WithContext(ctx)
			s.handleOAuthInitiate(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The dashboard opens the popup without an opener link, so both callback pages
// must report on the same-origin channel, and any opener message they still
// send must be addressed to their own origin rather than '*'.
func TestOAuthCallbackPages_ReportOnSameOriginChannel(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOAuthCallbackError(rec, "nope")
	errPage := rec.Body.String()

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "a"})
	}))
	defer token.Close()
	s, mock := oauthTestServer(t)
	expectStateLookup(mock, token.URL)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE mcp_servers`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`FROM mcp_servers WHERE id = $1 AND tenant_id = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"name"}))
	okRec := callback(s, contextkeys.WithTenantID(context.Background(), "tenant-a"))
	if okRec.Code != http.StatusOK {
		t.Fatalf("callback status = %d: %s", okRec.Code, okRec.Body.String())
	}

	for name, page := range map[string]string{"error": errPage, "success": okRec.Body.String()} {
		if !strings.Contains(page, "new BroadcastChannel('"+oauthResultChannel+"')") {
			t.Errorf("%s page does not post on the result channel:\n%s", name, page)
		}
		if strings.Contains(page, "'*'") {
			t.Errorf("%s page still posts to any origin:\n%s", name, page)
		}
	}
}
