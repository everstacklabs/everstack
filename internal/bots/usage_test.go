package bots

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestListUsesOneTenantScopedUsageQuery(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	store := NewStore(sqlx.NewDb(raw, "sqlmock"))
	mock.ExpectQuery(`SELECT b.id,.* WHERE b.tenant_id=\$1 ORDER BY`).WithArgs("tenant").WillReturnRows(sqlmock.NewRows([]string{"id", "api_key_id"}).AddRow("bot-one", "one").AddRow("bot-two", "two"))
	mock.ExpectQuery(`SELECT a\.api_key_id,a\.surface,SUM\(a\.calls\).* FROM api_key_activity_daily a\s+JOIN api_keys k ON k\.id::text=a\.api_key_id AND k\.org_id=\$1\s+WHERE a\.api_key_id=ANY\(\$2\)`).WithArgs("tenant", `{"one","two"}`).WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "surface", "calls"}).AddRow("one", "mcp", int64(7)).AddRow("two", "inference", int64(9)))
	list, err := store.List(context.Background(), "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Usage.Calls30d != 7 || list[1].Usage.Calls30d != 9 || len(list[0].Usage.BySurface) != 4 {
		t.Fatal(list)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
