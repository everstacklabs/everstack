package sandbox

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// A managed instance's sandbox carries the instance id, but GitHub rows are
// keyed by the owning organization. The ownership check and app lookup must
// use the resolved organization, or the org's integration is invisible to
// its own sandboxes.
func TestSandboxGitHubLookupsUseResolvedOrganization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := &SandboxManager{db: sqlx.NewDb(db, "sqlmock")}
	m.SetGitHubTenantKeyResolver(func(_ context.Context, tenantID string) string {
		if tenantID == "inst-a" {
			return "org-a"
		}
		return tenantID
	})

	mock.ExpectQuery(regexp.QuoteMeta("WHERE installation_id = $1 AND tenant_id = $2 AND status = 'active'")).
		WithArgs(int64(42), "org-a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "installation_id"}).AddRow(1, "org-a", 42))
	if err := m.requireTenantInstallation(context.Background(), "inst-a", 42); err != nil {
		t.Fatalf("own org's installation from its instance: %v", err)
	}

	// Another org's instance resolves to that org and finds nothing.
	mock.ExpectQuery(regexp.QuoteMeta("WHERE installation_id = $1 AND tenant_id = $2 AND status = 'active'")).
		WithArgs(int64(42), "inst-b").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if err := m.requireTenantInstallation(context.Background(), "inst-b", 42); err == nil {
		t.Fatal("another org's instance reached org A's installation")
	}

	mock.ExpectQuery(regexp.QuoteMeta("FROM github_apps\n\t\tWHERE tenant_id = $1 AND status = 'active'")).
		WithArgs("org-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := m.resolveGitHubApp(context.Background(), "inst-a"); err == nil {
		t.Fatal("expected not-found from the empty app row set")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("lookups must be keyed by the resolved organization: %v", err)
	}
}
