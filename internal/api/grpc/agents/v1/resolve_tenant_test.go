package v1

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/jmoiron/sqlx"
)

// TestResolveTenantID_FailClosedWithoutContext pins the post-P0
// contract: when the request context has no tenant id and there is no
// single-tenant DB fallback available, the handler must return
// PermissionDenied. Regressing this re-opens the cross-tenant leak
// closed on 2026-05-06 — see project_tenant_isolation_bugs.md.
//
// We construct the Server with db=nil so singleTenantFallback is
// short-circuited; this isolates the auth-context branch from any
// "lone organizations row" environment quirks in the test DB.
func TestResolveTenantID_FailClosedWithoutContext(t *testing.T) {
	s := &Server{}
	tid, err := s.resolveTenantID(context.Background(), "client-supplied-must-be-ignored")
	if err == nil {
		t.Fatalf("expected error when context has no tenant id, got tid=%q", tid)
	}
	if tid != "" {
		t.Fatalf("expected empty tenant id on failure, got %q", tid)
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	if ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %v", ce.Code())
	}
}

// TestResolveTenantID_PrefersContextTenant locks the source-of-truth
// contract: when context has a tenant id, that's what's used. The
// `requestTenantID` parameter remains on the signature for caller
// compatibility but must never be consulted (tested by passing a
// distinct value and asserting the context value wins).
func TestResolveTenantID_PrefersContextTenant(t *testing.T) {
	s := &Server{}
	ctx := contextkeys.WithTenantID(context.Background(), "ctx-tenant")
	tid, err := s.resolveTenantID(ctx, "body-tenant-must-be-ignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tid != "ctx-tenant" {
		t.Fatalf("expected context tenant id, got %q", tid)
	}
}

func TestSingleTenantFallbackRechecksAFormerlySingleTenantDatabase(t *testing.T) {
	withUnmanagedGateway(t)
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{db: sqlx.NewDb(rawDB, "sqlmock")}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id::text FROM organizations LIMIT 2")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(ownerTenant))
	if tenantID, ok := s.singleTenantFallback(context.Background()); !ok || tenantID != ownerTenant {
		t.Fatalf("first fallback = %q, %v", tenantID, ok)
	}
	if tenantID, ok := s.singleTenantFallback(context.Background()); !ok || tenantID != ownerTenant {
		t.Fatalf("cached fallback = %q, %v", tenantID, ok)
	}

	s.singleTenantFallbackCached.Store(&singleTenantFallbackResult{
		tenantID:  ownerTenant,
		expiresAt: time.Now().Add(-time.Second),
	})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id::text FROM organizations LIMIT 2")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(ownerTenant).AddRow(otherTenant))
	if tenantID, ok := s.singleTenantFallback(context.Background()); ok || tenantID != "" {
		t.Fatalf("second fallback = %q, %v", tenantID, ok)
	}

	if tenantID, ok := s.singleTenantFallback(context.Background()); ok || tenantID != "" {
		t.Fatalf("disabled fallback = %q, %v", tenantID, ok)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSingleTenantFallbackIsDisabledOnManagedGateway(t *testing.T) {
	previous := enterprise.ManagedGateway()
	enterprise.SetManagedGateway(true)
	t.Cleanup(func() { enterprise.SetManagedGateway(previous) })

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{db: sqlx.NewDb(rawDB, "sqlmock")}
	s.singleTenantFallbackCached.Store(&singleTenantFallbackResult{
		tenantID:  ownerTenant,
		expiresAt: time.Now().Add(time.Minute),
	})
	if tenantID, ok := s.singleTenantFallback(context.Background()); ok || tenantID != "" {
		t.Fatalf("managed fallback = %q, %v", tenantID, ok)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSingleTenantFallbackRetriesAfterAnEmptyDatabase(t *testing.T) {
	withUnmanagedGateway(t)
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{db: sqlx.NewDb(rawDB, "sqlmock")}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id::text FROM organizations LIMIT 2")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if tenantID, ok := s.singleTenantFallback(context.Background()); ok || tenantID != "" {
		t.Fatalf("empty fallback = %q, %v", tenantID, ok)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id::text FROM organizations LIMIT 2")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(ownerTenant))
	if tenantID, ok := s.singleTenantFallback(context.Background()); !ok || tenantID != ownerTenant {
		t.Fatalf("populated fallback = %q, %v", tenantID, ok)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func withUnmanagedGateway(t *testing.T) {
	t.Helper()
	previous := enterprise.ManagedGateway()
	enterprise.SetManagedGateway(false)
	t.Cleanup(func() { enterprise.SetManagedGateway(previous) })
}
