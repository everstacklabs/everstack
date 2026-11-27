package snapshot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	s3pkg "github.com/everstacklabs/everstack/internal/storage/s3"
)

// TestWorkspaceStoreAgainstS3 drives the real s3 adapter (multipart upload,
// Content-MD5 wire checksums as configured for R2, post-upload re-read
// verification, direct GET) against an S3-compatible endpoint. Skipped
// unless EVS_SANDBOX_WORKSPACE_S3_TEST_ENDPOINT is set, e.g. a local MinIO:
//
//	EVS_SANDBOX_WORKSPACE_S3_TEST_ENDPOINT=http://127.0.0.1:19000 \
//	EVS_SANDBOX_WORKSPACE_S3_TEST_BUCKET=sbx \
//	EVS_SANDBOX_WORKSPACE_S3_TEST_ACCESS_KEY=... \
//	EVS_SANDBOX_WORKSPACE_S3_TEST_SECRET_KEY=... \
//	go test ./internal/sandbox/snapshot -run TestWorkspaceStoreAgainstS3
func TestWorkspaceStoreAgainstS3(t *testing.T) {
	endpoint := os.Getenv("EVS_SANDBOX_WORKSPACE_S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("EVS_SANDBOX_WORKSPACE_S3_TEST_ENDPOINT not set")
	}
	bucket := os.Getenv("EVS_SANDBOX_WORKSPACE_S3_TEST_BUCKET")
	ctx := context.Background()
	store, err := s3pkg.New(ctx, s3pkg.Config{
		Endpoint:        endpoint,
		Region:          "us-east-1",
		Bucket:          bucket,
		AccessKeyID:     os.Getenv("EVS_SANDBOX_WORKSPACE_S3_TEST_ACCESS_KEY"),
		SecretAccessKey: os.Getenv("EVS_SANDBOX_WORKSPACE_S3_TEST_SECRET_KEY"),
		ForcePathStyle:  true,
		WireChecksum:    s3pkg.WireChecksumContentMD5,
	})
	if err != nil {
		t.Fatalf("s3 client: %v", err)
	}
	s := NewFromObjectStore(store, bucket)
	s.workspacePartSize = 5 << 20 // S3 minimum part size; forces 3 parts below

	path, data := writeTar(t, 12<<20+123)
	tenant, sandbox := "tenant-it", "sbx-it"
	t.Cleanup(func() { _ = s.Delete(context.Background(), tenant, sandbox) })

	first, err := s.PutWorkspace(ctx, own(tenant, sandbox), path)
	if err != nil {
		t.Fatalf("PutWorkspace: %v", err)
	}
	latest, err := s.PutWorkspace(ctx, own(tenant, sandbox), path)
	if err != nil {
		t.Fatalf("PutWorkspace (second generation): %v", err)
	}
	if err := s.PruneWorkspaces(ctx, tenant, sandbox, latest); err != nil {
		t.Fatalf("PruneWorkspaces: %v", err)
	}
	if err := s.GetWorkspace(ctx, own(tenant, sandbox), first, &bytes.Buffer{}); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("pruned generation err = %v, want ErrSnapshotMissing", err)
	}

	var out bytes.Buffer
	if err := s.GetWorkspace(ctx, own(tenant, sandbox), latest, &out); err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("round-tripped workspace differs")
	}
	bad := latest
	bad.SHA256 = strings.Repeat("0", 64)
	if err := s.GetWorkspace(ctx, own(tenant, sandbox), bad, &bytes.Buffer{}); !errors.Is(err, ErrWorkspaceCorrupt) {
		t.Fatalf("digest mismatch err = %v, want ErrWorkspaceCorrupt", err)
	}

	if err := s.Delete(ctx, tenant, sandbox); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	objs, err := store.List(ctx, bucket, sandboxPrefix(tenant, sandbox)+"/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 0 {
		t.Fatalf("objects left after Delete: %v", objs)
	}
}
