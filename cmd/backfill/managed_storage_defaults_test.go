package backfill

import (
	"context"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestPostgresManagedTenantSourceListsAuthoritativeInventoryByCursor(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	db := sqlx.NewDb(rawDB, "sqlmock")
	source := &postgresManagedTenantSource{db: db}

	mock.ExpectBegin()
	mock.ExpectExec(`SELECT set_config\('app.bypass_rls', 'on', true\)`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT instance_id::text[\s\S]*FROM everstack.tenant_config[\s\S]*instance_id > COALESCE\([\s\S]*NULLIF\(\$1, ''\)::uuid[\s\S]*ORDER BY instance_id[\s\S]*LIMIT \$2`).
		WithArgs("11111111-1111-1111-1111-111111111111", 2).
		WillReturnRows(sqlmock.NewRows([]string{"instance_id"}).
			AddRow("22222222-2222-2222-2222-222222222222").
			AddRow("33333333-3333-3333-3333-333333333333"))
	mock.ExpectCommit()

	got, err := source.ListManagedTenantIDs(context.Background(), "11111111-1111-1111-1111-111111111111", 2)
	if err != nil {
		t.Fatalf("ListManagedTenantIDs() error = %v", err)
	}
	want := []string{
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tenant IDs = %#v, want %#v", got, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedStorageDefaultBackfillCommandExposesRequiredInputs(t *testing.T) {
	cmd := NewManagedStorageDefaultBackfillCommand()
	if cmd.Use != "backfill-managed-storage-defaults" || !cmd.Hidden {
		t.Fatalf("command = use %q hidden %t", cmd.Use, cmd.Hidden)
	}
	for _, name := range []string{"dsn", "tenant-dsn", "cell-id", "batch"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s flag", name)
		}
	}
}
