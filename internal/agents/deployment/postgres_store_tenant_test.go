package deployment

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// Keys and invocations are read and changed only for the tenant that owns
// them, in SQL, not just in the handler.
func TestKeyAndInvocationQueriesFilterByTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	store := NewPostgresStore(sqlx.NewDb(rawDB, "sqlmock"))
	ctx := context.Background()

	mock.ExpectQuery(regexp.QuoteMeta("FROM agent_deployment_keys WHERE deployment_id = $1 AND tenant_id = $2")).
		WithArgs("dep-1", "tenant-a").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := store.ListKeys(ctx, "dep-1", "tenant-a"); err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_deployment_keys SET is_active = FALSE WHERE id = $1 AND tenant_id = $2")).
		WithArgs("key-1", "tenant-b").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.RevokeKey(ctx, "key-1", "tenant-b"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("RevokeKey of a key the tenant does not own = %v, want ErrKeyNotFound", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM agent_deployment_invocations WHERE deployment_id = $1 AND tenant_id = $2")).
		WithArgs("dep-1", "tenant-a").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE deployment_id = $1 AND tenant_id = $2")).
		WithArgs("dep-1", "tenant-a", 50, 0).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, _, err := store.ListInvocations(ctx, "dep-1", "tenant-a", 0, 0); err != nil {
		t.Fatalf("ListInvocations: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
