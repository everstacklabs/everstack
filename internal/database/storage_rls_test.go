package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

const (
	storageSchemaMigration     = "migrations/sql/postgres/object_storage_init_20260226001900/up.sql"
	baseRLSPolicyMigration     = "migrations/sql/postgres/rls_tenant_isolation_20260507141542/up.sql"
	storageRLSPolicyMigration  = "migrations/sql/postgres/object_storage_rls_policies_20260807230000/up.sql"
	storageCredentialMigration = "migrations/sql/postgres/object_storage_credentials_20260808090000/up.sql"
	storageLifecycleMigration  = "migrations/sql/postgres/storage_upload_lifecycle_20260811100000/up.sql"
)

var storageRLSTables = []string{
	"object_storage_configs",
	"object_storage_objects",
	"object_storage_usage",
	"object_storage_credentials",
	"object_storage_uploads",
	"object_storage_upload_events",
}

func TestStorageRLSPolicyMigrationCoversEveryStorageTableWithoutArming(t *testing.T) {
	data, err := os.ReadFile(storageRLSPolicyMigration)
	if err != nil {
		t.Fatalf("read storage RLS migration: %v", err)
	}
	credentialData, err := os.ReadFile(storageCredentialMigration)
	if err != nil {
		t.Fatalf("read storage credential migration: %v", err)
	}
	lifecycleData, err := os.ReadFile(storageLifecycleMigration)
	if err != nil {
		t.Fatalf("read storage lifecycle migration: %v", err)
	}
	sql := string(data) + "\n" + string(credentialData) + "\n" + string(lifecycleData)
	for _, table := range storageRLSTables {
		if !strings.Contains(sql, table) {
			t.Errorf("migration does not install a policy for %s", table)
		}
	}
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "ENABLE ROW LEVEL SECURITY") || strings.Contains(upper, "FORCE ROW LEVEL SECURITY") {
		t.Fatal("policy migration must remain dormant until storage DB calls use tenant transactions")
	}
}

func TestPGStorageRLSIsolatesEveryStorageTable(t *testing.T) {
	dsn := os.Getenv("EVS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EVS_TEST_PG_DSN not set; skipping real-Postgres storage RLS test")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	readMigration := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		return string(data)
	}

	ctx := context.Background()
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	const schema = "everstack_storage_rls_test"
	const role = "everstack_storage_rls_test"
	setup := []string{
		`CREATE SCHEMA IF NOT EXISTS everstack`,
		`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`,
		`CREATE SCHEMA ` + schema,
		`SET LOCAL search_path = ` + schema + `, everstack, public`,
		readMigration(storageSchemaMigration),
		readMigration(baseRLSPolicyMigration),
		readMigration(storageRLSPolicyMigration),
		readMigration(storageCredentialMigration),
		readMigration(storageLifecycleMigration),
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='` + role + `') THEN CREATE ROLE ` + role + ` NOLOGIN; END IF; END $$`,
		`GRANT USAGE ON SCHEMA ` + schema + ` TO ` + role,
		`GRANT USAGE ON SCHEMA everstack TO ` + role,
		`GRANT EXECUTE ON FUNCTION everstack.tenant_matches(text) TO ` + role,
		`INSERT INTO object_storage_credentials (id, tenant_id, ciphertext, key_id) VALUES
			('storagecred-a', 'tenantA', decode('00', 'hex'), 'v1'),
			('storagecred-b', 'tenantB', decode('00', 'hex'), 'v1')`,
		`INSERT INTO object_storage_configs (id, tenant_id, provider, bucket) VALUES
			('cfg-a', 'tenantA', 's3', 'bucket-a'), ('cfg-b', 'tenantB', 's3', 'bucket-b')`,
		`INSERT INTO object_storage_objects (id, tenant_id, config_id, key, purpose) VALUES
			('obj-a', 'tenantA', 'cfg-a', 'a/key', 'artifact'), ('obj-b', 'tenantB', 'cfg-b', 'b/key', 'artifact')`,
		`INSERT INTO object_storage_usage (tenant_id, total_bytes, object_count) VALUES
			('tenantA', 10, 1), ('tenantB', 20, 1)`,
		`INSERT INTO object_storage_uploads (
			id, tenant_id, config_id, key, filename, expected_size_bytes,
			actual_size_bytes, purpose, idempotency_key, request_fingerprint,
			state, reservation_state, expires_at
		) VALUES
			('upload-a', 'tenantA', 'cfg-a', 'a/upload', 'a.txt', 10, 10, 'upload', 'idem-a', 'fingerprint-a', 'ready', 'committed', NOW()),
			('upload-b', 'tenantB', 'cfg-b', 'b/upload', 'b.txt', 20, 20, 'upload', 'idem-b', 'fingerprint-b', 'ready', 'committed', NOW())`,
		`INSERT INTO object_storage_upload_events (
			sequence, tenant_id, object_id, from_state, to_state, reason_code
		) VALUES
			(101, 'tenantA', 'upload-a', '', 'ready', 'test_fixture'),
			(102, 'tenantB', 'upload-b', '', 'ready', 'test_fixture')`,
	}
	for _, statement := range setup {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatalf("storage RLS setup failed: %v", err)
		}
	}

	insertByTable := map[string]string{
		"object_storage_configs":       `INSERT INTO object_storage_configs (id, tenant_id, provider, bucket) VALUES ('cfg-cross', 'tenantB', 's3', 'cross')`,
		"object_storage_objects":       `INSERT INTO object_storage_objects (id, tenant_id, config_id, key, purpose) VALUES ('obj-cross', 'tenantB', 'cfg-b', 'cross/key', 'artifact')`,
		"object_storage_usage":         `INSERT INTO object_storage_usage (tenant_id, total_bytes, object_count) VALUES ('tenantCross', 1, 1)`,
		"object_storage_credentials":   `INSERT INTO object_storage_credentials (id, tenant_id, ciphertext, key_id) VALUES ('storagecred_cross', 'tenantB', decode('00', 'hex'), 'v1')`,
		"object_storage_uploads":       `INSERT INTO object_storage_uploads (id, tenant_id, config_id, key, filename, expected_size_bytes, purpose, idempotency_key, request_fingerprint, state, reservation_state, expires_at) VALUES ('upload-cross', 'tenantB', 'cfg-b', 'cross/upload', 'cross.txt', 1, 'upload', 'idem-cross', 'fingerprint-cross', 'pending', 'reserved', NOW())`,
		"object_storage_upload_events": `INSERT INTO object_storage_upload_events (sequence, tenant_id, object_id, from_state, to_state, reason_code) VALUES (999, 'tenantB', 'upload-b', 'ready', 'deleting', 'cross_tenant')`,
	}

	for _, table := range storageRLSTables {
		var policyCount int
		if err := tx.GetContext(ctx, &policyCount,
			`SELECT count(*) FROM pg_policies WHERE schemaname = $1 AND tablename = $2 AND policyname = 'tenant_isolation'`,
			schema, table); err != nil {
			t.Fatalf("inspect %s policy: %v", table, err)
		}
		if policyCount != 1 {
			t.Fatalf("%s policy count = %d, want 1 from shipped migration", table, policyCount)
		}
		qualified := schema + "." + table
		for _, statement := range []string{
			`ALTER TABLE ` + qualified + ` ENABLE ROW LEVEL SECURITY`,
			`ALTER TABLE ` + qualified + ` FORCE ROW LEVEL SECURITY`,
			`GRANT SELECT, INSERT, UPDATE, DELETE ON ` + qualified + ` TO ` + role,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatalf("arm %s: %v", table, err)
			}
		}
	}

	setTenant := func(tenant string) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, `RESET ROLE`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+role); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant); err != nil {
			t.Fatal(err)
		}
	}

	for _, table := range storageRLSTables {
		t.Run(table, func(t *testing.T) {
			qualified := schema + "." + table
			countAsTenant := func(tenant string) int {
				t.Helper()
				setTenant(tenant)
				var count int
				if err := tx.GetContext(ctx, &count, `SELECT count(*) FROM `+qualified); err != nil {
					t.Fatal(err)
				}
				return count
			}

			if got := countAsTenant("tenantA"); got != 1 {
				t.Fatalf("tenantA unfiltered count = %d, want 1", got)
			}
			if got := countAsTenant("ghost"); got != 0 {
				t.Fatalf("unknown tenant unfiltered count = %d, want 0", got)
			}

			setTenant("tenantA")
			result, err := tx.ExecContext(ctx, `UPDATE `+qualified+` SET tenant_id = tenant_id WHERE tenant_id = 'tenantB'`)
			if err != nil {
				t.Fatal(err)
			}
			if rows, _ := result.RowsAffected(); rows != 0 {
				t.Fatalf("cross-tenant update affected %d rows, want 0", rows)
			}
			result, err = tx.ExecContext(ctx, `DELETE FROM `+qualified+` WHERE tenant_id = 'tenantB'`)
			if err != nil {
				t.Fatal(err)
			}
			if rows, _ := result.RowsAffected(); rows != 0 {
				t.Fatalf("cross-tenant delete affected %d rows, want 0", rows)
			}

			if _, err := tx.ExecContext(ctx, `SAVEPOINT storage_cross_insert`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, insertByTable[table]); err == nil {
				t.Fatal("cross-tenant insert succeeded, want RLS WITH CHECK failure")
			}
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT storage_cross_insert`); err != nil {
				t.Fatal(fmt.Errorf("restore transaction after expected RLS failure: %w", err))
			}
		})
	}
}
