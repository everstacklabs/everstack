package credentials

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestRewrapCredentialsMovesCiphertextToActiveKey(t *testing.T) {
	oldCipher, err := NewEnvelopeCipher("key-v1", map[string]string{"key-v1": "old-root"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(ProviderCredentials{AccessKeyID: "access-value", SecretAccessKey: "secret-value"})
	oldCiphertext, _, err := oldCipher.Seal("tenant-a", "storagecred_old", payload)
	if err != nil {
		t.Fatal(err)
	}

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	rotatedCipher, err := NewEnvelopeCipher("key-v2", map[string]string{
		"key-v1": "old-root",
		"key-v2": "new-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewPostgresStore(db, rotatedCipher)
	newCiphertext := &captureBytesArgument{}

	mock.ExpectQuery("SELECT id, tenant_id, ciphertext, key_id").
		WithArgs("key-v2", 100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "ciphertext", "key_id"}).
			AddRow("storagecred_old", "tenant-a", oldCiphertext, "key-v1"))
	mock.ExpectExec("UPDATE object_storage_credentials").
		WithArgs(newCiphertext, "key-v2", "storagecred_old", "tenant-a", "key-v1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id, tenant_id, ciphertext, key_id").
		WithArgs("key-v2", 100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "ciphertext", "key_id"}))

	updated, err := store.RewrapCredentials(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("RewrapCredentials() = %d, want 1", updated)
	}
	for _, secret := range []string{"access-value", "secret-value"} {
		if strings.Contains(string(newCiphertext.value), secret) {
			t.Fatalf("rewrapped ciphertext contains %q", secret)
		}
	}
	decrypted, err := rotatedCipher.Open("tenant-a", "storagecred_old", "key-v2", newCiphertext.value)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != string(payload) {
		t.Fatalf("rewrapped payload = %q", decrypted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
