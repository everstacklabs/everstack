package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/github"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

const (
	ghOrgA = "11111111-1111-1111-1111-111111111111"
	ghOrgB = "22222222-2222-2222-2222-222222222222"
)

var ghInstallationColumns = []string{
	"id", "tenant_id", "installation_id", "account_login", "account_type", "app_id",
	"permissions", "repository_selection", "status", "installed_by", "created_at", "updated_at",
}

func newGitHubTestServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Server{}
	s.SetGitHubApp(nil, github.NewStore(sqlx.NewDb(db, "sqlmock")))
	return s, mock
}

// sessionPrincipal mirrors what the session/API-key middleware installs for
// a signed-in admin of orgID.
func sessionPrincipal(orgID string) context.Context {
	ctx := contextkeys.WithUserID(context.Background(), "user-1")
	ctx = contextkeys.WithTenantID(ctx, orgID)
	ctx = contextkeys.WithUserRole(ctx, "admin")
	return contextkeys.WithTenantAuthenticated(ctx)
}

// The admin UI's real request shape (apps/admin/src/server/github.ts): body
// tenantId and x-tenant-id header both carry the caller's own org id. It must
// keep working, and the list must be filtered to that org in SQL.
func TestListGitHubInstallationsOwnOrgIsFilteredByTenant(t *testing.T) {
	s, mock := newGitHubTestServer(t)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND status = 'active'")).
		WithArgs(ghOrgA).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns).
			AddRow(1, ghOrgA, 42, "acme", "Organization", 7, []byte(`{}`), "all", "active", nil, now, now))

	req := connect.NewRequest(&agentspb.ListGitHubInstallationsRequest{TenantId: ghOrgA})
	req.Header().Set("x-tenant-id", ghOrgA)
	resp, err := s.ListGitHubInstallations(sessionPrincipal(ghOrgA), req)
	if err != nil {
		t.Fatalf("own-org list failed: %v", err)
	}
	if got := len(resp.Msg.Installations); got != 1 {
		t.Fatalf("installations = %d, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("list must be scoped by tenant_id in SQL: %v", err)
	}
}

// A member of org A naming org B in the body or header never reaches org B:
// the hint is ignored and every query runs scoped to org A.
func TestGitHubRPCsIgnoreAnotherTenantsID(t *testing.T) {
	s, mock := newGitHubTestServer(t)
	ctx := sessionPrincipal(ghOrgA)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND status = 'active'")).
		WithArgs(ghOrgA).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns))
	listReq := connect.NewRequest(&agentspb.ListGitHubInstallationsRequest{TenantId: ghOrgB})
	listReq.Header().Set("x-tenant-id", ghOrgB)
	if _, err := s.ListGitHubInstallations(ctx, listReq); err != nil {
		t.Fatalf("list: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("WHERE installation_id = $1 AND tenant_id = $2 AND status = 'active'")).
		WithArgs(int64(42), ghOrgA).
		WillReturnResult(sqlmock.NewResult(0, 0))
	removeReq := connect.NewRequest(&agentspb.RemoveGitHubInstallationRequest{TenantId: ghOrgB, InstallationId: 42})
	if _, err := s.RemoveGitHubInstallation(ctx, removeReq); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("remove of org B's installation: err = %v, want NotFound", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("WHERE installation_id = $1 AND tenant_id = $2 AND status = 'active'")).
		WithArgs(int64(42), ghOrgA).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns))
	reposReq := connect.NewRequest(&agentspb.ListGitHubRepositoriesRequest{TenantId: ghOrgB, InstallationId: 42})
	if _, err := s.ListGitHubRepositories(ctx, reposReq); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("repos of org B's installation: err = %v, want NotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("queries must be scoped to the principal's org: %v", err)
	}
}

// No authenticated principal: a bare x-tenant-id header is not a credential.
func TestGitHubRPCsRequireAuthenticatedPrincipal(t *testing.T) {
	s, _ := newGitHubTestServer(t)
	req := connect.NewRequest(&agentspb.ListGitHubInstallationsRequest{TenantId: ghOrgA})
	req.Header().Set("x-tenant-id", ghOrgA)
	if _, err := s.ListGitHubInstallations(context.Background(), req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unauthenticated list: err = %v, want PermissionDenied", err)
	}
}

// Linking only claims an unclaimed (pending) installation or one already
// ours; the UPDATE carries that guard, so another tenant's installation id
// cannot be re-pointed.
func TestLinkGitHubInstallationNeverRepointsAnotherTenants(t *testing.T) {
	s, mock := newGitHubTestServer(t)
	mock.ExpectQuery(regexp.QuoteMeta("AND (tenant_id = $2 OR (tenant_id = '' AND $3))")).
		WithArgs(int64(42), ghOrgA, true).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns)) // owned by someone else: no row

	req := connect.NewRequest(&agentspb.LinkGitHubInstallationRequest{TenantId: ghOrgA, InstallationId: 42})
	if _, err := s.LinkGitHubInstallation(sessionPrincipal(ghOrgA), req); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("link of foreign installation: err = %v, want NotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// instancePrincipal mirrors the gateway's cookie auth on a managed instance
// host (cmd/serve/cookie_session_auth.go): the tenant is the instance id and
// the verified organization rides in the request instance scope. No tenant
// config is installed on that path.
func instancePrincipal(instanceID, orgID string) context.Context {
	ctx := contextkeys.WithCloudUserID(context.Background(), "user-1")
	ctx = contextkeys.WithTenantID(ctx, instanceID)
	ctx = contextkeys.WithUserRole(ctx, "admin")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	return contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{
		InstanceID: instanceID, OrganizationID: orgID, OrganizationSlug: "slug",
	})
}

const (
	ghInstanceA = "33333333-3333-3333-3333-333333333333"
	ghInstanceB = "55555555-5555-5555-5555-555555555555"
)

// Existing rows are keyed by organization id. From an instance of that org
// the canonical key is the org, whatever the UI sends.
func TestAuthorizedTenantIDManagedInstanceMapsToOrg(t *testing.T) {
	ctx := instancePrincipal(ghInstanceA, ghOrgA)
	for _, requested := range []string{"", ghOrgA, ghOrgB} {
		got, err := github.AuthorizedTenantID(ctx, requested)
		if err != nil || got != ghOrgA {
			t.Fatalf("requested %q: got %q, %v; want %q", requested, got, err, ghOrgA)
		}
	}
	// The controlplane tenant-middleware shape resolves the same way.
	tcCtx := contextkeys.WithTenantAuthenticated(contextkeys.WithTenantID(
		contextkeys.WithCloudUserID(context.Background(), "user-1"), ghInstanceA))
	tcCtx = tenant.WithConfig(tcCtx, &tenant.Config{InstanceID: ghInstanceA, OrganizationID: ghOrgA})
	if got, err := github.AuthorizedTenantID(tcCtx, ""); err != nil || got != ghOrgA {
		t.Fatalf("tenant-config shape: got %q, %v", got, err)
	}
}

// An installation written before this change (tenant_id = org A) stays
// visible and usable from an instance of org A, and an instance of org B
// only ever queries org B.
func TestOrgKeyedInstallationVisibleFromOwnInstanceOnly(t *testing.T) {
	s, mock := newGitHubTestServer(t)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND status = 'active'")).
		WithArgs(ghOrgA).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns).
			AddRow(1, ghOrgA, 42, "acme", "Organization", 7, []byte(`{}`), "all", "active", nil, now, now))
	req := connect.NewRequest(&agentspb.ListGitHubInstallationsRequest{TenantId: ghOrgA})
	req.Header().Set("x-tenant-id", ghOrgA)
	resp, err := s.ListGitHubInstallations(instancePrincipal(ghInstanceA, ghOrgA), req)
	if err != nil || len(resp.Msg.Installations) != 1 {
		t.Fatalf("own org's installation from its instance: resp=%v err=%v", resp, err)
	}

	// Relinking the pre-existing row from the instance claims it for org A.
	mock.ExpectQuery(regexp.QuoteMeta("AND (tenant_id = $2 OR (tenant_id = '' AND $3))")).
		WithArgs(int64(42), ghOrgA, true).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns).
			AddRow(1, ghOrgA, 42, "acme", "Organization", 7, []byte(`{}`), "all", "active", nil, now, now))
	link := connect.NewRequest(&agentspb.LinkGitHubInstallationRequest{TenantId: ghOrgA, InstallationId: 42})
	if _, err := s.LinkGitHubInstallation(instancePrincipal(ghInstanceA, ghOrgA), link); err != nil {
		t.Fatalf("relink from own instance: %v", err)
	}

	// An instance of org B asking for org A's rows queries org B only.
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND status = 'active'")).
		WithArgs(ghOrgB).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns))
	other := connect.NewRequest(&agentspb.ListGitHubInstallationsRequest{TenantId: ghOrgA})
	other.Header().Set("x-tenant-id", ghOrgA)
	resp, err = s.ListGitHubInstallations(instancePrincipal(ghInstanceB, ghOrgB), other)
	if err != nil || len(resp.Msg.Installations) != 0 {
		t.Fatalf("org B's instance saw org A's installation: resp=%v err=%v", resp, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// GET /v1/integrations/github/tree used to take the tenant from x-tenant-id
// alone.
func TestGitHubRepoTreeRefusesForeignTenantHeader(t *testing.T) {
	s, mock := newGitHubTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/v1/integrations/github/tree?installation_id=42&owner=acme&repo=secret", nil)
	r.Header.Set("x-tenant-id", ghOrgB)
	r = r.WithContext(sessionPrincipal(ghOrgA))
	w := httptest.NewRecorder()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE installation_id = $1 AND tenant_id = $2 AND status = 'active'")).
		WithArgs(int64(42), ghOrgA).
		WillReturnRows(sqlmock.NewRows(ghInstallationColumns))
	s.handleListGitHubRepoTree(w, r, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("tree for org B's installation: status = %d, want 404 (scoped to org A)", w.Code)
	}

	// Anonymous caller with a chosen header: refused too.
	r = httptest.NewRequest(http.MethodGet, "/v1/integrations/github/tree?installation_id=42&owner=acme&repo=secret", nil)
	r.Header.Set("x-tenant-id", ghOrgA)
	w = httptest.NewRecorder()
	s.handleListGitHubRepoTree(w, r, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("anonymous tree: status = %d, want 403", w.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Linking and removing change the organization's GitHub integration, so a
// viewer (or an API key, which carries no role) is refused before any store
// access; listing stays open to every member.
func TestGitHubMutationsRequireOrgAdmin(t *testing.T) {
	viewer := contextkeys.WithUserRole(sessionPrincipal(ghOrgA), "viewer")
	apiKey := contextkeys.WithAuthenticatedAPIKey(context.Background(), ghOrgA, "hash")
	for name, ctx := range map[string]context.Context{"viewer": viewer, "api key": apiKey} {
		t.Run(name, func(t *testing.T) {
			s, mock := newGitHubTestServer(t)
			_, err := s.RemoveGitHubInstallation(ctx, connect.NewRequest(&agentspb.RemoveGitHubInstallationRequest{InstallationId: 42}))
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("remove: err = %v, want PermissionDenied", err)
			}
			_, err = s.LinkGitHubInstallation(ctx, connect.NewRequest(&agentspb.LinkGitHubInstallationRequest{InstallationId: 42}))
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("link: err = %v, want PermissionDenied", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// On a gateway serving several tenants a pending (tenant-less) installation is
// never claimable: nothing ties it to the tenant that installed it, and
// installation ids are sequential.
func TestClaimRefusesUnownedInstallationOnSharedGateway(t *testing.T) {
	s, mock := newGitHubTestServer(t)
	s.githubStore.SetSharedGateway(true)
	mock.ExpectQuery(regexp.QuoteMeta("AND (tenant_id = $2 OR (tenant_id = '' AND $3))")).
		WithArgs(int64(42), ghOrgA, false).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err := s.LinkGitHubInstallation(sessionPrincipal(ghOrgA), connect.NewRequest(&agentspb.LinkGitHubInstallationRequest{InstallationId: 42}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
