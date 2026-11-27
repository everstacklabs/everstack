package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func newManifestTestHandler(t *testing.T) (*ManifestHandler, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewManifestHandler(NewStore(sqlx.NewDb(db, "sqlmock"))), mock
}

func memberOf(orgID string) context.Context {
	ctx := contextkeys.WithUserID(context.Background(), "user-1")
	ctx = contextkeys.WithTenantID(ctx, orgID)
	ctx = contextkeys.WithUserRole(ctx, "owner")
	return contextkeys.WithTenantAuthenticated(ctx)
}

// The callback installs the new app for the manifest session's tenant, so a
// caller-chosen tenant_id on Start would replace another tenant's GitHub App.
func TestManifestStartRefusesForeignOrAnonymousTenant(t *testing.T) {
	h, mock := newManifestTestHandler(t)

	cases := []struct {
		name string
		ctx  context.Context
		url  string
	}{
		{"anonymous with tenant_id", context.Background(), "/integrations/github/manifest/start?tenant_id=org-b"},
		{"anonymous with x-tenant-id only", context.Background(), "/integrations/github/manifest/start"},
		// The callback replaces the org's GitHub App credentials: owners and
		// admins only.
		{"viewer of own org", contextkeys.WithUserRole(memberOf("org-b"), "viewer"), "/integrations/github/manifest/start?tenant_id=org-b"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, tc.url, nil).WithContext(tc.ctx)
		r.Header.Set("x-tenant-id", "org-b")
		w := httptest.NewRecorder()
		h.Start(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", tc.name, w.Code)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("no manifest session may be created: %v", err)
	}
}

// The admin UI sends its own org id (github-integration.tsx); that keeps
// working and the session is created for it. A foreign tenant_id from a
// member of org A still binds the app to org A, never to org B.
func TestManifestStartOwnOrgCreatesSessionForPrincipal(t *testing.T) {
	h, mock := newManifestTestHandler(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO github_manifest_sessions")).
		WithArgs(sqlmock.AnyArg(), "org-a", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	r := httptest.NewRequest(http.MethodGet, "/integrations/github/manifest/start?tenant_id=org-a&return_to=/settings/integrations", nil).
		WithContext(memberOf("org-a"))
	w := httptest.NewRecorder()
	h.Start(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("own-org start: status = %d, body = %s", w.Code, w.Body.String())
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO github_manifest_sessions")).
		WithArgs(sqlmock.AnyArg(), "org-a", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	r = httptest.NewRequest(http.MethodGet, "/integrations/github/manifest/start?tenant_id=org-b", nil).
		WithContext(memberOf("org-a"))
	w = httptest.NewRecorder()
	h.Start(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("foreign-hint start: status = %d", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("manifest session must be bound to the principal's org: %v", err)
	}
}

func TestStoreQueriesAreTenantScoped(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewStore(sqlx.NewDb(db, "sqlmock"))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND status = 'active'")).
		WithArgs("org-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := s.GetAppByTenant(context.Background(), "org-a"); err != ErrGitHubAppNotFound {
		t.Fatalf("GetAppByTenant: err = %v, want not found", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("WHERE installation_id = $1 AND tenant_id = $2 AND status = 'active'")).
		WithArgs(int64(42), "org-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := s.GetInstallationForTenant(context.Background(), "org-a", 42); err == nil {
		t.Fatal("GetInstallationForTenant returned a row for another tenant")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("store must filter by tenant_id: %v", err)
	}
}

func TestInstanceOrgResolverMapsInstanceToOrgOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	resolve := NewInstanceOrgResolver(sqlx.NewDb(db, "sqlmock"))
	mock.ExpectQuery(regexp.QuoteMeta("FROM everstack.tenant_config WHERE instance_id::text = $1")).
		WithArgs("inst-a").WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow("org-a"))
	if got := resolve(context.Background(), "inst-a"); got != "org-a" {
		t.Fatalf("instance: got %q, want org-a", got)
	}
	// Not an instance (self-hosted org id): maps to itself.
	mock.ExpectQuery(regexp.QuoteMeta("FROM everstack.tenant_config")).
		WithArgs("org-z").WillReturnRows(sqlmock.NewRows([]string{"organization_id"}))
	if got := resolve(context.Background(), "org-z"); got != "org-z" {
		t.Fatalf("non-instance: got %q, want org-z", got)
	}
}
