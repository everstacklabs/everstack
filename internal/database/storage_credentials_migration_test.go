package database

import (
	"os"
	"strings"
	"testing"
)

func TestStorageCredentialMigrationUsesOpaqueEncryptedReferences(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(storageCredentialMigration)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(data))

	for _, required := range []string{
		"create table if not exists object_storage_credentials",
		"create table if not exists object_storage_credential_state",
		"ciphertext bytea",
		"key_id",
		"credential_ref",
		"create unique index if not exists idx_object_storage_configs_credential_ref",
		"create unique index if not exists idx_tenant_volume_buckets_credential_ref",
		"alter table tenant_volume_buckets",
		"foreign key (credential_ref, tenant_id)",
		"tenant_isolation",
		"cutover_enabled",
		"values (true, false, now())",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration is missing %q", required)
		}
	}
	if strings.Contains(sql, "enable row level security") || strings.Contains(sql, "force row level security") {
		t.Fatal("credential RLS policy must remain dormant until storage transactions set the tenant")
	}
	if strings.Contains(sql, "update object_storage_configs set access_key_id") || strings.Contains(sql, "update events") {
		t.Fatal("schema migration must not destroy historical credentials before the encrypted backfill runs")
	}
}

func TestStorageCredentialMigrationRefusesUnsafeRollback(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("migrations/sql/postgres/object_storage_credentials_20260808090000/down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	sql := strings.ToLower(string(data))
	for _, required := range []string{
		"credential_ref is not null",
		"tenant_volume_buckets",
		"exists (select 1 from object_storage_credentials)",
		"raise exception",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("down migration is missing safety guard %q", required)
		}
	}
}
