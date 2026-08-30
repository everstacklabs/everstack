package serve

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	s3store "github.com/everstacklabs/everstack/internal/storage/s3"
	"github.com/jmoiron/sqlx"
)

type managedStorageTestStore struct {
	bucket string
	key    string
}

func (s *managedStorageTestStore) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, nil
}

func (s *managedStorageTestStore) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}

func (s *managedStorageTestStore) Put(_ context.Context, bucket, key, _ string, _ io.Reader) (string, error) {
	s.bucket = bucket
	s.key = key
	return "etag", nil
}

func (s *managedStorageTestStore) Delete(context.Context, string, string) error { return nil }

func (s *managedStorageTestStore) Head(context.Context, string, string) (int64, string, error) {
	return 0, "", nil
}

func (s *managedStorageTestStore) List(context.Context, string, string) ([]storagepkg.BucketObject, error) {
	return nil, nil
}

func (s *managedStorageTestStore) BlobCapabilities() storagepkg.BlobCapabilities {
	return storagepkg.BlobCapabilities{ContractVersion: storagepkg.BlobPlaneV2}
}

func (s *managedStorageTestStore) Get(_ context.Context, _ string, key string) (*storagepkg.ObjectReader, error) {
	return &storagepkg.ObjectReader{
		ObjectInfo: storagepkg.ObjectInfo{Key: key},
		Body:       io.NopCloser(strings.NewReader("content")),
	}, nil
}

func (s *managedStorageTestStore) ListPage(context.Context, string, storagepkg.ListOptions) (storagepkg.ObjectPage, error) {
	return storagepkg.ObjectPage{}, nil
}

func (s *managedStorageTestStore) PutIfAbsent(_ context.Context, _ string, key string, _ io.Reader, _ storagepkg.PutOptions) (storagepkg.ObjectInfo, error) {
	return storagepkg.ObjectInfo{Key: key}, nil
}

func (s *managedStorageTestStore) CopyIfAbsent(_ context.Context, _ string, _ string, _ string, destinationKey string) (storagepkg.CopyResult, error) {
	return storagepkg.CopyResult{ObjectInfo: storagepkg.ObjectInfo{Key: destinationKey}}, nil
}

func (s *managedStorageTestStore) BeginMultipart(_ context.Context, bucket, key string, options storagepkg.MultipartOptions) (storagepkg.MultipartUpload, error) {
	return storagepkg.MultipartUpload{Bucket: bucket, Key: key, ID: "upload-1", Options: options}, nil
}

func (s *managedStorageTestStore) UploadPart(context.Context, storagepkg.MultipartUpload, int32, io.Reader, storagepkg.PutOptions) (storagepkg.UploadedPart, error) {
	return storagepkg.UploadedPart{}, nil
}

func (s *managedStorageTestStore) CompleteMultipart(_ context.Context, upload storagepkg.MultipartUpload, _ []storagepkg.UploadedPart) (storagepkg.ObjectInfo, error) {
	return storagepkg.ObjectInfo{Key: upload.Key}, nil
}

func (s *managedStorageTestStore) AbortMultipart(context.Context, storagepkg.MultipartUpload) error {
	return nil
}

func validManagedStorageEnvironment() map[string]string {
	return map[string]string{
		managedStorageEnabledEnv:     "true",
		managedStorageCellIDEnv:      "r2-eu-prod-001",
		managedStorageR2EndpointEnv:  "https://0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com",
		managedStorageR2RegionEnv:    "auto",
		managedStorageR2BucketEnv:    "everstack-storage-prod-eu-001",
		managedStorageR2AccessKeyEnv: "platform-access-key",
		managedStorageR2SecretKeyEnv: "platform-secret-key",
	}
}

func environmentLookup(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestLoadManagedStorageR2ConfigDisabledByDefault(t *testing.T) {
	t.Parallel()

	config, enabled, err := loadManagedStorageR2Config(environmentLookup(nil))
	if err != nil {
		t.Fatalf("loadManagedStorageR2Config() error = %v", err)
	}
	if enabled || config != (managedStorageR2Config{}) {
		t.Fatalf("disabled config = %#v, enabled = %v", config, enabled)
	}
}

func TestLoadManagedStorageR2ConfigAcceptsCompleteEUCell(t *testing.T) {
	t.Parallel()

	config, enabled, err := loadManagedStorageR2Config(environmentLookup(validManagedStorageEnvironment()))
	if err != nil {
		t.Fatalf("loadManagedStorageR2Config() error = %v", err)
	}
	if !enabled {
		t.Fatal("loadManagedStorageR2Config() enabled = false")
	}
	if config.CellID != "r2-eu-prod-001" || config.Region != "auto" || config.Bucket != "everstack-storage-prod-eu-001" {
		t.Fatalf("loadManagedStorageR2Config() config = %#v", config)
	}
}

func TestLoadManagedStorageR2ConfigFailsClosedOnPartialConfiguration(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{
		managedStorageCellIDEnv,
		managedStorageR2EndpointEnv,
		managedStorageR2RegionEnv,
		managedStorageR2BucketEnv,
		managedStorageR2AccessKeyEnv,
		managedStorageR2SecretKeyEnv,
	} {
		missing := missing
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			environment := validManagedStorageEnvironment()
			delete(environment, missing)
			_, enabled, err := loadManagedStorageR2Config(environmentLookup(environment))
			if !enabled || err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("loadManagedStorageR2Config() enabled = %v, error = %v", enabled, err)
			}
			if strings.Contains(err.Error(), "platform-access-key") || strings.Contains(err.Error(), "platform-secret-key") {
				t.Fatalf("configuration error leaked credentials: %v", err)
			}
		})
	}
}

func TestLoadManagedStorageR2ConfigRejectsUnsafeOrNonEUCells(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		env   string
		value string
	}{
		{name: "invalid enabled flag", env: managedStorageEnabledEnv, value: "sometimes"},
		{name: "invalid cell id", env: managedStorageCellIDEnv, value: "EU cell"},
		{name: "non EU endpoint", env: managedStorageR2EndpointEnv, value: "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com"},
		{name: "endpoint path", env: managedStorageR2EndpointEnv, value: "https://0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com/bucket"},
		{name: "endpoint credentials", env: managedStorageR2EndpointEnv, value: "https://user:pass@0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com"},
		{name: "wrong signing region", env: managedStorageR2RegionEnv, value: "eu-west-1"},
		{name: "invalid bucket", env: managedStorageR2BucketEnv, value: "Prod Bucket"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			environment := validManagedStorageEnvironment()
			environment[test.env] = test.value
			_, _, err := loadManagedStorageR2Config(environmentLookup(environment))
			if err == nil {
				t.Fatal("loadManagedStorageR2Config() succeeded for unsafe configuration")
			}
		})
	}
}

func TestBuildManagedStorageRuntimeRejectsSelfHostedEnable(t *testing.T) {
	t.Parallel()

	factoryCalled := false
	runtime, err := buildManagedStorageRuntime(
		context.Background(),
		nil,
		false,
		environmentLookup(validManagedStorageEnvironment()),
		func(context.Context, s3store.Config) (storagepkg.ObjectStore, error) {
			factoryCalled = true
			return nil, errors.New("must not be called")
		},
	)
	if err == nil || runtime != nil || factoryCalled {
		t.Fatalf("buildManagedStorageRuntime() runtime = %#v, error = %v, factoryCalled = %v", runtime, err, factoryCalled)
	}
}

func TestBuildManagedStorageRuntimeWiresHardenedR2Cell(t *testing.T) {
	t.Parallel()

	baseStore := &managedStorageTestStore{}
	var captured s3store.Config
	db := sqlx.NewDb(&sql.DB{}, "postgres")
	runtime, err := buildManagedStorageRuntime(
		context.Background(),
		db,
		true,
		environmentLookup(validManagedStorageEnvironment()),
		func(_ context.Context, config s3store.Config) (storagepkg.ObjectStore, error) {
			captured = config
			return baseStore, nil
		},
	)
	if err != nil {
		t.Fatalf("buildManagedStorageRuntime() error = %v", err)
	}
	if runtime == nil || runtime.defaults == nil || runtime.resolver == nil {
		t.Fatalf("buildManagedStorageRuntime() runtime = %#v", runtime)
	}
	if captured.Endpoint != validManagedStorageEnvironment()[managedStorageR2EndpointEnv] ||
		captured.Region != "auto" ||
		captured.Bucket != "everstack-storage-prod-eu-001" ||
		captured.AccessKeyID != "platform-access-key" ||
		captured.SecretAccessKey != "platform-secret-key" {
		t.Fatalf("S3 config = %#v", captured)
	}
	if !captured.ForcePathStyle || !captured.DisableNativeCopy || !captured.EnforceManagedEgress ||
		captured.WireChecksum != s3store.WireChecksumContentMD5 || captured.PathPrefix != "" {
		t.Fatalf("S3 hardening config = %#v", captured)
	}

	tenantID := "tenant-a"
	store, err := runtime.resolver.ResolveManagedStore(context.Background(), storagepkg.ManagedConnection{
		TenantID:   tenantID,
		CellID:     "r2-eu-prod-001",
		PathPrefix: storagepkg.ManagedTenantPrefix(tenantID),
	})
	if err != nil {
		t.Fatalf("ResolveManagedStore() error = %v", err)
	}
	if _, err := store.Put(context.Background(), "attacker-bucket", "objects/a", "text/plain", strings.NewReader("content")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if baseStore.bucket != "everstack-storage-prod-eu-001" || baseStore.key != storagepkg.ManagedTenantPrefix(tenantID)+"/objects/a" {
		t.Fatalf("base store received bucket = %q, key = %q", baseStore.bucket, baseStore.key)
	}
}

func TestBuildManagedStorageRuntimeRequiresDatabaseAndStore(t *testing.T) {
	t.Parallel()

	environment := environmentLookup(validManagedStorageEnvironment())
	if _, err := buildManagedStorageRuntime(context.Background(), nil, true, environment, func(context.Context, s3store.Config) (storagepkg.ObjectStore, error) {
		return &managedStorageTestStore{}, nil
	}); err == nil {
		t.Fatal("buildManagedStorageRuntime() succeeded without a database")
	}

	db := sqlx.NewDb(&sql.DB{}, "postgres")
	if _, err := buildManagedStorageRuntime(context.Background(), db, true, environment, func(context.Context, s3store.Config) (storagepkg.ObjectStore, error) {
		return nil, errors.New("cell construction failed")
	}); err == nil {
		t.Fatal("buildManagedStorageRuntime() succeeded after store construction failed")
	}
}

func TestBuildManagedStorageRuntimeProducesTenantScopedR2Presign(t *testing.T) {
	t.Parallel()

	db := sqlx.NewDb(&sql.DB{}, "postgres")
	runtime, err := buildManagedStorageRuntime(
		context.Background(),
		db,
		true,
		environmentLookup(validManagedStorageEnvironment()),
		defaultManagedStorageS3Factory,
	)
	if err != nil {
		t.Fatalf("buildManagedStorageRuntime() error = %v", err)
	}

	tenantID := "tenant-a"
	store, err := runtime.resolver.ResolveManagedStore(context.Background(), storagepkg.ManagedConnection{
		TenantID:   tenantID,
		CellID:     "r2-eu-prod-001",
		PathPrefix: storagepkg.ManagedTenantPrefix(tenantID),
	})
	if err != nil {
		t.Fatalf("ResolveManagedStore() error = %v", err)
	}
	presigned, _, err := store.PutPresignedURL(context.Background(), "attacker-bucket", "objects/a", "text/plain", 7, time.Minute)
	if err != nil {
		t.Fatalf("PutPresignedURL() error = %v", err)
	}
	parsed, err := url.Parse(presigned)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	wantHost := "0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com"
	wantPath := "/everstack-storage-prod-eu-001/" + storagepkg.ManagedTenantPrefix(tenantID) + "/objects/a"
	if parsed.Host != wantHost || parsed.Path != wantPath {
		t.Fatalf("presigned target = https://%s%s, want https://%s%s", parsed.Host, parsed.Path, wantHost, wantPath)
	}
	if strings.Contains(presigned, "platform-secret-key") {
		t.Fatal("presigned URL leaked the platform secret key")
	}
}
