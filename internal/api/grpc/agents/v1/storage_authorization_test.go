package v1

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

func TestResolveStorageContextAuthorizesTenantBeforeCredentialLookup(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: sqlx.NewDb(db, "sqlmock")}

	if _, err := server.resolveStorageContext(context.Background(), "tenant-1", "session-1"); !errors.Is(err, storageauth.ErrUnauthenticated) {
		t.Fatalf("resolveStorageContext() error = %v, want unauthenticated", err)
	}

	crossTenantCtx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-2", "verified-key-hash")
	if _, err := server.resolveStorageContext(crossTenantCtx, "tenant-1", "session-1"); !errors.Is(err, storageauth.ErrPermissionDenied) {
		t.Fatalf("resolveStorageContext() error = %v, want permission denied", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("authorization should run before credential SQL: %v", err)
	}
}
