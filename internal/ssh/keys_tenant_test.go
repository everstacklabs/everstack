package ssh

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestDeleteKeyByTenantScopesDeleteAndReportsMissing(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer rawDB.Close()
	store := NewKeyStore(sqlx.NewDb(rawDB, "sqlmock"))
	query := regexp.QuoteMeta(`
		DELETE FROM user_ssh_keys
		WHERE id = $1 AND tenant_id = $2`)

	mock.ExpectExec(query).
		WithArgs(int64(7), "tenant-b").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.DeleteKeyByTenant(context.Background(), 7, "tenant-b"); !errors.Is(err, ErrSSHKeyNotFound) {
		t.Fatalf("foreign key delete error = %v, want ErrSSHKeyNotFound", err)
	}

	mock.ExpectExec(query).
		WithArgs(int64(7), "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.DeleteKeyByTenant(context.Background(), 7, "tenant-a"); err != nil {
		t.Fatalf("owner key delete: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}
