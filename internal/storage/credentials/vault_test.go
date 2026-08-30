package credentials

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func vaultTestStore(t *testing.T, transport roundTripFunc) (*VaultStore, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	store, err := NewVaultStore(sqlx.NewDb(rawDB, "sqlmock"), VaultConfig{
		Address: "https://vault.example", Token: "vault-root-token", Namespace: "everstack",
		MountPath: "secret", PathPrefix: "everstack/storage-credentials",
	}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return store, mock
}

func TestVaultStoreWritesValueToVaultAndOnlyReferenceMetadataToPostgres(t *testing.T) {
	secret := "provider-secret-value"
	var requestPath string
	store, mock := vaultTestStore(t, func(request *http.Request) (*http.Response, error) {
		requestPath = request.URL.Path
		if request.Header.Get("X-Vault-Token") != "vault-root-token" || request.Header.Get("X-Vault-Namespace") != "everstack" {
			t.Fatalf("vault authentication headers were not set")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), secret) {
			t.Fatalf("vault request did not contain provider credential")
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	mock.ExpectExec("INSERT INTO object_storage_credentials").
		WithArgs(sqlmock.AnyArg(), "tenant-a", "vault", []byte{}, "vault").
		WillReturnResult(sqlmock.NewResult(0, 1))

	reference, err := store.Put(context.Background(), "tenant-a", ProviderCredentials{AccessKeyID: "access", SecretAccessKey: secret})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if !strings.HasPrefix(reference, "storagecred_") || strings.Contains(requestPath, "tenant-a") || !strings.Contains(requestPath, reference) {
		t.Fatalf("vault path = %q, reference = %q", requestPath, reference)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVaultStoreResolvesTenantScopedReference(t *testing.T) {
	store, mock := vaultTestStore(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s", request.Method)
		}
		body := `{"data":{"data":{"access_key_id":"access","secret_access_key":"secret"}}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	mock.ExpectQuery("SELECT backend FROM object_storage_credentials").
		WithArgs("storagecred_ref", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("vault"))

	credentials, err := store.Resolve(context.Background(), "tenant-a", "storagecred_ref")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if credentials.AccessKeyID != "access" || credentials.SecretAccessKey != "secret" {
		t.Fatalf("Resolve() credentials = %#v", credentials)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVaultStorePermanentlyDeletesUnreferencedValueBeforeRegistryRevocation(t *testing.T) {
	store, mock := vaultTestStore(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodDelete || !strings.Contains(request.URL.Path, "/metadata/") {
			t.Fatalf("vault revocation request = %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	mock.ExpectQuery("SELECT backend FROM object_storage_credentials").
		WithArgs("storagecred_ref", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("vault"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT backend FROM object_storage_credentials").
		WithArgs("storagecred_ref", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("vault"))
	mock.ExpectExec("UPDATE object_storage_credentials").
		WithArgs("storagecred_ref", "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.Revoke(context.Background(), "tenant-a", "storagecred_ref"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVaultStoreErrorDoesNotExposeProviderResponse(t *testing.T) {
	store, mock := vaultTestStore(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("Authorization: provider-secret-value")), Header: make(http.Header)}, nil
	})
	_, err := store.Put(context.Background(), "tenant-a", ProviderCredentials{AccessKeyID: "access", SecretAccessKey: "provider-secret-value"})
	if err == nil || strings.Contains(err.Error(), "provider-secret-value") || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("Put() error exposes provider response: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
