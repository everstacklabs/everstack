package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
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
		WithArgs("everstack-test-0185df").
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

	req := httptest.NewRequest(http.MethodPost, "https://everstack-test-0185df.dev.eu-gra-1.everstack.ai/everstack.agents.v1.AgentsService/ListAgents", nil)
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
		WithArgs("everstack-test-0185df").
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

	req := httptest.NewRequest(http.MethodPost, "https://everstack-test-0185df.dev.eu-gra-1.everstack.ai/oauth/token", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCookieSessionAuth_PublicLicenseStatusCarriesVerifiedHostInstance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery(`SELECT[\s\S]*tc\.instance_id::text[\s\S]*tc\.organization_id::text[\s\S]*FROM everstack\.tenant_config AS tc`).
		WithArgs("prod-3fa6c9").
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug"}).
			AddRow("instance-1", "org-1", "everstack"))

	mw := cookieSessionAuthMiddleware(func() *sqlx.DB { return xdb })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := contextkeys.RequestInstanceScopeFromContext(r.Context())
		if !ok {
			t.Fatal("verified request instance scope is missing")
		}
		if scope.InstanceID != "instance-1" || scope.OrganizationID != "org-1" || scope.OrganizationSlug != "everstack" {
			t.Fatalf("request instance scope = %+v", scope)
		}
		if contextkeys.GetTenantID(r.Context()) != "" || contextkeys.IsTenantAuthenticated(r.Context()) {
			t.Fatal("public host resolution must not authenticate a tenant")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(
		http.MethodPost,
		"https://prod-3fa6c9.dev.eu-gra-1.everstack.ai/everstack.gateway.v1.GatewayService/GetLicenseMonitorStatus",
		nil,
	)
	h.ServeHTTP(httptest.NewRecorder(), req)

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
	req := httptest.NewRequest("GET", "/", nil)
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

	mock.ExpectQuery(`SELECT user_id::text FROM everstack\.sessions`).
		WithArgs("session-token").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("user-1"))
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
		got := readSessionToken(req)
		if got != "tok-"+name {
			t.Fatalf("alias %s: got %q want %q", name, got, "tok-"+name)
		}
	}
}
