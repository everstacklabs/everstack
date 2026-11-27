package middleware

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// EVS-309 review: the cookie re-authentication path used to answer with the
// user's first active instance (LIMIT 1, Host ignored). With a hostname
// selected instance in context it must answer for THAT instance only, and
// deny when the user is not a member of its organization.

func newSessionMock(t *testing.T) (*APIKeyConnectInterceptor, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rawDB.Close() })
	i := NewAPIKeyInterceptorWithSessionDB(false, nil, sqlx.NewDb(rawDB, "sqlmock"))
	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("cookie").
		WillReturnRows(sessionlivetest.LegacyRows("user-1", time.Now().Add(time.Hour)))
	return i, mock
}

func hostScopedCtx() context.Context {
	return contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-b", OrganizationID: "11111111-1111-1111-1111-111111111111", OrganizationSlug: "b",
	})
}

func TestLookupSession_HostScope_NonMemberIsDenied(t *testing.T) {
	i, mock := newSessionMock(t)
	mock.ExpectQuery(`FROM everstack\.organization_members om[\s\S]*om\.organization_id = \$2::uuid`).
		WithArgs("user-1", "11111111-1111-1111-1111-111111111111").
		WillReturnRows(sqlmock.NewRows([]string{"role"}))

	if auth := i.lookupSession(hostScopedCtx(), "cookie"); auth.valid {
		t.Fatalf("non-member of the host's org must be denied, got %+v", auth)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLookupSession_HostScope_MemberGetsHostInstanceOnly(t *testing.T) {
	i, mock := newSessionMock(t)
	mock.ExpectQuery(`FROM everstack\.organization_members om[\s\S]*om\.organization_id = \$2::uuid`).
		WithArgs("user-1", "11111111-1111-1111-1111-111111111111").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))

	auth := i.lookupSession(hostScopedCtx(), "cookie")
	if !auth.valid || auth.tenantID != "instance-b" || auth.role != "admin" {
		t.Fatalf("member must be scoped to the host instance, got %+v", auth)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The cache is keyed by host instance too: a hit for tenant A's host must
// not serve tenant B's host.
func TestLookupSession_CacheIsPerHostInstance(t *testing.T) {
	i, mock := newSessionMock(t)
	mock.ExpectQuery(`om\.organization_id = \$2::uuid`).
		WithArgs("user-1", "11111111-1111-1111-1111-111111111111").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))
	if auth := i.lookupSession(hostScopedCtx(), "cookie"); !auth.valid {
		t.Fatal("first lookup should succeed")
	}
	// Same cookie, different host: fresh session + membership lookup, denied.
	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("cookie").
		WillReturnRows(sessionlivetest.LegacyRows("user-1", time.Now().Add(time.Hour)))
	mock.ExpectQuery(`om\.organization_id = \$2::uuid`).
		WithArgs("user-1", "22222222-2222-2222-2222-222222222222").
		WillReturnRows(sqlmock.NewRows([]string{"role"}))
	otherHost := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-c", OrganizationID: "22222222-2222-2222-2222-222222222222", OrganizationSlug: "c",
	})
	if auth := i.lookupSession(otherHost, "cookie"); auth.valid {
		t.Fatalf("cached tenant A answer leaked to tenant C's host: %+v", auth)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A request the tenant middleware already refused for this host must not be
// re-authenticated from the same cookie.
func TestValidateAPIKey_SkipsCookieWhenTenantAuthRejected(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	i := NewAPIKeyInterceptorWithSessionDB(false, nil, sqlx.NewDb(rawDB, "sqlmock"))
	hdr := http.Header{}
	hdr.Set("Cookie", "es_everstack_session=cookie")

	ctx := contextkeys.WithTenantAuthRejected(context.Background())
	_, err = i.validateAPIKey(ctx, hdr, "/everstack.providers.v1.ProvidersService/GetConfigYAML", "corr")
	if err == nil {
		t.Fatal("rejected host session must not authenticate via the cookie branch")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
