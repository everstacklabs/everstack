package storage

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestManagedDefaultIdentityIsStableAndTenantScoped(t *testing.T) {
	t.Parallel()

	firstID := ManagedDefaultConfigID("tenant-a")
	if firstID == "" || firstID != ManagedDefaultConfigID("tenant-a") {
		t.Fatalf("ManagedDefaultConfigID() = %q, want stable non-empty ID", firstID)
	}
	if firstID == ManagedDefaultConfigID("tenant-b") {
		t.Fatal("managed config IDs collide across tenants")
	}

	firstPrefix := ManagedTenantPrefix("tenant-a")
	if firstPrefix == "" || firstPrefix != ManagedTenantPrefix("tenant-a") {
		t.Fatalf("ManagedTenantPrefix() = %q, want stable non-empty prefix", firstPrefix)
	}
	if firstPrefix == ManagedTenantPrefix("tenant-b") {
		t.Fatal("managed prefixes collide across tenants")
	}
	if matched, _ := regexp.MatchString(`^tenants/[0-9a-f]{64}$`, firstPrefix); !matched {
		t.Fatalf("ManagedTenantPrefix() = %q, want opaque normalized tenant prefix", firstPrefix)
	}
}

func TestPostgresManagedDefaultsCreatesAndRepairsOneStableDefault(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	defaults, err := NewPostgresManagedDefaults(db, "r2-eu-production")
	if err != nil {
		t.Fatal(err)
	}

	tenantID := "tenant-a"
	configID := ManagedDefaultConfigID(tenantID)
	prefix := ManagedTenantPrefix(tenantID)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT set_config('app.current_tenant', $1, true)")).
		WithArgs(tenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")).
		WithArgs("everstack-storage-default:" + tenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id AS config_id, tenant_id, managed_cell_id AS cell_id,[\\s\\S]*WHERE tenant_id = \\$1 AND management_mode = 'system'[\\s\\S]*FOR UPDATE").
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"config_id", "tenant_id", "cell_id", "path_prefix", "created_at", "updated_at"}))
	mock.ExpectExec("UPDATE object_storage_configs[\\s\\S]*is_default = false").
		WithArgs(tenantID, configID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_configs").
		WithArgs(configID, tenantID, "r2-eu-production", prefix).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id AS config_id, tenant_id, managed_cell_id AS cell_id,[\\s\\S]*managed_path_prefix AS path_prefix, created_at, updated_at[\\s\\S]*FROM object_storage_configs").
		WithArgs(configID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"config_id", "tenant_id", "cell_id", "path_prefix", "created_at", "updated_at"}).
			AddRow(configID, tenantID, "r2-eu-production", prefix, now, now))
	mock.ExpectCommit()

	created, err := defaults.EnsureDefault(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("EnsureDefault() error = %v", err)
	}
	if created.ConfigID != configID || created.TenantID != tenantID || created.CellID != "r2-eu-production" || created.PathPrefix != prefix {
		t.Fatalf("EnsureDefault() = %#v", created)
	}

	// A subsequent repair keeps both the public identity and physical placement,
	// even if the bootstrap cell configured for new tenants has changed.
	legacyCellID := "r2-eu-legacy"
	legacyPrefix := ManagedTenantPrefix("tenant-a-legacy-placement")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT set_config('app.current_tenant', $1, true)")).
		WithArgs(tenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")).
		WithArgs("everstack-storage-default:" + tenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id AS config_id, tenant_id, managed_cell_id AS cell_id,[\\s\\S]*WHERE tenant_id = \\$1 AND management_mode = 'system'[\\s\\S]*FOR UPDATE").
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"config_id", "tenant_id", "cell_id", "path_prefix", "created_at", "updated_at"}).
			AddRow(configID, tenantID, legacyCellID, legacyPrefix, now, now))
	mock.ExpectExec("UPDATE object_storage_configs[\\s\\S]*is_default = false").
		WithArgs(tenantID, configID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE object_storage_configs[\\s\\S]*management_mode = 'system'").
		WithArgs(configID, tenantID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id AS config_id, tenant_id, managed_cell_id AS cell_id,[\\s\\S]*managed_path_prefix AS path_prefix, created_at, updated_at[\\s\\S]*FROM object_storage_configs").
		WithArgs(configID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"config_id", "tenant_id", "cell_id", "path_prefix", "created_at", "updated_at"}).
			AddRow(configID, tenantID, legacyCellID, legacyPrefix, now, now))
	mock.ExpectCommit()

	repaired, err := defaults.EnsureDefault(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("EnsureDefault() repair error = %v", err)
	}
	if repaired.ConfigID != created.ConfigID {
		t.Fatalf("repair changed config ID from %q to %q", created.ConfigID, repaired.ConfigID)
	}
	if repaired.CellID != legacyCellID || repaired.PathPrefix != legacyPrefix {
		t.Fatalf("repair changed physical placement: %#v", repaired)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresManagedDefaultsRejectsMissingIdentity(t *testing.T) {
	rawDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	if _, err := NewPostgresManagedDefaults(sqlx.NewDb(rawDB, "sqlmock"), ""); err == nil {
		t.Fatal("NewPostgresManagedDefaults() error = nil, want missing cell error")
	}
	defaults, err := NewPostgresManagedDefaults(sqlx.NewDb(rawDB, "sqlmock"), "r2-eu-production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaults.EnsureDefault(context.Background(), ""); err == nil {
		t.Fatal("EnsureDefault() error = nil, want missing tenant error")
	}
}
