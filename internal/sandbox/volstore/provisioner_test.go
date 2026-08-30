package volstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	"github.com/jmoiron/sqlx"
)

type recordingCredentialStore struct {
	reference string
	put       []storagecredentials.ProviderCredentials
	resolved  storagecredentials.ProviderCredentials
}

func (s *recordingCredentialStore) Put(_ context.Context, _ string, credentials storagecredentials.ProviderCredentials) (string, error) {
	s.put = append(s.put, credentials)
	return s.reference, nil
}

func (s *recordingCredentialStore) Resolve(context.Context, string, string) (storagecredentials.ProviderCredentials, error) {
	return s.resolved, nil
}

func (*recordingCredentialStore) Revoke(context.Context, string, string) error { return nil }

func TestBucketName(t *testing.T) {
	// R2 bucket names: 3-63 chars, lowercase alphanumeric + hyphens.
	re := regexp.MustCompile(`^[a-z0-9-]{3,63}$`)
	for _, tenant := range []string{
		"inst_abc123",
		"550e8400-e29b-41d4-a716-446655440000",
		"Tenant With CAPS and spaces!!",
		"",
	} {
		got := BucketName(tenant)
		if !re.MatchString(got) {
			t.Fatalf("BucketName(%q) = %q is not a valid R2 bucket name", tenant, got)
		}
	}
	// Deterministic.
	if BucketName("inst_x") != BucketName("inst_x") {
		t.Fatal("BucketName not deterministic")
	}
	// Distinct tenants → distinct buckets.
	if BucketName("a") == BucketName("b") {
		t.Fatal("BucketName collision across tenants")
	}
}

func TestDeriveSecretAccessKey(t *testing.T) {
	val := "8M7wS6hCpXVc-DoRnPPY_UCWPgy8aea4Wy6kCe5T"
	sum := sha256.Sum256([]byte(val))
	want := hex.EncodeToString(sum[:])
	if got := deriveSecretAccessKey(val); got != want {
		t.Fatalf("deriveSecretAccessKey = %q, want %q", got, want)
	}
	if len(deriveSecretAccessKey(val)) != 64 {
		t.Fatal("secret access key must be 64 hex chars")
	}
}

func TestNewNilWhenUnconfigured(t *testing.T) {
	if New(nil, Config{}) != nil {
		t.Fatal("New(nil db) should return nil")
	}
}

func TestLookupResolvesOpaqueVolumeCredentialReference(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	credentialStore := &recordingCredentialStore{resolved: storagecredentials.ProviderCredentials{
		AccessKeyID: "resolved-access", SecretAccessKey: "resolved-secret", ProviderTokenID: "resolved-token",
	}}
	provisioner := New(db, Config{AccountID: "account", APIToken: "api-token"}, credentialStore)

	mock.ExpectQuery("SELECT tenant_id, bucket_name, endpoint, COALESCE\\(credential_ref, ''\\) AS credential_ref").
		WithArgs("tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "bucket_name", "endpoint", "credential_ref"}).
			AddRow("tenant-a", "bucket", "https://storage.example", "storagecred_volume"))

	bucket, err := provisioner.lookup(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if bucket.AccessKeyID != "resolved-access" || bucket.SecretAccessKey != "resolved-secret" || bucket.CFTokenID != "resolved-token" {
		t.Fatalf("resolved bucket = %#v", bucket)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorePersistsOnlyOpaqueVolumeCredentialReference(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	credentialStore := &recordingCredentialStore{reference: "storagecred_volume"}
	provisioner := New(db, Config{AccountID: "account", APIToken: "api-token"}, credentialStore)
	bucket := &TenantBucket{
		TenantID: "tenant-a", BucketName: "bucket", Endpoint: "https://storage.example",
		AccessKeyID: "raw-access", SecretAccessKey: "raw-secret", CFTokenID: "raw-token",
	}

	mock.ExpectExec("INSERT INTO tenant_volume_buckets").
		WithArgs("tenant-a", "bucket", "https://storage.example", "storagecred_volume").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := provisioner.store(context.Background(), bucket); err != nil {
		t.Fatal(err)
	}
	if len(credentialStore.put) != 1 || credentialStore.put[0].ProviderTokenID != "raw-token" {
		t.Fatalf("encrypted credential writes = %#v", credentialStore.put)
	}
	if bucket.CredentialRef != "storagecred_volume" {
		t.Fatalf("bucket credential ref = %q", bucket.CredentialRef)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

var _ storagecredentials.Store = (*recordingCredentialStore)(nil)
