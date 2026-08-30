package credentials

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestResolveConfigCredentialsKeepsLegacyReadsCompatibleBeforeCutover(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, _ := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	store, _ := NewPostgresStore(db, cipher)

	mock.ExpectQuery("SELECT cutover_enabled FROM object_storage_credential_state").
		WillReturnRows(sqlmock.NewRows([]string{"cutover_enabled"}).AddRow(false))
	mock.ExpectQuery("SELECT access_key_id, secret_access_key FROM object_storage_configs").
		WithArgs("config-1", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"access_key_id", "secret_access_key"}).
			AddRow("legacy-access", "legacy-secret"))

	credentials, reference, err := ResolveConfigCredentials(context.Background(), store, "tenant-a", "config-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if reference != "" || credentials.AccessKeyID != "legacy-access" || credentials.SecretAccessKey != "legacy-secret" {
		t.Fatalf("ResolveConfigCredentials() = %#v, %q", credentials, reference)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialCutoverEnabledDefaultsTrueForCustomStores(t *testing.T) {
	store := &recordingStoreForCutoverTest{}
	enabled, err := CredentialCutoverEnabled(context.Background(), store)
	if err != nil || !enabled {
		t.Fatalf("CredentialCutoverEnabled() = %t, %v", enabled, err)
	}
}

func TestResolveVolumeCredentialsKeepsLegacyReadsCompatibleBeforeCutover(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	cipher, _ := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "root-secret"})
	store, _ := NewPostgresStore(db, cipher)

	mock.ExpectQuery("SELECT cutover_enabled FROM object_storage_credential_state").
		WillReturnRows(sqlmock.NewRows([]string{"cutover_enabled"}).AddRow(false))
	mock.ExpectQuery("SELECT access_key_id, secret_access_key, cf_token_id AS provider_token_id").
		WithArgs("tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"access_key_id", "secret_access_key", "provider_token_id"}).
			AddRow("legacy-access", "legacy-secret", "legacy-token-id"))

	credentials, reference, err := ResolveVolumeCredentials(context.Background(), store, "tenant-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if reference != "" || credentials.AccessKeyID != "legacy-access" || credentials.SecretAccessKey != "legacy-secret" || credentials.ProviderTokenID != "legacy-token-id" {
		t.Fatalf("ResolveVolumeCredentials() = %#v, %q", credentials, reference)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type recordingStoreForCutoverTest struct{}

func (*recordingStoreForCutoverTest) Put(context.Context, string, ProviderCredentials) (string, error) {
	return "", nil
}
func (*recordingStoreForCutoverTest) Resolve(context.Context, string, string) (ProviderCredentials, error) {
	return ProviderCredentials{}, nil
}
func (*recordingStoreForCutoverTest) Revoke(context.Context, string, string) error { return nil }
