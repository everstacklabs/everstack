package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// EVS-309 review: HTTP twin of the gRPC host-scoped session tests.

func newHTTPSessionMock(t *testing.T) (*APIKeyInterceptor, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rawDB.Close() })
	i := NewAPIKeyInterceptor(false)
	i.sessionDB = sqlx.NewDb(rawDB, "sqlmock")
	i.cacheTTL = time.Minute
	return i, mock
}

func expectSessionRow(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(sessionlivetest.QueryPattern).
		WithArgs("cookie").
		WillReturnRows(sessionlivetest.LegacyRows("user-1", time.Now().Add(time.Hour)))
}

func httpHostScopedCtx() context.Context {
	return contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "instance-b", OrganizationID: "11111111-1111-1111-1111-111111111111", OrganizationSlug: "b",
	})
}

func TestResolveSession_HostScope_NonMemberIsDenied(t *testing.T) {
	i, mock := newHTTPSessionMock(t)
	expectSessionRow(mock)
	mock.ExpectQuery(`om\.organization_id = \$2::uuid`).
		WithArgs("user-1", "11111111-1111-1111-1111-111111111111").
		WillReturnRows(sqlmock.NewRows([]string{"role"}))
	if auth := i.resolveSession(httpHostScopedCtx(), "cookie"); auth.valid {
		t.Fatalf("non-member of the host's org must be denied, got %+v", auth)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSession_HostScope_MemberGetsHostInstanceOnly(t *testing.T) {
	i, mock := newHTTPSessionMock(t)
	expectSessionRow(mock)
	mock.ExpectQuery(`om\.organization_id = \$2::uuid`).
		WithArgs("user-1", "11111111-1111-1111-1111-111111111111").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("member"))
	auth := i.resolveSession(httpHostScopedCtx(), "cookie")
	if !auth.valid || auth.tenantID != "instance-b" || auth.role != "member" {
		t.Fatalf("member must be scoped to the host instance, got %+v", auth)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWithAPIKeyValidation_SkipsCookieWhenTenantAuthRejected(t *testing.T) {
	i, mock := newHTTPSessionMock(t)
	called := false
	h := i.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "https://victim-123456.example.com/everstack.providers.v1.ProvidersService/GetConfigYAML", nil)
	req.AddCookie(&http.Cookie{Name: "es_everstack_session", Value: "cookie"})
	req = req.WithContext(contextkeys.WithTenantAuthRejected(req.Context()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called || rec.Code != http.StatusUnauthorized {
		t.Fatalf("rejected host session must not pass the API-key gate: called=%v status=%d", called, rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
