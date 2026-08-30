package managed

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	storagepkg "github.com/everstacklabs/everstack/internal/storage"
)

type storeCall struct {
	operation string
	bucket    string
	key       string
	prefix    string
}

type recordingStore struct {
	calls       []storeCall
	listObjects []storagepkg.BucketObject
	pageObjects []storagepkg.ObjectInfo
	listErr     error
}

type legacyStore struct {
	storagepkg.ObjectStore
}

func (s *recordingStore) record(operation, bucket, key, prefix string) {
	s.calls = append(s.calls, storeCall{operation: operation, bucket: bucket, key: key, prefix: prefix})
}

func (s *recordingStore) PutPresignedURL(_ context.Context, bucket, key, _ string, _ int64, _ time.Duration) (string, map[string]string, error) {
	s.record("put-presigned", bucket, key, "")
	return "https://example.invalid/upload", nil, nil
}

func (s *recordingStore) GetPresignedURL(_ context.Context, bucket, key string, _ time.Duration) (string, error) {
	s.record("get-presigned", bucket, key, "")
	return "https://example.invalid/download", nil
}

func (s *recordingStore) Put(_ context.Context, bucket, key, _ string, _ io.Reader) (string, error) {
	s.record("put", bucket, key, "")
	return "etag", nil
}

func (s *recordingStore) Delete(_ context.Context, bucket, key string) error {
	s.record("delete", bucket, key, "")
	return nil
}

func (s *recordingStore) Head(_ context.Context, bucket, key string) (int64, string, error) {
	s.record("head", bucket, key, "")
	return 7, "text/plain", nil
}

func (s *recordingStore) List(_ context.Context, bucket, prefix string) ([]storagepkg.BucketObject, error) {
	s.record("list", bucket, "", prefix)
	return s.listObjects, s.listErr
}

func (s *recordingStore) BlobCapabilities() storagepkg.BlobCapabilities {
	return storagepkg.BlobCapabilities{ContractVersion: storagepkg.BlobPlaneV2, DirectRead: true}
}

func (s *recordingStore) Get(_ context.Context, bucket, key string) (*storagepkg.ObjectReader, error) {
	s.record("get", bucket, key, "")
	return &storagepkg.ObjectReader{
		ObjectInfo: storagepkg.ObjectInfo{Key: key},
		Body:       io.NopCloser(strings.NewReader("content")),
	}, nil
}

func (s *recordingStore) ListPage(_ context.Context, bucket string, options storagepkg.ListOptions) (storagepkg.ObjectPage, error) {
	s.record("list-page", bucket, "", options.Prefix)
	return storagepkg.ObjectPage{Objects: s.pageObjects, NextCursor: "next"}, nil
}

func (s *recordingStore) PutIfAbsent(_ context.Context, bucket, key string, _ io.Reader, _ storagepkg.PutOptions) (storagepkg.ObjectInfo, error) {
	s.record("put-if-absent", bucket, key, "")
	return storagepkg.ObjectInfo{Key: key}, nil
}

func (s *recordingStore) CopyIfAbsent(_ context.Context, sourceBucket, sourceKey, destinationBucket, destinationKey string) (storagepkg.CopyResult, error) {
	s.record("copy-source", sourceBucket, sourceKey, "")
	s.record("copy-destination", destinationBucket, destinationKey, "")
	return storagepkg.CopyResult{ObjectInfo: storagepkg.ObjectInfo{Key: destinationKey}}, nil
}

func (s *recordingStore) BeginMultipart(_ context.Context, bucket, key string, options storagepkg.MultipartOptions) (storagepkg.MultipartUpload, error) {
	s.record("begin-multipart", bucket, key, "")
	return storagepkg.MultipartUpload{Bucket: bucket, Key: key, ID: "upload-1", Options: options}, nil
}

func (s *recordingStore) UploadPart(_ context.Context, upload storagepkg.MultipartUpload, _ int32, _ io.Reader, _ storagepkg.PutOptions) (storagepkg.UploadedPart, error) {
	s.record("upload-part", upload.Bucket, upload.Key, "")
	return storagepkg.UploadedPart{Number: 1, ETag: "etag", Checksum: storagepkg.NewSHA256Checksum([]byte("content"))}, nil
}

func (s *recordingStore) CompleteMultipart(_ context.Context, upload storagepkg.MultipartUpload, _ []storagepkg.UploadedPart) (storagepkg.ObjectInfo, error) {
	s.record("complete-multipart", upload.Bucket, upload.Key, "")
	return storagepkg.ObjectInfo{Key: upload.Key}, nil
}

func (s *recordingStore) AbortMultipart(_ context.Context, upload storagepkg.MultipartUpload) error {
	s.record("abort-multipart", upload.Bucket, upload.Key, "")
	return nil
}

func TestNewResolverRejectsInvalidCells(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	tests := []struct {
		name  string
		cells []Cell
	}{
		{name: "no cells"},
		{name: "empty cell id", cells: []Cell{{Bucket: "cell-bucket", Store: store}}},
		{name: "empty bucket", cells: []Cell{{ID: "r2-eu-001", Store: store}}},
		{name: "nil store", cells: []Cell{{ID: "r2-eu-001", Bucket: "cell-bucket"}}},
		{name: "legacy store", cells: []Cell{{ID: "r2-eu-001", Bucket: "cell-bucket", Store: legacyStore{ObjectStore: store}}}},
		{name: "duplicate cell id", cells: []Cell{{ID: "r2-eu-001", Bucket: "cell-bucket", Store: store}, {ID: " r2-eu-001 ", Bucket: "cell-bucket", Store: store}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewResolver(test.cells...); err == nil {
				t.Fatal("NewResolver() succeeded for invalid cell configuration")
			}
		})
	}
}

func TestResolverRejectsUnknownCellAndInvalidTenantPlacement(t *testing.T) {
	t.Parallel()

	resolver, err := NewResolver(Cell{ID: "r2-eu-001", Bucket: "cell-bucket", Store: &recordingStore{}})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	tenantID := "tenant-a"
	tests := []struct {
		name       string
		connection storagepkg.ManagedConnection
	}{
		{
			name: "unknown cell",
			connection: storagepkg.ManagedConnection{
				TenantID: tenantID, CellID: "r2-eu-999", PathPrefix: storagepkg.ManagedTenantPrefix(tenantID),
			},
		},
		{
			name: "empty tenant",
			connection: storagepkg.ManagedConnection{
				CellID: "r2-eu-001", PathPrefix: storagepkg.ManagedTenantPrefix(tenantID),
			},
		},
		{
			name: "cross tenant prefix",
			connection: storagepkg.ManagedConnection{
				TenantID: tenantID, CellID: "r2-eu-001", PathPrefix: storagepkg.ManagedTenantPrefix("tenant-b"),
			},
		},
		{
			name: "unnormalized prefix",
			connection: storagepkg.ManagedConnection{
				TenantID: tenantID, CellID: "r2-eu-001", PathPrefix: storagepkg.ManagedTenantPrefix(tenantID) + "/",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := resolver.ResolveManagedStore(context.Background(), test.connection); err == nil {
				t.Fatal("ResolveManagedStore() succeeded for invalid placement")
			}
		})
	}
}

func TestResolvedStorePinsBucketAndTenantPrefixForEveryOperation(t *testing.T) {
	t.Parallel()

	base := &recordingStore{}
	resolver, err := NewResolver(Cell{ID: "r2-eu-001", Bucket: "cell-bucket", Store: base})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	tenantID := "tenant-a"
	physicalPrefix := storagepkg.ManagedTenantPrefix(tenantID)
	store, err := resolver.ResolveManagedStore(context.Background(), storagepkg.ManagedConnection{
		TenantID: tenantID, CellID: "r2-eu-001", PathPrefix: physicalPrefix,
	})
	if err != nil {
		t.Fatalf("ResolveManagedStore() error = %v", err)
	}

	ctx := context.Background()
	if _, _, err := store.PutPresignedURL(ctx, "attacker-bucket", "objects/a", "text/plain", 7, time.Minute); err != nil {
		t.Fatalf("PutPresignedURL() error = %v", err)
	}
	if _, err := store.GetPresignedURL(ctx, "attacker-bucket", "objects/b", time.Minute); err != nil {
		t.Fatalf("GetPresignedURL() error = %v", err)
	}
	if _, err := store.Put(ctx, "attacker-bucket", "objects/c", "text/plain", strings.NewReader("content")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if err := store.Delete(ctx, "attacker-bucket", "objects/d"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, _, err := store.Head(ctx, "attacker-bucket", "objects/e"); err != nil {
		t.Fatalf("Head() error = %v", err)
	}

	wantKeys := []string{"objects/a", "objects/b", "objects/c", "objects/d", "objects/e"}
	if len(base.calls) != len(wantKeys) {
		t.Fatalf("underlying call count = %d, want %d", len(base.calls), len(wantKeys))
	}
	for i, call := range base.calls {
		if call.bucket != "cell-bucket" {
			t.Fatalf("call %d bucket = %q, want fixed configured bucket", i, call.bucket)
		}
		wantKey := physicalPrefix + "/" + wantKeys[i]
		if call.key != wantKey {
			t.Fatalf("call %d key = %q, want %q", i, call.key, wantKey)
		}
	}
}

func TestResolvedStoreListsOnlyTenantScopedKeys(t *testing.T) {
	t.Parallel()

	tenantID := "tenant-a"
	physicalPrefix := storagepkg.ManagedTenantPrefix(tenantID)
	base := &recordingStore{listObjects: []storagepkg.BucketObject{
		{Key: physicalPrefix + "/objects/a", SizeBytes: 1},
		{Key: physicalPrefix + "/objects/b", SizeBytes: 2},
	}}
	resolver, err := NewResolver(Cell{ID: "r2-eu-001", Bucket: "cell-bucket", Store: base})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	store, err := resolver.ResolveManagedStore(context.Background(), storagepkg.ManagedConnection{
		TenantID: tenantID, CellID: "r2-eu-001", PathPrefix: physicalPrefix,
	})
	if err != nil {
		t.Fatalf("ResolveManagedStore() error = %v", err)
	}

	objects, err := store.List(context.Background(), "attacker-bucket", "objects/")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(base.calls) != 1 || base.calls[0].bucket != "cell-bucket" || base.calls[0].prefix != physicalPrefix+"/objects/" {
		t.Fatalf("underlying list call = %#v", base.calls)
	}
	if len(objects) != 2 || objects[0].Key != "objects/a" || objects[1].Key != "objects/b" {
		t.Fatalf("List() objects = %#v", objects)
	}
}

func TestResolvedStoreFailsClosedWhenProviderReturnsCrossTenantKey(t *testing.T) {
	t.Parallel()

	tenantID := "tenant-a"
	physicalPrefix := storagepkg.ManagedTenantPrefix(tenantID)
	base := &recordingStore{listObjects: []storagepkg.BucketObject{{
		Key: storagepkg.ManagedTenantPrefix("tenant-b") + "/objects/a",
	}}}
	resolver, err := NewResolver(Cell{ID: "r2-eu-001", Bucket: "cell-bucket", Store: base})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	store, err := resolver.ResolveManagedStore(context.Background(), storagepkg.ManagedConnection{
		TenantID: tenantID, CellID: "r2-eu-001", PathPrefix: physicalPrefix,
	})
	if err != nil {
		t.Fatalf("ResolveManagedStore() error = %v", err)
	}

	_, err = store.List(context.Background(), "", "")
	if err == nil || !errors.Is(err, ErrTenantScopeViolation) {
		t.Fatalf("List() error = %v, want ErrTenantScopeViolation", err)
	}
}

func TestResolvedStorePreservesAndScopesBlobPlaneV2(t *testing.T) {
	t.Parallel()

	tenantID := "tenant-a"
	physicalPrefix := storagepkg.ManagedTenantPrefix(tenantID)
	base := &recordingStore{pageObjects: []storagepkg.ObjectInfo{{Key: physicalPrefix + "/objects/listed"}}}
	resolver, err := NewResolver(Cell{ID: "r2-eu-001", Bucket: "cell-bucket", Store: base})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	resolved, err := resolver.ResolveManagedStore(context.Background(), storagepkg.ManagedConnection{
		TenantID: tenantID, CellID: "r2-eu-001", PathPrefix: physicalPrefix,
	})
	if err != nil {
		t.Fatalf("ResolveManagedStore() error = %v", err)
	}
	blob, err := storagepkg.RequireBlobPlane(resolved)
	if err != nil {
		t.Fatalf("RequireBlobPlane() error = %v", err)
	}
	if blob.BlobCapabilities().ContractVersion != storagepkg.BlobPlaneV2 {
		t.Fatalf("BlobCapabilities() = %#v", blob.BlobCapabilities())
	}

	ctx := context.Background()
	reader, err := blob.Get(ctx, "attacker-bucket", "objects/read")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if reader.Key != "objects/read" {
		t.Fatalf("Get() key = %q", reader.Key)
	}
	_ = reader.Body.Close()

	page, err := blob.ListPage(ctx, "attacker-bucket", storagepkg.ListOptions{Prefix: "objects/", Cursor: "cursor", PageSize: 10})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "objects/listed" || page.NextCursor != "next" {
		t.Fatalf("ListPage() = %#v", page)
	}

	put, err := blob.PutIfAbsent(ctx, "attacker-bucket", "objects/write", strings.NewReader("content"), storagepkg.PutOptions{})
	if err != nil || put.Key != "objects/write" {
		t.Fatalf("PutIfAbsent() = %#v, error = %v", put, err)
	}
	copyResult, err := blob.CopyIfAbsent(ctx, "attacker-source", "objects/source", "attacker-destination", "objects/copied")
	if err != nil || copyResult.Key != "objects/copied" {
		t.Fatalf("CopyIfAbsent() = %#v, error = %v", copyResult, err)
	}

	upload, err := blob.BeginMultipart(ctx, "attacker-bucket", "objects/multipart", storagepkg.MultipartOptions{})
	if err != nil {
		t.Fatalf("BeginMultipart() error = %v", err)
	}
	if upload.Bucket != "" || upload.Key != "objects/multipart" || upload.ID != "upload-1" {
		t.Fatalf("BeginMultipart() = %#v", upload)
	}
	upload.Bucket = "attacker-bucket"
	part, err := blob.UploadPart(ctx, upload, 1, strings.NewReader("content"), storagepkg.PutOptions{})
	if err != nil {
		t.Fatalf("UploadPart() error = %v", err)
	}
	completed, err := blob.CompleteMultipart(ctx, upload, []storagepkg.UploadedPart{part})
	if err != nil || completed.Key != "objects/multipart" {
		t.Fatalf("CompleteMultipart() = %#v, error = %v", completed, err)
	}
	if err := blob.AbortMultipart(ctx, upload); err != nil {
		t.Fatalf("AbortMultipart() error = %v", err)
	}

	wantKeys := []string{
		"objects/read",
		"",
		"objects/write",
		"objects/source",
		"objects/copied",
		"objects/multipart",
		"objects/multipart",
		"objects/multipart",
		"objects/multipart",
	}
	if len(base.calls) != len(wantKeys) {
		t.Fatalf("underlying Blob Plane call count = %d, want %d: %#v", len(base.calls), len(wantKeys), base.calls)
	}
	for index, call := range base.calls {
		if call.bucket != "cell-bucket" {
			t.Fatalf("call %d bucket = %q, want cell-bucket", index, call.bucket)
		}
		wantKey := wantKeys[index]
		if call.operation == "list-page" {
			if call.prefix != physicalPrefix+"/objects/" {
				t.Fatalf("list-page prefix = %q", call.prefix)
			}
			continue
		}
		if call.key != physicalPrefix+"/"+wantKey {
			t.Fatalf("call %d (%s) key = %q, want %q", index, call.operation, call.key, physicalPrefix+"/"+wantKey)
		}
	}
}
