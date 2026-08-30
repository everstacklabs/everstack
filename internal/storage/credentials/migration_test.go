package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestMigrateLegacyConfigEncryptsAndScrubsConfigAndEventsAtomically(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, err := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewPostgresStore(db, cipher)
	if err != nil {
		t.Fatal(err)
	}

	referenceArg := &captureStringArgument{}
	ciphertextArg := &captureBytesArgument{}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT credential_ref, access_key_id, secret_access_key").
		WithArgs("config-1", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"credential_ref", "access_key_id", "secret_access_key"}).
			AddRow(nil, "legacy-access", "legacy-secret"))
	mock.ExpectExec("INSERT INTO object_storage_credentials").
		WithArgs(referenceArg, "tenant-a", ciphertextArg, "key-v1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE object_storage_configs").
		WithArgs(referenceArg, "config-1", "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE events").
		WithArgs(referenceArg, "tenant-a", "config-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	reference, migrated, err := store.MigrateLegacyConfig(context.Background(), "tenant-a", "config-1")
	if err != nil {
		t.Fatalf("MigrateLegacyConfig() error = %v", err)
	}
	if !migrated || reference != referenceArg.value || !strings.HasPrefix(reference, "storagecred_") {
		t.Fatalf("MigrateLegacyConfig() = %q, %t", reference, migrated)
	}
	for _, secret := range []string{"legacy-access", "legacy-secret"} {
		if strings.Contains(string(ciphertextArg.value), secret) {
			t.Fatalf("ciphertext contains %q", secret)
		}
	}
	plaintext, err := cipher.Open("tenant-a", reference, "key-v1", ciphertextArg.value)
	if err != nil {
		t.Fatal(err)
	}
	var credentials ProviderCredentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != "legacy-access" || credentials.SecretAccessKey != "legacy-secret" {
		t.Fatalf("decrypted credentials = %#v", credentials)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyConfigScrubsPlaintextWhenReferenceAlreadyExists(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, _ := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	store, _ := NewPostgresStore(db, cipher)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT credential_ref, access_key_id, secret_access_key").
		WithArgs("config-1", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"credential_ref", "access_key_id", "secret_access_key"}).
			AddRow("storagecred_existing", "stale-access", "stale-secret"))
	mock.ExpectExec("UPDATE object_storage_configs").
		WithArgs("storagecred_existing", "config-1", "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE events").
		WithArgs("storagecred_existing", "tenant-a", "config-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	reference, remediated, err := store.MigrateLegacyConfig(context.Background(), "tenant-a", "config-1")
	if err != nil || !remediated || reference != "storagecred_existing" {
		t.Fatalf("MigrateLegacyConfig() = %q, %t, %v", reference, remediated, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyConfigRejectsIncompletePlaintextWithoutWriting(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, _ := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	store, _ := NewPostgresStore(db, cipher)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT credential_ref, access_key_id, secret_access_key").
		WithArgs("config-1", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"credential_ref", "access_key_id", "secret_access_key"}).
			AddRow(nil, "legacy-access", ""))
	mock.ExpectRollback()

	_, _, err = store.MigrateLegacyConfig(context.Background(), "tenant-a", "config-1")
	if !errors.Is(err, ErrLegacyCredentialIncomplete) {
		t.Fatalf("MigrateLegacyConfig() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyVolumeBucketEncryptsAndScrubsAllProviderTokenFields(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, _ := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	store, _ := NewPostgresStore(db, cipher)

	referenceArg := &captureStringArgument{}
	ciphertextArg := &captureBytesArgument{}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT credential_ref, access_key_id, secret_access_key, cf_token_id").
		WithArgs("tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"credential_ref", "access_key_id", "secret_access_key", "cf_token_id"}).
			AddRow(nil, "volume-access", "volume-secret", "cloudflare-token-id"))
	mock.ExpectExec("INSERT INTO object_storage_credentials").
		WithArgs(referenceArg, "tenant-a", ciphertextArg, "key-v1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE tenant_volume_buckets").
		WithArgs(referenceArg, "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	reference, remediated, err := store.MigrateLegacyVolumeBucket(context.Background(), "tenant-a")
	if err != nil || !remediated || reference != referenceArg.value {
		t.Fatalf("MigrateLegacyVolumeBucket() = %q, %t, %v", reference, remediated, err)
	}
	plaintext, err := cipher.Open("tenant-a", reference, "key-v1", ciphertextArg.value)
	if err != nil {
		t.Fatal(err)
	}
	var credentials ProviderCredentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != "volume-access" || credentials.SecretAccessKey != "volume-secret" || credentials.ProviderTokenID != "cloudflare-token-id" {
		t.Fatalf("decrypted volume credentials = %#v", credentials)
	}
	for _, forbidden := range []string{"volume-access", "volume-secret", "cloudflare-token-id"} {
		if strings.Contains(string(ciphertextArg.value), forbidden) {
			t.Fatalf("ciphertext contains %q", forbidden)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryReportsCountsWithoutReadingCredentialValues(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, _ := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	store, _ := NewPostgresStore(db, cipher)

	mock.ExpectQuery("SELECT[\\s\\S]*plaintext_configs").
		WillReturnRows(sqlmock.NewRows([]string{"plaintext_configs", "incomplete_configs"}).AddRow(3, 1))
	mock.ExpectQuery("SELECT[\\s\\S]*plaintext_volume_buckets").
		WillReturnRows(sqlmock.NewRows([]string{"plaintext_volume_buckets", "incomplete_volume_buckets"}).AddRow(2, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\)[\\s\\S]*events").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

	inventory, err := store.InventoryLegacyCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.PlaintextConfigs != 3 || inventory.IncompleteConfigs != 1 ||
		inventory.PlaintextVolumeBuckets != 2 || inventory.IncompleteVolumeBuckets != 1 || inventory.PostgresEvents != 4 {
		t.Fatalf("inventory = %#v", inventory)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnableCredentialCutoverIfCleanDoesNotRequireEncryptionKey(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	mock.ExpectQuery("SELECT[\\s\\S]*plaintext_configs").
		WillReturnRows(sqlmock.NewRows([]string{"plaintext_configs", "incomplete_configs"}).AddRow(0, 0))
	mock.ExpectQuery("SELECT[\\s\\S]*plaintext_volume_buckets").
		WillReturnRows(sqlmock.NewRows([]string{"plaintext_volume_buckets", "incomplete_volume_buckets"}).AddRow(0, 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\)[\\s\\S]*events").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("UPDATE object_storage_credential_state").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := EnableCredentialCutoverIfClean(context.Background(), db); err != nil {
		t.Fatalf("EnableCredentialCutoverIfClean() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBackfillLegacyCredentialsRedactsEventOnlyExposureAndReportsBeforeAfter(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, _ := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	store, _ := NewPostgresStore(db, cipher)

	expectInventory := func(plaintext, incomplete, events int) {
		mock.ExpectQuery("SELECT[\\s\\S]*plaintext_configs").
			WillReturnRows(sqlmock.NewRows([]string{"plaintext_configs", "incomplete_configs"}).AddRow(plaintext, incomplete))
		mock.ExpectQuery("SELECT[\\s\\S]*plaintext_volume_buckets").
			WillReturnRows(sqlmock.NewRows([]string{"plaintext_volume_buckets", "incomplete_volume_buckets"}).AddRow(0, 0))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\)[\\s\\S]*events").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(events))
	}
	expectInventory(0, 0, 2)
	mock.ExpectQuery("SELECT id, tenant_id[\\s\\S]*object_storage_configs").
		WithArgs(100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}))
	mock.ExpectQuery("SELECT tenant_id[\\s\\S]*tenant_volume_buckets").
		WithArgs(100).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}))
	mock.ExpectExec("UPDATE events AS e").
		WillReturnResult(sqlmock.NewResult(0, 2))
	expectInventory(0, 0, 0)
	mock.ExpectExec("UPDATE object_storage_credential_state").
		WillReturnResult(sqlmock.NewResult(0, 1))

	report, err := store.BackfillLegacyCredentials(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Before.PostgresEvents != 2 || report.After.PostgresEvents != 0 || report.PostgresEventsRedacted != 2 || !report.CutoverEnabled {
		t.Fatalf("report = %#v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
