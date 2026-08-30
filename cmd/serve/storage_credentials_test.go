package serve

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/jmoiron/sqlx"
)

func storageCredentialTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	rawDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	return sqlx.NewDb(rawDB, "sqlmock")
}

func TestNewStorageCredentialStoreSelectsVaultBackend(t *testing.T) {
	store, err := newStorageCredentialStore(storageCredentialTestDB(t), &validator.Config{
		SecretManager: &validator.SecretManagerConfig{
			Type:               "vault",
			StorageCredentials: &validator.StorageCredentialKeyringConfig{Backend: "inherit", PathPrefix: "everstack/storage"},
			Vault: &validator.VaultSecretManagerConfig{
				Address: "https://vault.example", Token: "vault-token", MountPath: "secret",
			},
		},
	})
	if err != nil {
		t.Fatalf("newStorageCredentialStore() error = %v", err)
	}
	if _, ok := store.(*storagecredentials.VaultStore); !ok {
		t.Fatalf("store type = %T, want VaultStore", store)
	}
}

func TestNewStorageCredentialStoreRejectsUnsupportedBackend(t *testing.T) {
	_, err := newStorageCredentialStore(storageCredentialTestDB(t), &validator.Config{
		SecretManager: &validator.SecretManagerConfig{
			StorageCredentials: &validator.StorageCredentialKeyringConfig{Backend: "unknown"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported storage credential backend") {
		t.Fatalf("newStorageCredentialStore() error = %v", err)
	}
}
