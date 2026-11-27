package serve

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	"github.com/everstacklabs/everstack/internal/lib/tenantstatus"
)

// Host resolution keyed on the first DNS label only, so
// "acme-a1b2c3.attacker.example" resolved to the acme tenant whenever it was
// routed here. The rest of the Host must be the tenant's canonical host (the
// host of its stored gateway_url).
func TestResolveInstanceFromHost_RequiresCanonicalHost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	row := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug", "gateway_url"}).
			AddRow("instance-1", "org-1", "acme", "https://acme-a1b2c3.eu-gra-1.everstack.ai")
	}

	mock.ExpectQuery(tenantConfigLookup).WithArgs("acme-a1b2c3").WillReturnRows(row())
	scope, err := resolveInstanceFromHost(context.Background(), xdb, "ACME-a1b2c3.eu-gra-1.everstack.ai:443")
	if err != nil || scope.InstanceID != "instance-1" {
		t.Fatalf("canonical host: scope=%+v err=%v", scope, err)
	}

	mock.ExpectQuery(tenantConfigLookup).WithArgs("acme-a1b2c3").WillReturnRows(row())
	if _, err := resolveInstanceFromHost(context.Background(), xdb, "acme-a1b2c3.attacker.example"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("non-canonical host: err = %v, want sql.ErrNoRows", err)
	}
	// The miss is remembered for that exact host, without poisoning the
	// canonical spelling (no further query for the spoofed host).
	if _, err := resolveInstanceFromHost(context.Background(), xdb, "acme-a1b2c3.attacker.example"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cached non-canonical host: err = %v", err)
	}
	mock.ExpectQuery(tenantConfigLookup).WithArgs("acme-a1b2c3").WillReturnRows(row())
	if _, err := resolveInstanceFromHost(context.Background(), xdb, "acme-a1b2c3.eu-gra-1.everstack.ai"); err != nil {
		t.Fatalf("canonical host after a spoofed miss: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The paused-host refusal reveals the org's billing page only to a caller
// whose session is a member of the tenant's organization.
func TestPausedTenantViewerCheck(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	check := pausedTenantViewerCheck(func() *sqlx.DB { return xdb })
	tenant := tenantstatus.Tenant{InstanceID: "instance-1", OrganizationID: "11111111-1111-1111-1111-111111111111"}

	req := func(cookies ...*http.Cookie) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "https://acme-a1b2c3.eu-gra-1.everstack.ai/", nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}

	if check(req(), tenant) {
		t.Fatal("no session must not be a verified viewer")
	}
	if check(req(&http.Cookie{Name: "es_everstack_session", Value: "tok"},
		&http.Cookie{Name: authdomain.InstanceSignedOutCookie, Value: "1"}), tenant) {
		t.Fatal("a signed-out browser must not be a verified viewer")
	}

	mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("outsider").
		WillReturnRows(sessionlivetest.LegacyRows("00000000-0000-0000-0000-0000000000ff", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`FROM everstack\.organization_members`).
		WithArgs("00000000-0000-0000-0000-0000000000ff", tenant.OrganizationID).
		WillReturnError(sql.ErrNoRows)
	if check(req(&http.Cookie{Name: "es_everstack_session", Value: "outsider"}), tenant) {
		t.Fatal("a session outside the tenant's org must not be a verified viewer")
	}

	mock.ExpectQuery(sessionlivetest.QueryPattern).WithArgs("member").
		WillReturnRows(sessionlivetest.LegacyRows("00000000-0000-0000-0000-000000000009", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`FROM everstack\.organization_members`).
		WithArgs("00000000-0000-0000-0000-000000000009", tenant.OrganizationID).
		WillReturnRows(sqlmock.NewRows([]string{"ok"}).AddRow(1))
	if !check(req(&http.Cookie{Name: "es_everstack_session", Value: "member"}), tenant) {
		t.Fatal("a member's session must be a verified viewer")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A dotless Host ("acme-a1b2c3", reachable in-cluster) is its own label. Its
// canonical-host miss must not negatively cache the label, or the real
// tenant host is refused for the cache TTL.
func TestResolveInstanceFromHost_DotlessMissDoesNotCacheLabel(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	row := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"instance_id", "organization_id", "organization_slug", "gateway_url"}).
			AddRow("instance-1", "org-1", "acme", "https://acme-a1b2c3.eu-gra-1.everstack.ai")
	}
	for _, spoof := range []string{"acme-a1b2c3", "acme-a1b2c3:8089"} {
		mock.ExpectQuery(tenantConfigLookup).WithArgs("acme-a1b2c3").WillReturnRows(row())
		if _, err := resolveInstanceFromHost(context.Background(), xdb, spoof); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s: err = %v, want sql.ErrNoRows", spoof, err)
		}
		mock.ExpectQuery(tenantConfigLookup).WithArgs("acme-a1b2c3").WillReturnRows(row())
		if scope, err := resolveInstanceFromHost(context.Background(), xdb, "acme-a1b2c3.eu-gra-1.everstack.ai"); err != nil || scope.InstanceID != "instance-1" {
			t.Fatalf("after %s miss, canonical host: scope=%+v err=%v", spoof, scope, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
