package firecracker

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCloneRootfs_FallbackProducesIdenticalCopy verifies the io.Copy
// fallback path produces a byte-identical copy. Runs on every platform
// — on Linux it exercises FICLONE/copy_file_range first, on darwin it
// hits the io.Copy path directly via the stub.
func TestCloneRootfs_FallbackProducesIdenticalCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.ext4")
	dst := filepath.Join(dir, "dst.ext4")

	// 4 MiB of random bytes — enough to exercise the 1 MiB chunked
	// io.CopyBuffer loop more than once, well past whatever the
	// kernel might short-return on copy_file_range.
	const size = 4 << 20
	want := make([]byte, size)
	if _, err := rand.Read(want); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	method, err := cloneRootfs(src, dst)
	if err != nil {
		t.Fatalf("cloneRootfs: %v", err)
	}
	t.Logf("clone method on %s: %s", runtime.GOOS, method)

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("size mismatch: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("content mismatch at byte %d: got %x want %x", i, got[i], want[i])
		}
	}
}

// TestCloneRootfs_MissingSourceReturnsError verifies clear error
// messaging for the operationally-likely "rootfs file isn't on disk
// yet" case — we shouldn't crash, we shouldn't half-create dst.
func TestCloneRootfs_MissingSourceReturnsError(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.ext4")

	_, err := cloneRootfs(filepath.Join(dir, "missing.ext4"), dst)
	if err == nil {
		t.Fatal("expected error on missing source, got nil")
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Fatal("dst was created despite source missing")
	}
}
