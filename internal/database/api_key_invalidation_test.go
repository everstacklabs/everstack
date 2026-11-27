package database

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/jmoiron/sqlx"
)

func TestAPIKeyRevokeProjectionInvalidatesCaches(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	pm := NewProjectionManager(sqlx.NewDb(raw, "sqlmock"), NewInMemoryEventBus())
	mock.ExpectExec("UPDATE api_keys SET revoked=TRUE").WithArgs("projection-key", sqlmock.AnyArg(), "tenant").WillReturnResult(sqlmock.NewResult(0, 1))
	revision := apikey.CacheRevision()
	err = pm.handleApiKeyRevoked(context.Background(), Event{Payload: []byte(`{"id":"projection-key","org_id":"tenant","revoked_at":"2026-11-10T00:00:00Z"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if apikey.CacheRevision() == revision {
		t.Fatal("revoke did not invalidate process caches")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
