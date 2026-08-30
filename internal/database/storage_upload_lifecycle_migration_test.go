package database

import (
	"os"
	"strings"
	"testing"
)

const storageUploadLifecycleMigration = "migrations/sql/postgres/storage_upload_lifecycle_20260811100000/up.sql"

func TestStorageUploadLifecycleMigrationShipsDurableStatesAccountingAndLegacyBackfill(t *testing.T) {
	data, err := os.ReadFile(storageUploadLifecycleMigration)
	if err != nil {
		t.Fatalf("read upload lifecycle migration: %v", err)
	}
	sql := string(data)

	required := []string{
		"CREATE TABLE IF NOT EXISTS object_storage_uploads",
		"CREATE TABLE IF NOT EXISTS object_storage_upload_events",
		"reserved_bytes",
		"reserved_object_count",
		"idempotency_key",
		"request_fingerprint",
		"reservation_state",
		"multipart_upload_id",
		"INSERT INTO object_storage_uploads",
		"FROM object_storage_objects",
		"CREATE POLICY tenant_isolation ON object_storage_uploads",
		"CREATE POLICY tenant_isolation ON object_storage_upload_events",
	}
	for _, token := range required {
		if !strings.Contains(sql, token) {
			t.Errorf("upload lifecycle migration is missing %q", token)
		}
	}

	for _, state := range []string{
		"pending",
		"transferred",
		"verifying",
		"ready",
		"failed",
		"quarantined",
		"deleting",
		"deleted",
	} {
		if !strings.Contains(sql, "'"+state+"'") {
			t.Errorf("upload lifecycle migration is missing state %q", state)
		}
	}

	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "ENABLE ROW LEVEL SECURITY") || strings.Contains(upper, "FORCE ROW LEVEL SECURITY") {
		t.Fatal("upload lifecycle RLS policies must remain dormant until tenant transactions are armed")
	}
}
