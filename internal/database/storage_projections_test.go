package database

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/jmoiron/sqlx"
)

type projectionCredentialStore struct{ revoked []string }

func (*projectionCredentialStore) Put(context.Context, string, storagecredentials.ProviderCredentials) (string, error) {
	return "", nil
}
func (*projectionCredentialStore) Resolve(context.Context, string, string) (storagecredentials.ProviderCredentials, error) {
	return storagecredentials.ProviderCredentials{}, nil
}
func (s *projectionCredentialStore) Revoke(_ context.Context, _, reference string) error {
	s.revoked = append(s.revoked, reference)
	return nil
}

func storageProjectionEvent(t *testing.T, eventType string, payload map[string]interface{}) Event {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return Event{Type: eventType, Payload: data}
}

func storageProjectionManager(t *testing.T) (*ProjectionManager, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New(): %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	return NewProjectionManager(sqlx.NewDb(rawDB, "sqlmock"), nil), mock
}

func TestStorageConfigProjectionsRejectMissingTenant(t *testing.T) {
	pm := NewProjectionManager(nil, nil)
	events := []struct {
		name string
		run  func(context.Context, Event) error
	}{
		{name: "create", run: pm.handleStorageConfigCreated},
		{name: "update", run: pm.handleStorageConfigUpdated},
		{name: "delete", run: pm.handleStorageConfigDeleted},
	}

	for _, tt := range events {
		t.Run(tt.name, func(t *testing.T) {
			event := storageProjectionEvent(t, "storage_config."+tt.name, map[string]interface{}{"id": "config-1"})
			if err := tt.run(context.Background(), event); err == nil {
				t.Fatal("projection error = nil, want missing tenant error")
			}
		})
	}
}

func TestStorageConfigCreatedCannotOverwriteAnotherTenant(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectExec(`ON CONFLICT \(id\) DO UPDATE SET[\s\S]*WHERE object_storage_configs\.tenant_id = EXCLUDED\.tenant_id`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	event := storageProjectionEvent(t, "storage_config.created", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-2", "provider": "s3", "bucket": "bucket",
		"created_at": "2026-08-07T00:00:00Z", "updated_at": "2026-08-07T00:00:00Z",
	})
	if err := pm.handleStorageConfigCreated(context.Background(), event); err == nil {
		t.Fatal("projection error = nil, want tenant conflict error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigCreatedPersistsOnlyCredentialReference(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectExec(`INSERT INTO object_storage_configs \([\s\S]*credential_ref[\s\S]*\)[\s\S]*VALUES`).
		WithArgs(
			"config-1", "tenant-1", "s3", "https://storage.example", "us-east-1", "bucket",
			"storagecred_opaque", "", "", "prefix", true, true,
			"2026-08-08T00:00:00Z", "2026-08-08T00:00:00Z",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := storageProjectionEvent(t, "storage_config.created", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "provider": "s3",
		"endpoint": "https://storage.example", "region": "us-east-1", "bucket": "bucket",
		"credential_ref": "storagecred_opaque", "path_prefix": "prefix",
		"is_default": true, "enabled": true,
		"created_at": "2026-08-08T00:00:00Z", "updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigCreated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigCreatedPreservesLegacyCredentialsBeforeCutover(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT cutover_enabled FROM object_storage_credential_state WHERE singleton = TRUE")).
		WillReturnRows(sqlmock.NewRows([]string{"cutover_enabled"}).AddRow(false))
	mock.ExpectExec(`INSERT INTO object_storage_configs`).
		WithArgs(
			"config-1", "tenant-1", "s3", "https://storage.example", "us-east-1", "bucket",
			"", "legacy-access", "legacy-secret", "prefix", true, true,
			"2026-08-08T00:00:00Z", "2026-08-08T00:00:00Z",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := storageProjectionEvent(t, "storage_config.created", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "provider": "s3",
		"endpoint": "https://storage.example", "region": "us-east-1", "bucket": "bucket",
		"access_key_id": "legacy-access", "secret_access_key": "legacy-secret", "path_prefix": "prefix",
		"is_default": true, "enabled": true,
		"created_at": "2026-08-08T00:00:00Z", "updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigCreated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigCreatedRejectsLegacyCredentialsAfterCutover(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT cutover_enabled FROM object_storage_credential_state WHERE singleton = TRUE")).
		WillReturnRows(sqlmock.NewRows([]string{"cutover_enabled"}).AddRow(true))

	event := storageProjectionEvent(t, "storage_config.created", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "provider": "s3", "bucket": "bucket",
		"access_key_id": "legacy-access", "secret_access_key": "legacy-secret",
		"created_at": "2026-08-08T00:00:00Z", "updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigCreated(context.Background(), event); err == nil {
		t.Fatal("projection error = nil, want post-cutover plaintext rejection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigUpdatedScopesAndVerifiesTenant(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	query := regexp.QuoteMeta("UPDATE object_storage_configs SET updated_at = $2::timestamptz WHERE id = $1 AND tenant_id = $3")
	mock.ExpectExec(query).
		WithArgs("config-1", "2026-08-07T00:00:00Z", "tenant-2").
		WillReturnResult(sqlmock.NewResult(0, 0))

	event := storageProjectionEvent(t, "storage_config.updated", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-2", "updated_at": "2026-08-07T00:00:00Z",
	})
	if err := pm.handleStorageConfigUpdated(context.Background(), event); err == nil {
		t.Fatal("projection error = nil, want tenant mismatch error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigUpdatedReplacesCredentialReferenceAndClearsLegacyColumns(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COALESCE\(c\.credential_ref, ''\)[\s\S]*FOR UPDATE OF c`).
		WithArgs("config-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"credential_ref", "backend", "generation"}).AddRow("storagecred_old", "postgres", 1))
	mock.ExpectQuery(`SELECT backend, generation[\s\S]*FROM object_storage_credentials`).
		WithArgs("storagecred_new", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"backend", "generation"}).AddRow("postgres", 2))
	mock.ExpectExec(`UPDATE object_storage_configs SET updated_at = \$2::timestamptz, credential_ref = \$3, access_key_id = '', secret_access_key = '' WHERE id = \$1 AND tenant_id = \$4`).
		WithArgs("config-1", "2026-08-08T00:00:00Z", "storagecred_new", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE object_storage_credentials[\s\S]*NOT EXISTS \(SELECT 1 FROM tenant_volume_buckets`).
		WithArgs("storagecred_old", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	event := storageProjectionEvent(t, "storage_config.updated", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "credential_ref": "storagecred_new",
		"updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigUpdatedCannotLetOlderRotationOverwriteNewerReference(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COALESCE\(c\.credential_ref, ''\)[\s\S]*FOR UPDATE OF c`).
		WithArgs("config-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"credential_ref", "backend", "generation"}).AddRow("storagecred_newer", "postgres", 9))
	mock.ExpectQuery(`SELECT backend, generation[\s\S]*FROM object_storage_credentials`).
		WithArgs("storagecred_older", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"backend", "generation"}).AddRow("postgres", 8))
	mock.ExpectExec(`UPDATE object_storage_credentials[\s\S]*backend = 'postgres'`).
		WithArgs("storagecred_older", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	event := storageProjectionEvent(t, "storage_config.updated", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "credential_ref": "storagecred_older",
		"updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigUpdated(context.Background(), event); !errors.Is(err, errStaleStorageCredentialProjection) {
		t.Fatalf("projection error = %v, want stale rotation", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigUpdatedRevokesExternalReferenceThroughSelectedBackend(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	store := &projectionCredentialStore{}
	pm.SetStorageCredentialStore(store)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT COALESCE\(c\.credential_ref, ''\)[\s\S]*FOR UPDATE OF c`).
		WithArgs("config-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"credential_ref", "backend", "generation"}).AddRow("storagecred_vault_old", "vault", 1))
	mock.ExpectQuery(`SELECT backend, generation[\s\S]*FROM object_storage_credentials`).
		WithArgs("storagecred_vault_new", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"backend", "generation"}).AddRow("vault", 2))
	mock.ExpectExec(`UPDATE object_storage_configs`).
		WithArgs("config-1", "2026-08-08T00:00:00Z", "storagecred_vault_new", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	event := storageProjectionEvent(t, "storage_config.updated", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "credential_ref": "storagecred_vault_new",
		"updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if len(store.revoked) != 1 || store.revoked[0] != "storagecred_vault_old" {
		t.Fatalf("external revocations = %#v", store.revoked)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigUpdatedPreservesLegacyRotationBeforeCutover(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT cutover_enabled FROM object_storage_credential_state WHERE singleton = TRUE")).
		WillReturnRows(sqlmock.NewRows([]string{"cutover_enabled"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE object_storage_configs SET updated_at = $2::timestamptz, access_key_id = $3, secret_access_key = $4, credential_ref = NULL WHERE id = $1 AND tenant_id = $5")).
		WithArgs("config-1", "2026-08-08T00:00:00Z", "legacy-access", "legacy-secret", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := storageProjectionEvent(t, "storage_config.updated", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "access_key_id": "legacy-access",
		"secret_access_key": "legacy-secret", "updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigUpdatedPreservesLegacyAccessKeyOnlyBeforeCutover(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT cutover_enabled FROM object_storage_credential_state WHERE singleton = TRUE")).
		WillReturnRows(sqlmock.NewRows([]string{"cutover_enabled"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE object_storage_configs SET updated_at = $2::timestamptz, access_key_id = $3, credential_ref = NULL WHERE id = $1 AND tenant_id = $4")).
		WithArgs("config-1", "2026-08-08T00:00:00Z", "legacy-access", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := storageProjectionEvent(t, "storage_config.updated", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "access_key_id": "legacy-access",
		"updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigUpdatedPreservesLegacySecretOnlyBeforeCutover(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT cutover_enabled FROM object_storage_credential_state WHERE singleton = TRUE")).
		WillReturnRows(sqlmock.NewRows([]string{"cutover_enabled"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE object_storage_configs SET updated_at = $2::timestamptz, secret_access_key = $3, credential_ref = NULL WHERE id = $1 AND tenant_id = $4")).
		WithArgs("config-1", "2026-08-08T00:00:00Z", "legacy-secret", "tenant-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	event := storageProjectionEvent(t, "storage_config.updated", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-1", "secret_access_key": "legacy-secret",
		"updated_at": "2026-08-08T00:00:00Z",
	})
	if err := pm.handleStorageConfigUpdated(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigDeletedVerifiesTenant(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT c\.tenant_id[\s\S]*FOR UPDATE OF c`).
		WithArgs("config-1").
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "credential_ref", "backend"}).AddRow("tenant-1", "storagecred_old", "postgres"))
	mock.ExpectRollback()

	event := storageProjectionEvent(t, "storage_config.deleted", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-2",
	})
	if err := pm.handleStorageConfigDeleted(context.Background(), event); err == nil {
		t.Fatal("projection error = nil, want tenant mismatch error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigDeletedReplayIsIdempotentWhenAlreadyAbsent(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT c\.tenant_id[\s\S]*FOR UPDATE OF c`).
		WithArgs("config-1").
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "credential_ref", "backend"}))
	mock.ExpectRollback()

	event := storageProjectionEvent(t, "storage_config.deleted", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-2",
	})
	if err := pm.handleStorageConfigDeleted(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v, want idempotent success", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorageConfigDeletedRemovesOwnedRow(t *testing.T) {
	pm, mock := storageProjectionManager(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT c\.tenant_id[\s\S]*FOR UPDATE OF c`).
		WithArgs("config-1").
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "credential_ref", "backend"}).AddRow("tenant-2", "storagecred_delete", "postgres"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM object_storage_configs WHERE id = $1 AND tenant_id = $2")).
		WithArgs("config-1", "tenant-2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE object_storage_credentials[\s\S]*NOT EXISTS \(SELECT 1 FROM tenant_volume_buckets`).
		WithArgs("storagecred_delete", "tenant-2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	event := storageProjectionEvent(t, "storage_config.deleted", map[string]interface{}{
		"id": "config-1", "tenant_id": "tenant-2",
	})
	if err := pm.handleStorageConfigDeleted(context.Background(), event); err != nil {
		t.Fatalf("projection error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
