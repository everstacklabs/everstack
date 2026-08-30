package credentials

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfiguredKeyringKeepsPreviousKeysReadableDuringRotation(t *testing.T) {
	t.Parallel()

	oldCipher, err := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "old-root-secret"})
	if err != nil {
		t.Fatal(err)
	}
	oldCiphertext, oldKeyID, err := oldCipher.Seal("tenant-a", "storagecred_old", []byte("old credential"))
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := NewEnvelopeCipherFromConfig(KeyringConfig{
		ActiveKeyID:  "key-v2",
		ActiveSecret: "new-root-secret",
		PreviousKeys: map[string]string{"key-v1": "old-root-secret"},
	})
	if err != nil {
		t.Fatalf("NewEnvelopeCipherFromConfig() error = %v", err)
	}
	plaintext, err := rotated.Open("tenant-a", "storagecred_old", oldKeyID, oldCiphertext)
	if err != nil {
		t.Fatalf("Open(previous key) error = %v", err)
	}
	if !bytes.Equal(plaintext, []byte("old credential")) {
		t.Fatalf("Open(previous key) = %q", plaintext)
	}

	_, activeKeyID, err := rotated.Seal("tenant-a", "storagecred_new", []byte("new credential"))
	if err != nil {
		t.Fatal(err)
	}
	if activeKeyID != "key-v2" {
		t.Fatalf("new ciphertext key id = %q, want key-v2", activeKeyID)
	}
}

func TestLoadKeyringConfigUsesDedicatedKeyAndParsesPreviousKeys(t *testing.T) {
	activeSecret := strings.Repeat("n", 32)
	previousSecret := strings.Repeat("o", 32)
	t.Setenv("EVS_STORAGE_CREDENTIAL_KEY_ID", "key-v2")
	t.Setenv("EVS_STORAGE_CREDENTIAL_MASTER_KEY", activeSecret)
	t.Setenv("EVS_STORAGE_CREDENTIAL_PREVIOUS_KEYS", `{"key-v1":"`+previousSecret+`"}`)

	config, err := LoadKeyringConfig()
	if err != nil {
		t.Fatalf("LoadKeyringConfig() error = %v", err)
	}
	if config.ActiveKeyID != "key-v2" || config.ActiveSecret != activeSecret {
		t.Fatalf("active key config = %#v", config)
	}
	if config.PreviousKeys["key-v1"] != previousSecret {
		t.Fatalf("previous key config = %#v", config.PreviousKeys)
	}
}

func TestNewKeyringConfigRejectsWeakMasterKey(t *testing.T) {
	_, err := NewKeyringConfig("v1", "repository-known-or-too-short", "")
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("NewKeyringConfig() error = %v, want minimum key length", err)
	}
}

func TestLoadKeyringConfigRequiresDedicatedMasterKey(t *testing.T) {
	t.Setenv("EVS_STORAGE_CREDENTIAL_KEY_ID", "")
	t.Setenv("EVS_STORAGE_CREDENTIAL_MASTER_KEY", "")
	t.Setenv("EVS_STORAGE_CREDENTIAL_PREVIOUS_KEYS", "")

	_, err := LoadKeyringConfig()
	if err == nil || !strings.Contains(err.Error(), "master key is not configured") {
		t.Fatalf("LoadKeyringConfig() error = %v, want missing master key", err)
	}
}
