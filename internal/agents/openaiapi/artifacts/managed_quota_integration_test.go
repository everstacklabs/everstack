package artifacts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type quotaBlob struct {
	mu         sync.Mutex
	data       map[string][]byte
	failDelete bool
	deletes    int
}

func (b *quotaBlob) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, nil
}
func (b *quotaBlob) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "https://invalid", nil
}
func (b *quotaBlob) Put(_ context.Context, _, key, _ string, body io.Reader) (string, error) {
	v, e := io.ReadAll(body)
	if e == nil {
		b.mu.Lock()
		b.data[key] = v
		b.mu.Unlock()
	}
	return "etag", e
}
func (b *quotaBlob) Delete(_ context.Context, _, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletes++
	if b.failDelete {
		b.failDelete = false
		return storage.NewOperationError("delete", storage.ErrorUnavailable, true, "", 0)
	}
	delete(b.data, key)
	return nil
}
func (b *quotaBlob) Head(_ context.Context, _, key string) (int64, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.data[key]
	if !ok {
		return 0, "", storage.NewOperationError("head", storage.ErrorNotFound, false, "", 0)
	}
	return int64(len(v)), "text/plain", nil
}
func (b *quotaBlob) List(context.Context, string, string) ([]storage.BucketObject, error) {
	return nil, nil
}
func (*quotaBlob) BlobCapabilities() storage.BlobCapabilities {
	return storage.BlobCapabilities{ContractVersion: storage.BlobPlaneV2, DirectRead: true}
}
func (b *quotaBlob) Get(_ context.Context, _, key string) (*storage.ObjectReader, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.data[key]
	if !ok {
		return nil, storage.NewOperationError("get", storage.ErrorNotFound, false, "", 0)
	}
	copy := append([]byte(nil), v...)
	return &storage.ObjectReader{ObjectInfo: storage.ObjectInfo{Key: key, SizeBytes: int64(len(copy))}, Body: io.NopCloser(bytes.NewReader(copy))}, nil
}
func (*quotaBlob) ListPage(context.Context, string, storage.ListOptions) (storage.ObjectPage, error) {
	return storage.ObjectPage{}, nil
}
func (*quotaBlob) PutIfAbsent(context.Context, string, string, io.Reader, storage.PutOptions) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, errors.New("unused")
}
func (*quotaBlob) CopyIfAbsent(context.Context, string, string, string, string) (storage.CopyResult, error) {
	return storage.CopyResult{}, errors.New("unused")
}
func (*quotaBlob) BeginMultipart(context.Context, string, string, storage.MultipartOptions) (storage.MultipartUpload, error) {
	return storage.MultipartUpload{}, errors.New("unused")
}
func (*quotaBlob) UploadPart(context.Context, storage.MultipartUpload, int32, io.Reader, storage.PutOptions) (storage.UploadedPart, error) {
	return storage.UploadedPart{}, errors.New("unused")
}
func (*quotaBlob) CompleteMultipart(context.Context, storage.MultipartUpload, []storage.UploadedPart) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, errors.New("unused")
}
func (*quotaBlob) AbortMultipart(context.Context, storage.MultipartUpload) error {
	return errors.New("unused")
}

func TestManagedDefaultQuotaAndRetryableArtifactCleanup(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN is not set")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := "test_openai_artifact_quota_" + regexp.MustCompile(`-`).ReplaceAllString(uuid.NewString(), "")
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	if _, err = db.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE SCHEMA IF NOT EXISTS everstack`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../../../database/migrations/sql/postgres/tenant_volume_buckets_20260530173710/up.sql", "../../../database/migrations/sql/postgres/object_storage_init_20260226001900/up.sql", "../../../database/migrations/sql/postgres/rls_tenant_isolation_20260507141542/up.sql", "../../../database/migrations/sql/postgres/object_storage_credentials_20260808090000/up.sql", "../../../database/migrations/sql/postgres/storage_upload_lifecycle_20260811100000/up.sql", "../../../database/migrations/sql/postgres/managed_storage_default_20260816000000/up.sql"} {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(b)); e != nil {
			t.Fatalf("migration %s: %v", path, e)
		}
	}
	tenant := "tenant-" + uuid.NewString()
	defaults, err := storage.NewPostgresManagedDefaults(db, "r2-eu-test-001")
	if err != nil {
		t.Fatal(err)
	}
	managed, err := defaults.EnsureDefault(context.Background(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	blob := &quotaBlob{data: map[string][]byte{}}
	lifecycle := storage.NewPostgresUploadLifecycle(db)
	uploader := storage.NewDirectUploader(blob, lifecycle, "logical")
	s := NewFixedStore(db, blob, uploader, lifecycle, "logical", managed.ConfigID)
	quota := int64(3)
	s.quota = func(context.Context) int64 { return quota }
	ctx := storageauth.WithSystemPrincipal(context.Background(), tenant)
	sc := Scope{OrganizationID: tenant, InstanceID: "instance", SessionID: "session", SandboxID: "sandbox"}
	content := []byte("five!")
	_, err = s.UploadInput(ctx, sc, "input.txt", "text/plain", content)
	var quotaErr *storage.QuotaExceededError
	if !errors.As(err, &quotaErr) {
		t.Fatalf("quota error=%v", err)
	}
	var uploads, total, reserved, count int64
	if err = db.Get(&uploads, `SELECT count(*) FROM object_storage_uploads WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COALESCE(MAX(total_bytes),0),COALESCE(MAX(reserved_bytes),0),COALESCE(MAX(object_count),0) FROM object_storage_usage WHERE tenant_id=$1`, tenant).Scan(&total, &reserved, &count); err != nil {
		t.Fatal(err)
	}
	if uploads != 0 || total != 0 || reserved != 0 || count != 0 || len(blob.data) != 0 {
		t.Fatalf("quota leak uploads=%d total=%d reserved=%d count=%d objects=%d", uploads, total, reserved, count, len(blob.data))
	}
	quota = 100
	artifact, err := s.UploadInput(ctx, sc, "input.txt", "text/plain", content)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := s.List(ctx, sc)
	if err != nil || len(listed) != 1 || listed[0].ObjectID != artifact.ObjectID || listed[0].Kind != "input" {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	download, err := s.Download(ctx, sc, artifact.ObjectID, time.Minute)
	if err != nil || download.Kind != "input" || download.Filename != "input.txt" || download.SizeBytes != int64(len(content)) {
		t.Fatalf("download=%+v err=%v", download, err)
	}
	inputRequest := executor.InputRequest{Ownership: executor.Ownership{OrganizationID: tenant, InstanceID: sc.InstanceID}, LocalSessionID: sc.SessionID, SandboxID: sc.SandboxID, ReferenceID: artifact.ObjectID}
	resolvedInput, err := s.ResolveInput(ctx, inputRequest)
	if err != nil || !bytes.Equal(resolvedInput.Content, content) || resolvedInput.SHA256 != artifact.SHA256 {
		t.Fatalf("resolved input=%+v err=%v", resolvedInput, err)
	}
	foreignInstance := inputRequest
	foreignInstance.InstanceID = "foreign-instance"
	if _, err = s.ResolveInput(ctx, foreignInstance); err == nil {
		t.Fatal("foreign instance resolved retained input")
	}
	foreignSandbox := inputRequest
	foreignSandbox.SandboxID = "foreign-sandbox"
	if _, err = s.ResolveInput(ctx, foreignSandbox); err == nil {
		t.Fatal("foreign sandbox resolved retained input")
	}
	blob.failDelete = true
	if err = s.Delete(ctx, sc, artifact.ObjectID); err == nil {
		t.Fatal("provider delete failure was hidden")
	}
	if err = db.QueryRow(`SELECT total_bytes,reserved_bytes,object_count FROM object_storage_usage WHERE tenant_id=$1`, tenant).Scan(&total, &reserved, &count); err != nil {
		t.Fatal(err)
	}
	if total != int64(len(content)) || reserved != 0 || count != 1 {
		t.Fatalf("failed cleanup refunded usage total=%d reserved=%d count=%d", total, reserved, count)
	}
	if _, err = db.Exec(`UPDATE object_storage_uploads SET next_attempt_at=clock_timestamp()-INTERVAL '1 second' WHERE tenant_id=$1 AND id=$2`, tenant, artifact.ObjectID); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, sc, artifact.ObjectID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT total_bytes,reserved_bytes,object_count FROM object_storage_usage WHERE tenant_id=$1`, tenant).Scan(&total, &reserved, &count); err != nil {
		t.Fatal(err)
	}
	if total != 0 || reserved != 0 || count != 0 || len(blob.data) != 0 || blob.deletes != 2 {
		t.Fatalf("cleanup total=%d reserved=%d count=%d objects=%d deletes=%d", total, reserved, count, len(blob.data), blob.deletes)
	}
}
