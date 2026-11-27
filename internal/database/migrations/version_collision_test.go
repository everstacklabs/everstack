package migrations

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// The migrator logs a warning when a version on disk is already recorded
// under another migration's name, which is how a skipped migration shows up
// on a database that already ran the other set.
func TestVersionsRecordedUnderAnotherName(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT name, version FROM everstack.schema_migrations WHERE dialect = $1 ORDER BY version DESC`)).
		WithArgs("postgres").
		WillReturnRows(sqlmock.NewRows([]string{"name", "version"}).
			AddRow("sandbox_idle_retention", int64(20260207000000)).
			AddRow("same_name", int64(20260101000000)))

	ups := []Migration{
		{Name: "feature_flags", Version: 20260207000000},
		{Name: "same_name", Version: 20260101000000},
		{Name: "not_applied_yet", Version: 20260301000000},
	}
	got := versionsRecordedUnderAnotherName(context.Background(), sqlx.NewDb(db, "sqlmock"), "postgres", ups)
	if len(got) != 1 || got[0].Migration.Name != "feature_flags" || got[0].RecordedName != "sandbox_idle_retention" {
		t.Fatalf("versionsRecordedUnderAnotherName() = %+v, want only feature_flags shadowed by sandbox_idle_retention", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	// Only Postgres is checked; other dialects are left alone.
	if got := versionsRecordedUnderAnotherName(context.Background(), sqlx.NewDb(db, "sqlmock"), "clickhouse", ups); got != nil {
		t.Fatalf("clickhouse check = %+v, want nil", got)
	}
}
