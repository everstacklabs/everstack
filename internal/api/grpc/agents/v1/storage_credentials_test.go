package v1

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/jmoiron/sqlx"
)

type agentStorageCredentialStore struct {
	reference string
	resolved  storagecredentials.ProviderCredentials
}

func (s *agentStorageCredentialStore) Put(context.Context, string, storagecredentials.ProviderCredentials) (string, error) {
	return "", nil
}

func (s *agentStorageCredentialStore) Resolve(_ context.Context, _ string, reference string) (storagecredentials.ProviderCredentials, error) {
	s.reference = reference
	return s.resolved, nil
}

func (*agentStorageCredentialStore) Revoke(context.Context, string, string) error { return nil }

func TestResolveStorageContextUsesOpaqueCredentialReference(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	credentialStore := &agentStorageCredentialStore{resolved: storagecredentials.ProviderCredentials{
		AccessKeyID: "resolved-access", SecretAccessKey: "resolved-secret",
	}}
	server := &Server{db: db, storageCredentialStore: credentialStore}

	mock.ExpectQuery("SELECT id, bucket, endpoint, region, provider, path_prefix, COALESCE\\(credential_ref, ''\\) AS credential_ref").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "bucket", "endpoint", "region", "provider", "path_prefix", "credential_ref"}).
			AddRow("config-1", "bucket", "https://storage.example", "us-east-1", "s3", "prefix", "storagecred_opaque"))

	ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-1", "verified-key-hash")
	storageContext, err := server.resolveStorageContext(ctx, "tenant-1", "session-1")
	if err != nil {
		t.Fatalf("resolveStorageContext() error = %v", err)
	}
	if storageContext == nil || storageContext.Store == nil {
		t.Fatal("resolveStorageContext() returned no storage context")
	}
	if credentialStore.reference != "storagecred_opaque" {
		t.Fatalf("resolved reference = %q", credentialStore.reference)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

var _ storagecredentials.Store = (*agentStorageCredentialStore)(nil)
