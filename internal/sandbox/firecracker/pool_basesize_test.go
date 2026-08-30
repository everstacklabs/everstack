package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVMPool_BaseRootfsSize(t *testing.T) {
	dir := t.TempDir()
	const size = 2048 * 1024 * 1024

	f, err := os.Create(filepath.Join(dir, "base.ext4"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	f.Close()

	p := &VMPool{config: FirecrackerConfig{RootfsDir: dir}}
	if got := p.baseRootfsSize(); got != size {
		t.Fatalf("baseRootfsSize = %d, want %d", got, size)
	}
	// Second call must come from the cache, not a re-stat: remove the
	// file and confirm the value survives.
	if err := os.Remove(filepath.Join(dir, "base.ext4")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := p.baseRootfsSize(); got != size {
		t.Fatalf("cached baseRootfsSize = %d, want %d", got, size)
	}
}

func TestVMPool_BaseRootfsSize_MissingImageFailsSafe(t *testing.T) {
	p := &VMPool{config: FirecrackerConfig{RootfsDir: t.TempDir()}}
	// 0 is the safe answer: Acquire compares a requested size against
	// this, so 0 makes every disk-specifying request cold-start rather
	// than hand out a warm VM that might be too small.
	if got := p.baseRootfsSize(); got != 0 {
		t.Fatalf("baseRootfsSize = %d, want 0 for a missing image", got)
	}
	if got := (*VMPool)(nil).baseRootfsSize(); got != 0 {
		t.Fatalf("nil pool baseRootfsSize = %d, want 0", got)
	}
}

// A warm VM is booted from an un-grown base.ext4, so it can only serve a
// request whose disk fits inside it. This pins the comparison Acquire
// makes.
func TestVMPool_WarmVMRejectedForLargerDisk(t *testing.T) {
	dir := t.TempDir()
	const base = 2048 * 1024 * 1024
	f, _ := os.Create(filepath.Join(dir, "base.ext4"))
	_ = f.Truncate(base)
	f.Close()
	p := &VMPool{config: FirecrackerConfig{RootfsDir: dir}}

	cases := []struct {
		name       string
		diskMB     int64
		wantCustom bool
	}{
		{"unset_disk_can_use_warm", 0, false},
		{"smaller_disk_can_use_warm", 512, false},
		{"exact_base_can_use_warm", 2048, false},
		{"larger_disk_must_cold_start", 20480, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.diskMB > 0 && tc.diskMB*1024*1024 > p.baseRootfsSize()
			if got != tc.wantCustom {
				t.Fatalf("needsCustomSize(disk) = %v, want %v", got, tc.wantCustom)
			}
		})
	}
}
