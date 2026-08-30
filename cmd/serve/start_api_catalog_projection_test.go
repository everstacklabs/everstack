package serve

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/jmoiron/sqlx"
)

func TestCatalogProjectionRequiresPostgresPrimary(t *testing.T) {
	for _, test := range []struct {
		name    string
		primary *database.Conn
		want    bool
	}{
		{name: "missing"},
		{name: "postgres without connection", primary: &database.Conn{Type: database.TypePostgres}},
		{name: "clickhouse", primary: &database.Conn{Type: database.TypeClickHouse, RW: &sqlx.DB{}}},
		{name: "postgres", primary: &database.Conn{Type: database.TypePostgres, RW: &sqlx.DB{}}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := supportsCatalogProjection(test.primary); got != test.want {
				t.Fatalf("supportsCatalogProjection() = %t, want %t", got, test.want)
			}
		})
	}
}
