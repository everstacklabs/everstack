package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAppliesStorageCredentialEnvironmentOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "gateway.yaml")
	fileSecret := strings.Repeat("f", 32)
	environmentSecret := strings.Repeat("e", 32)
	data := []byte("secret_manager:\n  storage_credentials:\n    backend: postgres\n    key_id: file-v1\n    master_key: " + fileSecret + "\n    previous_keys: '{}'\n    path_prefix: file/path\n  vault:\n    address: https://vault.file.example\n    token: file-token\n    mount_path: secret\n")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EVS_STORAGE_CREDENTIAL_KEY_ID", "env-v2")
	t.Setenv("EVS_STORAGE_CREDENTIAL_MASTER_KEY", environmentSecret)
	t.Setenv("EVS_STORAGE_CREDENTIAL_BACKEND", "vault")
	t.Setenv("EVS_STORAGE_CREDENTIAL_PATH_PREFIX", "env/path")
	t.Setenv("EVS_SECRET_MANAGER_VAULT_ADDRESS", "https://vault.env.example")
	t.Setenv("EVS_SECRET_MANAGER_VAULT_TOKEN", "env-token")

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.SecretManager == nil || config.SecretManager.StorageCredentials == nil {
		t.Fatalf("storage credential config = %#v", config.SecretManager)
	}
	keyring := config.SecretManager.StorageCredentials
	if keyring.Backend != "vault" || keyring.KeyID != "env-v2" || keyring.MasterKey != environmentSecret || keyring.PreviousKeys != "{}" || keyring.PathPrefix != "env/path" {
		t.Fatalf("layered storage credential config was not resolved correctly")
	}
	if config.SecretManager.Vault == nil || config.SecretManager.Vault.Address != "https://vault.env.example" || config.SecretManager.Vault.Token != "env-token" {
		t.Fatalf("layered Vault configuration was not resolved correctly")
	}
}
