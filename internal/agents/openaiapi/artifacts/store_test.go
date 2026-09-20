package artifacts

import (
	"context"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

type fakeUploader struct {
	params storage.InitiateUploadParams
	calls  int
}

func (f *fakeUploader) Upload(_ context.Context, p storage.InitiateUploadParams, _ io.Reader) (*storage.Upload, string, error) {
	f.params = p
	f.calls++
	return &storage.Upload{ObjectID: p.ObjectID, Filename: p.Filename, ContentType: p.ContentType, ActualSizeBytes: p.ExpectedSizeBytes, ActualChecksumSHA256: p.ExpectedChecksumSHA256, CreatedAt: p.Now}, "", nil
}

type fakeObjects struct{ deletes int }

func (*fakeObjects) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, nil
}
func (*fakeObjects) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "https://download.invalid", nil
}
func (*fakeObjects) Put(context.Context, string, string, string, io.Reader) (string, error) {
	return "", nil
}
func (f *fakeObjects) Delete(context.Context, string, string) error              { f.deletes++; return nil }
func (*fakeObjects) Head(context.Context, string, string) (int64, string, error) { return 0, "", nil }
func (*fakeObjects) List(context.Context, string, string) ([]storage.BucketObject, error) {
	return nil, nil
}

type fakeLifecycle struct {
	begin, complete int
	already         bool
}

func (f *fakeLifecycle) BeginDeletion(_ context.Context, tenant, id string, _ time.Time) (*storage.Upload, bool, error) {
	f.begin++
	return &storage.Upload{ObjectID: id, TenantID: tenant, Key: "key", State: storage.UploadStateDeleting}, f.already, nil
}
func (*fakeLifecycle) FailDeletion(context.Context, string, string, string, time.Time, time.Time) (*storage.Upload, error) {
	return nil, nil
}
func (f *fakeLifecycle) CompleteDeletion(context.Context, string, string, time.Time) (*storage.Upload, bool, error) {
	f.complete++
	return &storage.Upload{State: storage.UploadStateDeleted}, false, nil
}

func scope() Scope {
	return Scope{OrganizationID: "org-a", InstanceID: "inst-a", SessionID: "sess-a", SandboxID: "sbx-a"}
}
func authctx() context.Context { return storageauth.WithSystemPrincipal(context.Background(), "org-a") }

func TestUploadInputUsesResolvedBackendAndQuotaLifecycle(t *testing.T) {
	uploader := &fakeUploader{}
	objects := &fakeObjects{}
	resolved := 0
	s := NewStore(nil, nil, ResolverFunc(func(_ context.Context, got Scope) (Backend, error) {
		resolved++
		if got != scope() {
			t.Fatalf("scope=%+v", got)
		}
		return Backend{Objects: objects, Uploader: uploader, Bucket: "bucket-a", ConfigID: "cfg-a"}, nil
	}))
	s.quota = func(context.Context) int64 { return 42 }
	got, err := s.UploadInput(authctx(), scope(), "notes.txt", "text/plain", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got.SizeBytes != 5 || uploader.calls != 1 || resolved != 1 {
		t.Fatalf("upload=%+v calls=%d resolved=%d", got, uploader.calls, resolved)
	}
	p := uploader.params
	if p.TenantID != "org-a" || p.ConfigID != "cfg-a" || p.ReferenceID != "sess-a" || p.ReferenceType != referenceType || p.QuotaBytes != 42 {
		t.Fatalf("params=%+v", p)
	}
	if p.Purpose != "upload" || got.Kind != "input" {
		t.Fatalf("storage purpose=%q API kind=%q", p.Purpose, got.Kind)
	}
	if string(p.Metadata) == "" || !regexp.MustCompile(`"openai_instance_id":"inst-a"`).Match(p.Metadata) {
		t.Fatalf("metadata=%s", p.Metadata)
	}
}

func TestUploadRejectsForeignTenantBeforeResolver(t *testing.T) {
	called := false
	s := NewStore(nil, nil, ResolverFunc(func(context.Context, Scope) (Backend, error) { called = true; return Backend{}, nil }))
	_, err := s.UploadInput(storageauth.WithSystemPrincipal(context.Background(), "org-b"), scope(), "x.txt", "", []byte("x"))
	if err == nil || called {
		t.Fatalf("err=%v resolverCalled=%v", err, called)
	}
}

func TestDeleteIsIdempotentThroughLifecycleAndKeepsOwnershipFilter(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	db := sqlx.NewDb(raw, "sqlmock")
	objects := &fakeObjects{}
	life := &fakeLifecycle{}
	s := NewStore(db, life, ResolverFunc(func(context.Context, Scope) (Backend, error) {
		return Backend{Objects: objects, ConfigID: "cfg-a", Bucket: "bucket-a"}, nil
	}))
	rows := sqlmock.NewRows([]string{"id", "key", "filename", "content_type", "size_bytes", "checksum_sha256", "purpose", "created_at"}).AddRow("obj-a", "key", "a.txt", "text/plain", 1, digest([]byte("x")), "artifact", time.Now())
	mock.ExpectQuery("SELECT id, key, filename").WithArgs("obj-a", "org-a", "cfg-a", referenceType, "sess-a", "inst-a", "sbx-a").WillReturnRows(rows)
	if err := s.Delete(authctx(), scope(), "obj-a"); err != nil {
		t.Fatal(err)
	}
	if objects.deletes != 1 || life.begin != 1 || life.complete != 1 {
		t.Fatalf("delete=%d begin=%d complete=%d", objects.deletes, life.begin, life.complete)
	}
	life.already = true
	rows2 := sqlmock.NewRows([]string{"id", "key", "filename", "content_type", "size_bytes", "checksum_sha256", "purpose", "created_at"}).AddRow("obj-a", "key", "a.txt", "text/plain", 1, digest([]byte("x")), "artifact", time.Now())
	mock.ExpectQuery("SELECT id, key, filename").WithArgs("obj-a", "org-a", "cfg-a", referenceType, "sess-a", "inst-a", "sbx-a").WillReturnRows(rows2)
	if err := s.Delete(authctx(), scope(), "obj-a"); err != nil {
		t.Fatal(err)
	}
	if objects.deletes != 1 || life.complete != 1 {
		t.Fatal("idempotent replay repeated provider delete or accounting completion")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAllUsesLifecycleForEveryRetainedObject(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	db := sqlx.NewDb(raw, "sqlmock")
	objects := &fakeObjects{}
	life := &fakeLifecycle{}
	s := NewStore(db, life, ResolverFunc(func(context.Context, Scope) (Backend, error) {
		return Backend{Objects: objects, ConfigID: "cfg-a", Bucket: "bucket-a"}, nil
	}))
	now := time.Now()
	listRows := sqlmock.NewRows([]string{"id", "key", "filename", "content_type", "size_bytes", "checksum_sha256", "purpose", "created_at"}).
		AddRow("input-a", "input-key", "in.txt", "text/plain", 1, digest([]byte("i")), "input", now).
		AddRow("artifact-a", "artifact-key", "out.txt", "text/plain", 1, digest([]byte("o")), "artifact", now)
	mock.ExpectQuery("SELECT id, key, filename").
		WithArgs("org-a", "cfg-a", referenceType, "sess-a", "inst-a", "sbx-a").
		WillReturnRows(listRows)
	for _, row := range []struct{ id, key, name, purpose, checksum string }{
		{"input-a", "input-key", "in.txt", "input", digest([]byte("i"))},
		{"artifact-a", "artifact-key", "out.txt", "artifact", digest([]byte("o"))},
	} {
		lookup := sqlmock.NewRows([]string{"id", "key", "filename", "content_type", "size_bytes", "checksum_sha256", "purpose", "created_at"}).
			AddRow(row.id, row.key, row.name, "text/plain", 1, row.checksum, row.purpose, now)
		mock.ExpectQuery("SELECT id, key, filename").
			WithArgs(row.id, "org-a", "cfg-a", referenceType, "sess-a", "inst-a", "sbx-a").
			WillReturnRows(lookup)
	}
	if err := s.DeleteAll(authctx(), scope()); err != nil {
		t.Fatal(err)
	}
	if objects.deletes != 2 || life.begin != 2 || life.complete != 2 {
		t.Fatalf("provider deletes=%d lifecycle begin=%d complete=%d", objects.deletes, life.begin, life.complete)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
