package snapshot

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/storage"
)

func writeTar(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "trooper.tar.gz")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

func newTestWorkspaceStore(blob *memBlob) *ObjectStoreBacked {
	s := NewFromObjectStore(blob, "bkt")
	s.workspacePartSize = 1 << 10 // force multi-part uploads in tests
	return s
}

func TestPutWorkspaceUploadsVerifiedObjectUnderTenantPrefix(t *testing.T) {
	blob := newMemBlob()
	s := newTestWorkspaceStore(blob)
	path, data := writeTar(t, 5000)

	ref, err := s.PutWorkspace(context.Background(), own("tenant-a", "sbx-1"), path)
	if err != nil {
		t.Fatalf("PutWorkspace: %v", err)
	}
	if !strings.HasPrefix(ref.Key, "tenants/tenant-a/sandboxes/sbx-1/workspace/") {
		t.Fatalf("key %q is not under the tenant/sandbox workspace prefix", ref.Key)
	}
	if ref.SizeBytes != int64(len(data)) || len(ref.SHA256) != 64 {
		t.Fatalf("ref = %+v, want size %d and a sha256", ref, len(data))
	}
	if !blob.has("bkt", ref.Key) {
		t.Fatalf("object %q not stored", ref.Key)
	}

	parsed, ok, err := ParseWorkspaceRef(ref.String())
	if err != nil || !ok || parsed != ref {
		t.Fatalf("round trip: got %+v ok=%v err=%v, want %+v", parsed, ok, err, ref)
	}

	var out bytes.Buffer
	if err := s.GetWorkspace(context.Background(), own("tenant-a", "sbx-1"), ref, &out); err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("downloaded workspace differs from the uploaded tarball")
	}
}

func TestPutWorkspaceFailsWhenStoredBytesDoNotVerify(t *testing.T) {
	blob := newMemBlob()
	blob.corruptOnComplete = true
	s := newTestWorkspaceStore(blob)
	path, _ := writeTar(t, 3000)

	_, err := s.PutWorkspace(context.Background(), own("tenant-a", "sbx-1"), path)
	if err == nil {
		t.Fatal("PutWorkspace succeeded although the stored object failed checksum verification")
	}
	if got := blob.keys("bkt"); len(got) != 0 {
		t.Fatalf("unverified object left behind: %v", got)
	}
}

func TestPutWorkspaceReportsUploadFailure(t *testing.T) {
	blob := newMemBlob()
	blob.failUploadPart = storage.NewOperationError("upload multipart part", storage.ErrorUnavailable, true, "", 503)
	s := newTestWorkspaceStore(blob)
	path, _ := writeTar(t, 3000)

	if _, err := s.PutWorkspace(context.Background(), own("tenant-a", "sbx-1"), path); err == nil {
		t.Fatal("PutWorkspace succeeded although the upload failed")
	}
}

func TestGetWorkspaceChecksumMismatch(t *testing.T) {
	blob := newMemBlob()
	s := newTestWorkspaceStore(blob)
	path, _ := writeTar(t, 4000)
	ref, err := s.PutWorkspace(context.Background(), own("tenant-a", "sbx-1"), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("bytes corrupted in storage", func(t *testing.T) {
		blob.corruptOnGet[ref.Key] = true
		defer delete(blob.corruptOnGet, ref.Key)
		err := s.GetWorkspace(context.Background(), own("tenant-a", "sbx-1"), ref, &bytes.Buffer{})
		if !errors.Is(err, ErrWorkspaceCorrupt) {
			t.Fatalf("err = %v, want ErrWorkspaceCorrupt", err)
		}
	})
	t.Run("ref digest does not match object", func(t *testing.T) {
		bad := ref
		bad.SHA256 = strings.Repeat("0", 64)
		err := s.GetWorkspace(context.Background(), own("tenant-a", "sbx-1"), bad, &bytes.Buffer{})
		if !errors.Is(err, ErrWorkspaceCorrupt) {
			t.Fatalf("err = %v, want ErrWorkspaceCorrupt", err)
		}
	})
	t.Run("ref size does not match object", func(t *testing.T) {
		bad := ref
		bad.SizeBytes++
		err := s.GetWorkspace(context.Background(), own("tenant-a", "sbx-1"), bad, &bytes.Buffer{})
		if !errors.Is(err, ErrWorkspaceCorrupt) {
			t.Fatalf("err = %v, want ErrWorkspaceCorrupt", err)
		}
	})
}

func TestGetWorkspaceMissingObject(t *testing.T) {
	s := newTestWorkspaceStore(newMemBlob())
	ref := WorkspaceRef{
		Key:       RunPrefix(own("tenant-a", "sbx-1")) + "gone.tar.gz",
		SHA256:    strings.Repeat("a", 64),
		SizeBytes: 10,
	}
	err := s.GetWorkspace(context.Background(), own("tenant-a", "sbx-1"), ref, &bytes.Buffer{})
	if !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("err = %v, want ErrSnapshotMissing", err)
	}
}

func TestWorkspaceTenantKeyIsolation(t *testing.T) {
	blob := newMemBlob()
	s := newTestWorkspaceStore(blob)
	path, _ := writeTar(t, 2000)
	refA, err := s.PutWorkspace(context.Background(), own("tenant-a", "sbx-1"), path)
	if err != nil {
		t.Fatal(err)
	}

	// A ref recorded for tenant A must never be readable on behalf of
	// another tenant or another sandbox, even if the ref string is copied.
	for _, tc := range []struct{ tenant, sandbox string }{
		{"tenant-b", "sbx-1"},
		{"tenant-a", "sbx-2"},
		{"tenant", "a/sandboxes/sbx-1"},
	} {
		err := s.GetWorkspace(context.Background(), own(tc.tenant, tc.sandbox), refA, &bytes.Buffer{})
		if !errors.Is(err, ErrWorkspaceRefForeign) && !errors.Is(err, ErrInvalidWorkspaceOwner) {
			t.Fatalf("GetWorkspace(%q,%q) err = %v, want a foreign-ref rejection", tc.tenant, tc.sandbox, err)
		}
	}

	// Traversal in the ref key is rejected even under the right prefix.
	trav := refA
	trav.Key = WorkspacePrefix("tenant-a", "sbx-1") + "../../../../tenant-b/sandboxes/sbx-9/workspace/x.tar.gz"
	if err := s.GetWorkspace(context.Background(), own("tenant-a", "sbx-1"), trav, &bytes.Buffer{}); !errors.Is(err, ErrWorkspaceRefForeign) {
		t.Fatalf("traversal err = %v, want ErrWorkspaceRefForeign", err)
	}

	// Unsafe owner segments are refused on upload.
	for _, tc := range []struct{ tenant, sandbox string }{
		{"", "sbx"}, {"t", ""}, {"a/b", "sbx"}, {"t", ".."}, {"..", "s"}, {"t", "s b"},
	} {
		if _, err := s.PutWorkspace(context.Background(), own(tc.tenant, tc.sandbox), path); !errors.Is(err, ErrInvalidWorkspaceOwner) {
			t.Fatalf("PutWorkspace(%q,%q) err = %v, want ErrInvalidWorkspaceOwner", tc.tenant, tc.sandbox, err)
		}
	}
}

func TestPruneWorkspacesKeepsOnlyLatest(t *testing.T) {
	blob := newMemBlob()
	s := newTestWorkspaceStore(blob)
	ctx := context.Background()
	path, _ := writeTar(t, 1500)

	old, err := s.PutWorkspace(ctx, own("tenant-a", "sbx-1"), path)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy archive key from the pre-fix archive path.
	blob.put("bkt", key("tenant-a", "sbx-1", KindWorkspace), []byte("legacy"), "")
	// Another sandbox of the same tenant must be untouched.
	other, err := s.PutWorkspace(ctx, own("tenant-a", "sbx-2"), path)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.PutWorkspace(ctx, own("tenant-a", "sbx-1"), path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.PruneWorkspaces(ctx, "tenant-a", "sbx-1", latest); err != nil {
		t.Fatalf("PruneWorkspaces: %v", err)
	}
	if blob.has("bkt", old.Key) || blob.has("bkt", key("tenant-a", "sbx-1", KindWorkspace)) {
		t.Fatalf("older tarballs survived prune: %v", blob.keys("bkt"))
	}
	if !blob.has("bkt", latest.Key) || !blob.has("bkt", other.Key) {
		t.Fatalf("prune removed the latest or another sandbox's tarball: %v", blob.keys("bkt"))
	}
}

func TestDeleteRemovesWorkspaceTarballs(t *testing.T) {
	blob := newMemBlob()
	s := newTestWorkspaceStore(blob)
	ctx := context.Background()
	path, _ := writeTar(t, 1500)

	ref, err := s.PutWorkspace(ctx, own("tenant-a", "sbx-1"), path)
	if err != nil {
		t.Fatal(err)
	}
	blob.put("bkt", key("tenant-a", "sbx-1", KindWorkspace), []byte("legacy"), "")
	blob.put("bkt", manifestKey("tenant-a", "sbx-1"), []byte("{}"), "")
	keep, err := s.PutWorkspace(ctx, own("tenant-b", "sbx-1"), path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(ctx, "tenant-a", "sbx-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, k := range blob.keys("bkt") {
		if strings.HasPrefix(k, "tenants/tenant-a/sandboxes/sbx-1/") {
			t.Fatalf("Delete left %q behind (ref was %q)", k, ref.Key)
		}
	}
	if !blob.has("bkt", keep.Key) {
		t.Fatal("Delete removed another tenant's workspace")
	}
}

func TestParseWorkspaceRef(t *testing.T) {
	if _, ok, err := ParseWorkspaceRef("/tmp/everstack/sandboxes/x/trooper.tar.gz"); ok || err != nil {
		t.Fatalf("local path: ok=%v err=%v, want not an object ref", ok, err)
	}
	if _, ok, err := ParseWorkspaceRef(string(KindWorkspace)); ok || err != nil {
		t.Fatalf("legacy marker: ok=%v err=%v, want not an object ref", ok, err)
	}
	for _, bad := range []string{
		workspaceRefScheme + "k",
		workspaceRefScheme + "k#sha256=zz&size=1",
		workspaceRefScheme + "k#sha256=" + strings.Repeat("a", 64) + "&size=-1",
		workspaceRefScheme + "#sha256=" + strings.Repeat("a", 64) + "&size=1",
	} {
		if _, ok, err := ParseWorkspaceRef(bad); !ok || err == nil {
			t.Fatalf("ParseWorkspaceRef(%q) ok=%v err=%v, want a parse error", bad, ok, err)
		}
	}
}

func TestDisabledStoreIsNotAWorkspaceStore(t *testing.T) {
	var s Store = NewDisabled()
	if _, ok := s.(WorkspaceStore); ok {
		t.Fatal("Disabled must not satisfy WorkspaceStore: callers rely on that to detect unconfigured persistence")
	}
}

// A lost write-back leaves a verified generation in storage that no row
// references. LatestWorkspace must find the newest one and verify it.
func TestLatestWorkspaceAdoptsNewestVerifiedGeneration(t *testing.T) {
	blob := newMemBlob()
	s := newTestWorkspaceStore(blob)
	ctx := context.Background()

	if _, found, err := s.LatestWorkspace(ctx, own("tenant-a", "sbx-1"), &bytes.Buffer{}); found || err != nil {
		t.Fatalf("empty prefix: found=%v err=%v, want not found", found, err)
	}
	if has, err := s.HasWorkspace(ctx, own("tenant-a", "sbx-1")); has || err != nil {
		t.Fatalf("HasWorkspace on empty prefix = %v, %v", has, err)
	}

	oldPath, _ := writeTar(t, 1000)
	if _, err := s.PutWorkspace(ctx, own("tenant-a", "sbx-1"), oldPath); err != nil {
		t.Fatal(err)
	}
	newPath, newData := writeTar(t, 1200)
	newest, err := s.PutWorkspace(ctx, own("tenant-a", "sbx-1"), newPath)
	if err != nil {
		t.Fatal(err)
	}
	// Another tenant's newer object under a similar id must not be seen.
	if _, err := s.PutWorkspace(ctx, own("tenant-b", "sbx-1"), newPath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	ref, found, err := s.LatestWorkspace(ctx, own("tenant-a", "sbx-1"), &out)
	if err != nil || !found {
		t.Fatalf("LatestWorkspace: found=%v err=%v", found, err)
	}
	if ref != newest || !bytes.Equal(out.Bytes(), newData) {
		t.Fatalf("adopted %+v, want the newest generation %+v", ref, newest)
	}
	if has, err := s.HasWorkspace(ctx, own("tenant-a", "sbx-1")); !has || err != nil {
		t.Fatalf("HasWorkspace = %v, %v, want true", has, err)
	}

	blob.corruptOnGet[newest.Key] = true
	if _, _, err := s.LatestWorkspace(ctx, own("tenant-a", "sbx-1"), &bytes.Buffer{}); !errors.Is(err, ErrWorkspaceCorrupt) {
		t.Fatalf("corrupt newest generation err = %v, want ErrWorkspaceCorrupt", err)
	}
	delete(blob.corruptOnGet, newest.Key)

	if err := s.PruneWorkspaces(ctx, "tenant-a", "sbx-1", WorkspaceRef{}); err != nil {
		t.Fatal(err)
	}
	if has, _ := s.HasWorkspace(ctx, own("tenant-a", "sbx-1")); has {
		t.Fatal("prune with a zero keep must delete every generation")
	}
}

// own returns an owner in a fixed incarnation and run for tests.
func own(tenant, sandbox string) WorkspaceOwner {
	return WorkspaceOwner{TenantID: tenant, SandboxID: sandbox, Incarnation: "inc-1", RunID: "run-1"}
}

// Sandbox ids are reused across incarnations (wks_<agent>). A ref or a
// tarball from a previous incarnation, or a previous run, must never be
// restored or adopted for the current one.
func TestWorkspaceIncarnationAndRunIsolation(t *testing.T) {
	blob := newMemBlob()
	s := newTestWorkspaceStore(blob)
	ctx := context.Background()
	path, _ := writeTar(t, 900)

	oldInc := WorkspaceOwner{TenantID: "tenant-a", SandboxID: "wks_agent", Incarnation: "inc-old", RunID: "run-9"}
	oldRef, err := s.PutWorkspace(ctx, oldInc, path)
	if err != nil {
		t.Fatal(err)
	}
	cur := WorkspaceOwner{TenantID: "tenant-a", SandboxID: "wks_agent", Incarnation: "inc-new", RunID: "run-1"}

	if err := s.GetWorkspace(ctx, cur, oldRef, &bytes.Buffer{}); !errors.Is(err, ErrWorkspaceRefForeign) {
		t.Fatalf("previous incarnation's ref: err = %v, want ErrWorkspaceRefForeign", err)
	}
	if _, found, err := s.LatestWorkspace(ctx, cur, &bytes.Buffer{}); found || err != nil {
		t.Fatalf("previous incarnation adopted: found=%v err=%v", found, err)
	}

	// Same incarnation, previous run (the generation the last wake consumed).
	prevRun := cur
	prevRun.RunID = "run-0"
	consumed, err := s.PutWorkspace(ctx, prevRun, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.LatestWorkspace(ctx, cur, &bytes.Buffer{}); found {
		t.Fatal("a previous run's generation was adopted for the current run")
	}
	// But an explicit ref to it (still on the row) is restorable.
	if err := s.GetWorkspace(ctx, cur, consumed, &bytes.Buffer{}); err != nil {
		t.Fatalf("explicit ref within the incarnation: %v", err)
	}
	if has, _ := s.HasWorkspace(ctx, cur); has {
		t.Fatal("HasWorkspace must only see the current run")
	}

	if err := s.DeleteIncarnation(ctx, oldInc); err != nil {
		t.Fatal(err)
	}
	if blob.has("bkt", oldRef.Key) || !blob.has("bkt", consumed.Key) {
		t.Fatalf("DeleteIncarnation removed the wrong objects: %v", blob.keys("bkt"))
	}
}
