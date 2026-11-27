package metrics

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestHistoryFiltersByTenantAndSandbox(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db := sqlx.NewDb(raw, "sqlmock")
	t.Cleanup(func() { _ = db.Close() })

	query := regexp.QuoteMeta(`
		SELECT * FROM (
			SELECT * FROM sandbox_metrics_history
			WHERE tenant_id = $1 AND sandbox_id = $2
			ORDER BY collected_at DESC LIMIT $3
		) t ORDER BY collected_at ASC`)
	mock.ExpectQuery(query).
		WithArgs("tenant-a", "sbx-a", 25).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "sandbox_id", "tenant_id", "cpu_percent", "memory_usage", "memory_limit", "disk_used_mb", "collected_at",
		}).AddRow(1, "sbx-a", "tenant-a", 12.5, 1024, 2048, 100, time.Now()))

	got, err := NewRepository(db).History(context.Background(), "tenant-a", "sbx-a", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TenantID != "tenant-a" || got[0].SandboxID != "sbx-a" {
		t.Fatalf("history = %#v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
