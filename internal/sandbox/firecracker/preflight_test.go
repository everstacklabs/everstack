package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPreflight_AggregatesFailures verifies that Preflight returns
// every failing probe in the result slice (operators want to fix
// every config bug in one restart cycle, not "fix one, restart, hit
// the next, fix that, restart, ...").
func TestPreflight_AggregatesFailures(t *testing.T) {
	cfg := FirecrackerConfig{
		BinaryPath: "/definitely/does/not/exist/firecracker",
		KernelPath: "/definitely/does/not/exist/vmlinux",
		RootfsDir:  "/definitely/does/not/exist/rootfs",
		WorkDir:    "/definitely/does/not/exist/vms",
	}
	results, err := Preflight(cfg)
	if err == nil {
		t.Fatal("expected error from preflight with bogus paths")
	}
	if len(results) == 0 {
		t.Fatal("expected at least one probe result")
	}
	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	// At minimum binary, kernel, rootfs_dir, and work_dir_writable should fail.
	// (kvm probe is platform-dependent — pass on Linux CI with kvm, fail on darwin.)
	if failed < 3 {
		t.Fatalf("expected ≥3 failed probes, got %d (results=%+v)", failed, results)
	}
}

func TestCheckRootfsDir(t *testing.T) {
	dir := t.TempDir()
	if err := checkRootfsDir(dir); err == nil {
		t.Fatal("empty dir should fail rootfs probe")
	}
	if err := os.WriteFile(filepath.Join(dir, "ubuntu.ext4"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := checkRootfsDir(dir); err != nil {
		t.Fatalf("dir with ext4 should pass: %v", err)
	}
}

func TestCheckWritableDir(t *testing.T) {
	dir := t.TempDir()
	if err := checkWritableDir(dir); err != nil {
		t.Fatalf("tempdir should be writable: %v", err)
	}
	// Verify probe file is cleaned up.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == ".preflight-write-probe" {
			t.Fatal("probe file leaked in writable dir")
		}
	}
}
