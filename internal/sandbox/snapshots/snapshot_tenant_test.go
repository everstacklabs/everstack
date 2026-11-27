package snapshots

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestGetByIDFiltersByTenant(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db := sqlx.NewDb(raw, "sqlmock")
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM sandbox_snapshots WHERE id = $1 AND tenant_id = $2`)).
		WithArgs("snap-a", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "name", "state", "base_image", "from_sandbox_id", "error", "size_bytes", "created_at", "updated_at", "last_used_at",
		}).AddRow("snap-a", "tenant-a", "baseline", "active", "alpine", nil, nil, 10, time.Now(), time.Now(), nil))

	got, err := NewRepository(db).GetByID(context.Background(), "tenant-a", "snap-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "snap-a" || got.TenantID != "tenant-a" {
		t.Fatalf("snapshot = %#v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
