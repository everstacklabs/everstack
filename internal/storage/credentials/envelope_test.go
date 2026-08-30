package credentials

import (
	"bytes"
	"testing"
)

func TestEnvelopeCipherEncryptsAndBindsCredentialsToTenantAndReference(t *testing.T) {
	t.Parallel()

	cipher, err := NewEnvelopeCipher("key-v1", map[string]string{
		"key-v1": "a stable high-entropy storage credential root secret",
	})
	if err != nil {
		t.Fatalf("NewEnvelopeCipher() error = %v", err)
	}

	plaintext := []byte(`{"access_key_id":"access-key-value","secret_access_key":"secret-key-value"}`)
	ciphertext, keyID, err := cipher.Seal("tenant-a", "storagecred_reference", plaintext)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if keyID != "key-v1" {
		t.Fatalf("Seal() key id = %q, want key-v1", keyID)
	}
	for _, secret := range [][]byte{[]byte("access-key-value"), []byte("secret-key-value")} {
		if bytes.Contains(ciphertext, secret) {
			t.Fatalf("ciphertext contains plaintext credential %q", secret)
		}
	}

	got, err := cipher.Open("tenant-a", "storagecred_reference", keyID, ciphertext)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Open() = %q, want %q", got, plaintext)
	}

	if _, err := cipher.Open("tenant-b", "storagecred_reference", keyID, ciphertext); err == nil {
		t.Fatal("Open() succeeded for a different tenant")
	}
	if _, err := cipher.Open("tenant-a", "different-reference", keyID, ciphertext); err == nil {
		t.Fatal("Open() succeeded for a different reference")
	}
}
