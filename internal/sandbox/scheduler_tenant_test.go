package sandbox

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestRunCronNowLoadsTheCronInTenantScope(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	mgr := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM sandbox_crons WHERE id = $1 AND tenant_id = $2")).
		WithArgs(int64(42), "tenant-a").
		WillReturnError(sql.ErrNoRows)

	if err := mgr.RunCronNow(context.Background(), 42, "tenant-a"); err == nil {
		t.Fatal("RunCronNow error = nil, want missing cron error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
