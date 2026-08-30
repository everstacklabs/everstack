package database

import (
	"os"
	"strings"
	"testing"
)

func TestManagedStorageMigrationSeparatesLogicalConnectionFromPhysicalPlacement(t *testing.T) {
	data, err := os.ReadFile("migrations/sql/postgres/managed_storage_default_20260816000000/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, required := range []string{
		"'everstack'",
		"management_mode",
		"managed_cell_id",
		"managed_path_prefix",
		"provider = 'everstack'",
		"endpoint = ''",
		"bucket = ''",
		"credential_ref IS NULL",
		"is_default = true",
		"enabled = true",
		"managed_path_prefix ~ '^tenants/[0-9a-f]{64}$'",
		"WHERE management_mode = 'system'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("managed storage migration is missing %q", required)
		}
	}
}

func TestManagedStorageMigrationRefusesDestructiveRollback(t *testing.T) {
	data, err := os.ReadFile("migrations/sql/postgres/managed_storage_default_20260816000000/down.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	if !strings.Contains(sql, "RAISE EXCEPTION") {
		t.Fatal("managed storage rollback does not fail closed when connections exist")
	}
	if strings.Contains(sql, "DELETE FROM object_storage_configs") {
		t.Fatal("managed storage rollback deletes customer object metadata")
	}
}
