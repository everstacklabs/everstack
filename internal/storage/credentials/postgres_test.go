package credentials

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

type captureStringArgument struct {
	value string
}

func (m *captureStringArgument) Match(value driver.Value) bool {
	got, ok := value.(string)
	if ok {
		m.value = got
	}
	return ok
}

type captureBytesArgument struct {
	value []byte
}

func (m *captureBytesArgument) Match(value driver.Value) bool {
	got, ok := value.([]byte)
	if ok {
		m.value = append([]byte(nil), got...)
	}
	return ok
}

func TestPostgresStorePersistsOpaqueEncryptedCredentialReferences(t *testing.T) {
	t.Parallel()

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

	refArg := &captureStringArgument{}
	ciphertextArg := &captureBytesArgument{}
	mock.ExpectExec("INSERT INTO object_storage_credentials").
		WithArgs(refArg, "tenant-a", ciphertextArg, "key-v1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	want := ProviderCredentials{AccessKeyID: "access-key-value", SecretAccessKey: "secret-key-value"}
	reference, err := store.Put(context.Background(), "tenant-a", want)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if reference != refArg.value || !strings.HasPrefix(reference, "storagecred_") {
		t.Fatalf("Put() reference = %q, captured = %q", reference, refArg.value)
	}
	if strings.Contains(reference, "tenant-a") || strings.Contains(reference, want.AccessKeyID) {
		t.Fatalf("credential reference discloses tenant or credential data: %q", reference)
	}
	for _, secret := range []string{want.AccessKeyID, want.SecretAccessKey} {
		if strings.Contains(string(ciphertextArg.value), secret) {
			t.Fatalf("stored ciphertext contains plaintext credential %q", secret)
		}
	}

	mock.ExpectQuery("SELECT ciphertext, key_id FROM object_storage_credentials").
		WithArgs(reference, "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"ciphertext", "key_id"}).AddRow(ciphertextArg.value, "key-v1"))

	got, err := store.Resolve(context.Background(), "tenant-a", reference)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != want {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}

	mock.ExpectQuery("SELECT ciphertext, key_id FROM object_storage_credentials").
		WithArgs(reference, "tenant-b").
		WillReturnError(sql.ErrNoRows)
	if _, err := store.Resolve(context.Background(), "tenant-b", reference); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("cross-tenant Resolve() error = %v, want ErrCredentialNotFound", err)
	}

	mock.ExpectExec("UPDATE object_storage_credentials").
		WithArgs(reference, "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.Revoke(context.Background(), "tenant-a", reference); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
