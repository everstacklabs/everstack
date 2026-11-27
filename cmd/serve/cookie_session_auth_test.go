package serve

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/jmoiron/sqlx"
)

// TestCookieSessionAuth_NilDBSkipsEnrichment verifies the middleware is
// safe to install before the DB pool is opened — request goes through
// without any context mutation.
func TestCookieSessionAuth_NilDBSkipsEnrichment(t *testing.T) {
	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return nil })
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if uid := contextkeys.CloudUserIDFromContext(r.Context()); uid != "" {
			t.Errorf("expected no user_id with nil DB, got %q", uid)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "es_tenant_session", Value: "tok"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("downstream handler should still be invoked")
	}
}

// TestCookieSessionAuth_PublicAssetWithoutCookieIsNoOp confirms static assets
// do not pay for a platform lookup. SPA shells and public instance bootstrap
// RPCs intentionally do resolve the verified host even before authentication.
func TestCookieSessionAuth_PublicAssetWithoutCookieIsNoOp(t *testing.T) {
	mw := cookieSessionAuthMiddleware(func() *sqlx.DB {
		t.Fatal("middleware reached DB closure for a public asset")
		return nil
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestCookieSessionAuth_BearerCarriesVerifiedHostInstanceWithoutAuthenticating(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery(`SELECT[\s\S]*tc\.instance_id::text[\s\S]*tc\.organization_id::text[\s\S]*FROM everstack\.tenant_config AS tc`).
		WithArgs("acme-a1b2c3").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
			AddRow("instance-1", "org-1", "everstack"))

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := contextkeys.RequestInstanceScopeFromContext(r.Context())
		if !ok {
			t.Fatal("verified request instance scope is missing")
		}
		if scope.InstanceID != "instance-1" || scope.OrganizationID != "org-1" {
			t.Fatalf("request instance scope = %+v", scope)
		}
		if scope.OrganizationSlug != "everstack" {
			t.Fatalf("request organization slug = %q, want everstack", scope.OrganizationSlug)
		}
		if contextkeys.GetTenantID(r.Context()) != "" {
			t.Fatal("host resolution alone must not authenticate a tenant")
		}
		if contextkeys.IsTenantAuthenticated(r.Context()) {
			t.Fatal("host resolution alone marked the request authenticated")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://acme-a1b2c3.dev.eu-gra-1.everstack.ai/everstack.agents.v1.AgentsService/ListAgents", nil)
	req.Header.Set("Authorization", "Bearer legacy-cli-token")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCookieSessionAuth_OAuthTokenExchangeCarriesVerifiedHostInstance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery(`SELECT[\s\S]*tc\.instance_id::text[\s\S]*tc\.organization_id::text[\s\S]*FROM everstack\.tenant_config AS tc`).
		WithArgs("acme-a1b2c3").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
			AddRow("instance-1", "org-1", "everstack"))

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := contextkeys.RequestInstanceScopeFromContext(r.Context())
		if !ok || scope.InstanceID != "instance-1" || scope.OrganizationID != "org-1" {
			t.Fatalf("request instance scope = %+v, present=%t", scope, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://acme-a1b2c3.dev.eu-gra-1.everstack.ai/oauth/token", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// EVS-309: the pre-auth status RPCs have no legitimate consumer of a
// hostname-selected instance, so an anonymous caller must not even trigger
// the lookup. The panicking DB closure proves the host is never resolved.
func TestCookieSessionAuth_PublicStatusRPCsDoNotResolveHost(t *testing.T) {
	for _, path := range []string{
		"/everstack.gateway.v1.GatewayService/GetLicenseMonitorStatus",
		"/everstack.gateway.v1.GatewayService/GetGatewayInstanceStatus",
		"/everstack.gateway.v1.GatewayService/GetTrialStatus",
		"/everstack.gateway.v1.GatewayService/GetGateway",
		"/everstack.config.v1.ConfigService/GetRuntimeConfigSection",
	} {
		t.Run(path, func(t *testing.T) {
			mw := cookieSessionAuthMiddleware(func() *sqlx.DB {
				t.Fatalf("%s: anonymous status RPC must not resolve the host", path)
				return nil
			})
			called := false
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if _, ok := contextkeys.RequestInstanceScopeFromContext(r.Context()); ok {
					t.Fatal("anonymous status RPC must not carry a host-selected instance scope")
				}
				if contextkeys.GetTenantID(r.Context()) != "" || contextkeys.IsTenantAuthenticated(r.Context()) {
					t.Fatal("anonymous status RPC must not carry tenant identity")
				}
			}))
			req := httptest.NewRequest(http.MethodPost, "https://prod-a1b2c3.dev.eu-gra-1.everstack.ai"+path, nil)
			h.ServeHTTP(httptest.NewRecorder(), req)
			if !called {
				t.Fatal("handler not reached")
			}
		})
	}
}

// EVS-309: a valid cloud session whose user is NOT a member of the host's
// organization must not be handed the victim's instance scope (consumers
// such as GetSession synthesis and GetLicenseMonitorStatus read it), nor
// be marked authenticated on that host. Host resolved + membership failed
// fails closed.
func TestCookieSessionAuth_NonMemberCookieUserGetsNoHostScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("cookie-token").
		WillReturnRows(sessionlivetest.LegacyRows("00000000-0000-0000-0000-000000000009", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`SELECT[\s\S]*tc\.instance_id::text[\s\S]*FROM everstack\.tenant_config AS tc`).
		WithArgs("prod-a1b2c3").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
			AddRow("instance-1", "org-1", "everstack"))
	mock.ExpectQuery(`SELECT COALESCE\(role, ''\) FROM everstack\.organization_members`).
		WithArgs("00000000-0000-0000-0000-000000000009", "org-1").
		WillReturnRows(sqlmock.NewRows([]string{"role"}))

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ctx := r.Context()
		if _, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok {
			t.Fatal("non-member must not receive the host's instance scope")
		}
		if contextkeys.IsTenantAuthenticated(ctx) || contextkeys.GetTenantID(ctx) != "" || contextkeys.CloudUserIDFromContext(ctx) != "" {
			t.Fatalf("non-member on a resolved host must fail closed, got tenant=%q authed=%v user=%q",
				contextkeys.GetTenantID(ctx), contextkeys.IsTenantAuthenticated(ctx), contextkeys.CloudUserIDFromContext(ctx))
		}
	}))
	req := httptest.NewRequest(http.MethodPost, "https://prod-a1b2c3.dev.eu-gra-1.everstack.ai/everstack.agents.v1.AgentsService/ListAgents", nil)
	req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "cookie-token"})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !called {
		t.Fatal("handler not reached")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestCookieSessionAuth_SignedOutMarkerSkipsEnrichment covers the half of
// instance sign-out that the SPA can't do for itself: with the cloud's
// parent-domain cookie still in the jar, a signed-out browser must get no
// identity at all, or the RPCs behind the redirect keep succeeding and the
// sign-out is skin deep. The panicking DB closure proves we never even look
// the cookie up.
func TestCookieSessionAuth_SignedOutMarkerSkipsEnrichment(t *testing.T) {
	mw := cookieSessionAuthMiddleware(func() *sqlx.DB {
		t.Fatal("middleware reached DB closure despite signed-out marker")
		return nil
	})
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if uid := contextkeys.CloudUserIDFromContext(r.Context()); uid != "" {
			t.Errorf("signed-out request was enriched with user_id %q", uid)
		}
		w.WriteHeader(http.StatusOK)
	}))
	// An API call, not a shell load: a load of the SPA shell resolves the
	// host (no identity) so the signed-out visit can be recorded
	// anonymously; see spa_analytics_env_test.go.
	req := httptest.NewRequest("POST", "/everstack.agents.v1.AgentsService/ListAgents", nil)
	req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "cloud-tok"})
	req.AddCookie(&http.Cookie{Name: authdomain.InstanceSignedOutCookie, Value: "1"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("downstream handler should still be invoked")
	}
}

// TestCookieSessionAuth_AlreadySetIsNoOp confirms we don't overwrite an
// upstream cloud_user_id assignment.
func TestCookieSessionAuth_AlreadySetIsNoOp(t *testing.T) {
	mw := cookieSessionAuthMiddleware(func() *sqlx.DB {
		t.Fatal("middleware reached DB closure despite cloud_user_id already set")
		return nil
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uid := contextkeys.CloudUserIDFromContext(r.Context()); uid != "preset-uid" {
			t.Errorf("expected preserved uid, got %q", uid)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(contextkeys.WithCloudUserID(req.Context(), "preset-uid"))
	req.AddCookie(&http.Cookie{Name: "es_tenant_session", Value: "tok"})
	// Provide an actual sqlx.DB pointer so dbFn() != nil; we expect the
	// middleware to NOT call into it because cloud_user_id is preset.
	// Wait — the closure above panics on entry. So if dbFn is called at
	// all, the test fails — exactly what we want for this branch.
	h.ServeHTTP(httptest.NewRecorder(), req)
}

// TestCookieSessionAuth_SetsTenantAuthenticatedFlag pins the contract
// that downstream code relies on: when a cookie resolves to a user_id,
// the request is also marked tenant-authenticated so the API-key
// interceptor accepts it. Forgetting this turns every cookie-only RPC
// into "Reconnecting to sandbox..." in the FE.
func TestCookieSessionAuth_SetsTenantAuthenticatedFlag(t *testing.T) {
	// Build a stub middleware that bypasses the DB call and stamps a
	// preset user id; the real DB lookup is exercised in integration
	// tests, this unit test just guards the context-mutation contract.
	stamp := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := contextkeys.WithCloudUserID(r.Context(), "u-1")
			ctx = contextkeys.WithTenantAuthenticated(ctx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	called := false
	h := stamp(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if uid := contextkeys.CloudUserIDFromContext(r.Context()); uid != "u-1" {
			t.Errorf("cloud user id not propagated, got %q", uid)
		}
		if !contextkeys.IsTenantAuthenticated(r.Context()) {
			t.Errorf("tenant-authenticated flag not set — API-key interceptor will reject the request")
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("downstream handler should be invoked")
	}
}

func TestCookieSessionAuth_PreservesVerifiedMembershipRole(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("session-token").
		WillReturnRows(sessionlivetest.LegacyRows("user-1", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`SELECT organization_id::text AS organization_id, COALESCE\(role, ''\) AS role[\s\S]*FROM everstack\.organization_members`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "role"}).AddRow("tenant-1", "admin"))

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contextkeys.CloudUserIDFromContext(r.Context()); got != "user-1" {
			t.Errorf("cloud user id = %q, want user-1", got)
		}
		if got := contextkeys.GetTenantID(r.Context()); got != "tenant-1" {
			t.Errorf("tenant id = %q, want tenant-1", got)
		}
		if got := contextkeys.GetUserRole(r.Context()); got != "admin" {
			t.Errorf("user role = %q, want admin", got)
		}
		if !contextkeys.IsTenantAuthenticated(r.Context()) {
			t.Error("tenant-authenticated marker was not set")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "" // Exercise self-hosted membership resolution, not tenant_config.
	req.AddCookie(&http.Cookie{Name: "es_tenant_session", Value: "session-token"})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestPickActiveOrg_Policy locks the post-P0 contract for tenant
// resolution under cookie auth: never silently pick a tenant the
// browser didn't ask for. The cases here mirror what the membership
// query + `x-org-id` header combinations look like in production.
func TestPickActiveOrg_Policy(t *testing.T) {
	cases := []struct {
		name         string
		memberships  []string
		headerOrg    string
		wantOrg      string
		wantResolved bool
	}{
		{
			name:         "no memberships → fail closed",
			memberships:  nil,
			headerOrg:    "",
			wantResolved: false,
		},
		{
			name:         "no memberships, header present → still fail closed",
			memberships:  nil,
			headerOrg:    "org-a",
			wantResolved: false,
		},
		{
			name:         "single membership, no header → auto-resolve",
			memberships:  []string{"org-a"},
			headerOrg:    "",
			wantOrg:      "org-a",
			wantResolved: true,
		},
		{
			name:         "single membership, whitespace header → treated as empty",
			memberships:  []string{"org-a"},
			headerOrg:    "   ",
			wantOrg:      "org-a",
			wantResolved: true,
		},
		{
			name:         "single membership, matching header → use header",
			memberships:  []string{"org-a"},
			headerOrg:    "org-a",
			wantOrg:      "org-a",
			wantResolved: true,
		},
		{
			name:         "single membership, mismatched header → fail closed",
			memberships:  []string{"org-a"},
			headerOrg:    "org-b",
			wantResolved: false,
		},
		{
			name:         "multi membership, no header → fail closed (no joined_at fallback)",
			memberships:  []string{"org-a", "org-b"},
			headerOrg:    "",
			wantResolved: false,
		},
		{
			name:         "multi membership, header matches → use matched",
			memberships:  []string{"org-a", "org-b"},
			headerOrg:    "org-b",
			wantOrg:      "org-b",
			wantResolved: true,
		},
		{
			name:         "multi membership, header doesn't match → fail closed",
			memberships:  []string{"org-a", "org-b"},
			headerOrg:    "org-c",
			wantResolved: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOrg, gotOK := pickActiveOrg(tc.memberships, tc.headerOrg)
			if gotOK != tc.wantResolved {
				t.Fatalf("resolved: got %v, want %v (org=%q)", gotOK, tc.wantResolved, gotOrg)
			}
			if gotOK && gotOrg != tc.wantOrg {
				t.Fatalf("org: got %q, want %q", gotOrg, tc.wantOrg)
			}
			if !gotOK && gotOrg != "" {
				t.Fatalf("expected empty org when not resolved, got %q", gotOrg)
			}
		})
	}
}

func TestReadSessionToken_ChecksAllAliases(t *testing.T) {
	for _, name := range cookieSessionCookieAliases {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: name, Value: "tok-" + name})
		got := readSessionTokens(req)
		if len(got) != 1 || got[0] != "tok-"+name {
			t.Fatalf("alias %s: got %q want [%q]", name, got, "tok-"+name)
		}
	}
}

// ---- Review finding 1: unresolved Host on a managed gateway ---------------

// On a managed gateway every request belongs to a hostname-selected tenant.
// A Host that resolves to nothing (here: an unknown label, sent uppercased
// with a trailing dot to exercise normalization) must not drop the request
// into the self-hosted membership fallback, and must never mark it
// authenticated: that marker alone skips API-key and license validation.
func TestCookieSessionAuth_ManagedGateway_UnresolvedHostFailsClosed(t *testing.T) {
	enterprise.SetManagedGateway(true)
	t.Cleanup(func() { enterprise.SetManagedGateway(false) })

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("cookie-token").
		WillReturnRows(sessionlivetest.LegacyRows("00000000-0000-0000-0000-000000000009", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).
		WithArgs("victim-123456").
		WillReturnError(sql.ErrNoRows)
	// No membership fallback query may follow.

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ctx := r.Context()
		if contextkeys.IsTenantAuthenticated(ctx) || contextkeys.GetTenantID(ctx) != "" || contextkeys.CloudUserIDFromContext(ctx) != "" {
			t.Fatalf("unresolved host on a managed gateway must fail closed, got tenant=%q authed=%v user=%q",
				contextkeys.GetTenantID(ctx), contextkeys.IsTenantAuthenticated(ctx), contextkeys.CloudUserIDFromContext(ctx))
		}
	}))
	req := httptest.NewRequest(http.MethodPost, "https://victim-123456.example.com/everstack.providers.v1.ProvidersService/GetConfigYAML", nil)
	req.Host = "VICTIM-123456.Example.com.:443"
	req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "cookie-token"})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !called {
		t.Fatal("handler not reached")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The same uppercased/ported/dotted spelling of a REAL tenant host resolves
// to that tenant for a verified member.
func TestCookieSessionAuth_HostSpellingNormalizedBeforeLookup(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("cookie-token").
		WillReturnRows(sessionlivetest.LegacyRows("00000000-0000-0000-0000-000000000009", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).
		WithArgs("prod-a1b2c3").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
			AddRow("instance-1", "org-1", "everstack"))
	mock.ExpectQuery(`SELECT COALESCE\(role, ''\) FROM everstack\.organization_members`).
		WithArgs("00000000-0000-0000-0000-000000000009", "org-1").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contextkeys.GetTenantID(r.Context()) != "instance-1" || !contextkeys.IsTenantAuthenticated(r.Context()) {
			t.Fatalf("member on a normalized host spelling must resolve, got tenant=%q", contextkeys.GetTenantID(r.Context()))
		}
	}))
	req := httptest.NewRequest(http.MethodPost, "https://prod-a1b2c3.dev.eu-gra-1.everstack.ai/everstack.agents.v1.AgentsService/ListAgents", nil)
	req.Host = "PROD-A1B2C3.dev.eu-gra-1.everstack.ai.:443"
	req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "cookie-token"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Self-hosted (no tenant_config): a valid cookie for a user with zero
// memberships must not be marked authenticated. Previously CloudUserID and
// TenantAuthenticated were set with tenantOK=false, which let the request
// skip API-key validation on handlers with no tenant check.
func TestCookieSessionAuth_SelfHosted_ZeroMembershipsNotAuthenticated(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("cookie-token").
		WillReturnRows(sessionlivetest.LegacyRows("00000000-0000-0000-0000-000000000009", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).
		WithArgs("gateway").
		WillReturnError(errors.New(`relation "everstack.tenant_config" does not exist`))
	mock.ExpectQuery(`SELECT organization_id::text AS organization_id`).
		WithArgs("00000000-0000-0000-0000-000000000009").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "role"}))

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ctx := r.Context()
		if contextkeys.IsTenantAuthenticated(ctx) || contextkeys.CloudUserIDFromContext(ctx) != "" {
			t.Fatalf("zero-membership user must not be authenticated, got authed=%v user=%q",
				contextkeys.IsTenantAuthenticated(ctx), contextkeys.CloudUserIDFromContext(ctx))
		}
	}))
	req := httptest.NewRequest(http.MethodPost, "https://gateway.internal/everstack.providers.v1.ProvidersService/GetConfigYAML", nil)
	req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "cookie-token"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("handler not reached")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ---- Review: caller-controlled cookies must not skip host rejection -------

// A signed-out marker plus a session cookie used to return before any host
// resolution, leaving the API-key interceptor free to re-authenticate the
// session cookie from its cache. The signed-out browser must instead be
// marked rejected so no downstream cookie branch recognises it.
func TestCookieSessionAuth_SignedOutMarkerStampsRejection(t *testing.T) {
	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { t.Fatal("must not touch the DB"); return nil })
	var rejected bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rejected = contextkeys.IsTenantAuthRejected(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "https://prod-a1b2c3.dev.eu-gra-1.everstack.ai/v1/openai-agents/capabilities", nil)
	req.AddCookie(&http.Cookie{Name: authdomain.InstanceSignedOutCookie, Value: "1"})
	req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "S"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !rejected {
		t.Fatal("signed-out browser with a session cookie must be marked rejected")
	}
}

// An invalid higher-priority alias must not shadow a valid session, and a
// fully invalid set of aliases must be marked rejected rather than left for
// the interceptor's cached cookie branch.
func TestCookieSessionAuth_AliasesAllTriedThenRejected(t *testing.T) {
	t.Run("invalid alias then valid member", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		xdb := sqlx.NewDb(db, "sqlmock")
		mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("bad").WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("good").
			WillReturnRows(sessionlivetest.LegacyRows("00000000-0000-0000-0000-000000000009", time.Now().Add(time.Hour)))
		mock.ExpectQuery(`FROM everstack\.tenant_config AS tc`).WithArgs("prod-a1b2c3").
			WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).AddRow("instance-1", "org-1", "everstack"))
		mock.ExpectQuery(`SELECT COALESCE\(role, ''\) FROM everstack\.organization_members`).
			WithArgs("00000000-0000-0000-0000-000000000009", "org-1").
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("member"))
		mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if contextkeys.GetTenantID(r.Context()) != "instance-1" {
				t.Fatalf("valid second alias must authenticate, got tenant %q", contextkeys.GetTenantID(r.Context()))
			}
		}))
		req := httptest.NewRequest(http.MethodGet, "https://prod-a1b2c3.dev.eu-gra-1.everstack.ai/v1/openai-agents/capabilities", nil)
		req.AddCookie(&http.Cookie{Name: "es_tenant_session", Value: "bad"})
		req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "good"})
		h.ServeHTTP(httptest.NewRecorder(), req)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("all aliases invalid", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		xdb := sqlx.NewDb(db, "sqlmock")
		mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("bad").WillReturnError(sql.ErrNoRows)
		mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
		var rejected bool
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rejected = contextkeys.IsTenantAuthRejected(r.Context()) }))
		req := httptest.NewRequest(http.MethodGet, "https://prod-a1b2c3.dev.eu-gra-1.everstack.ai/v1/openai-agents/capabilities", nil)
		req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "bad"})
		h.ServeHTTP(httptest.NewRecorder(), req)
		if !rejected {
			t.Fatal("presented-but-invalid session must be marked rejected")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

// The instance-token exchange binds the token to the host-resolved instance,
// so the scope must reach the handler whatever cookies the browser carries:
// the parent-domain cloud cookie (arriving from the cloud login), the
// signed-out marker (re-entering after sign-out) or a dead session. None of
// them may add cookie identity to the request.
func TestCookieSessionAuth_InstanceTokenExchangeResolvesHostDespiteCookies(t *testing.T) {
	cookieSets := map[string][]*http.Cookie{
		"no cookies":         nil,
		"cloud cookie":       {{Name: "es_everstack_session", Value: "cloud-tok"}},
		"signed-out marker":  {{Name: "es_everstack_session", Value: "cloud-tok"}, {Name: authdomain.InstanceSignedOutCookie, Value: "1"}},
		"stale local cookie": {{Name: "everstack_session", Value: "expired"}},
	}
	for _, path := range []string{"/api/auth/exchange?token=x", "/auth/cloud-callback?code=x"} {
		for name, cookies := range cookieSets {
			t.Run(path+"/"+name, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				xdb := sqlx.NewDb(db, "sqlmock")
				mock.ExpectQuery(`SELECT[\s\S]*tc\.instance_id::text[\s\S]*FROM everstack\.tenant_config AS tc`).
					WithArgs("acme-a1b2c3").
					WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
						AddRow("instance-a", "org-a", "acme"))

				called := false
				h := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					scope, ok := contextkeys.RequestInstanceScopeFromContext(r.Context())
					if !ok || scope.InstanceID != "instance-a" || scope.OrganizationID != "org-a" {
						t.Errorf("request instance scope = %+v, present=%t", scope, ok)
					}
					if uid := contextkeys.CloudUserIDFromContext(r.Context()); uid != "" {
						t.Errorf("exchange request carries cookie identity %q", uid)
					}
					if contextkeys.IsTenantAuthenticated(r.Context()) {
						t.Error("host resolution marked the exchange request authenticated")
					}
				}))
				req := httptest.NewRequest(http.MethodGet, "https://acme-a1b2c3.eu-gra-1.everstack.ai"+path, nil)
				for _, c := range cookies {
					req.AddCookie(c)
				}
				h.ServeHTTP(httptest.NewRecorder(), req)
				if !called {
					t.Fatal("downstream handler not invoked")
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
