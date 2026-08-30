package firecracker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const mib = 1024 * 1024

// writeImage creates a file of exactly size bytes (sparse) for the
// size-comparison paths, which never invoke resize2fs.
func writeImage(t *testing.T, size int64) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rootfs.ext4")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return p
}

func TestGrowRootfs_NoOpCases(t *testing.T) {
	cases := []struct {
		name    string
		current int64
		diskMB  int64
	}{
		{"zero_disk_mb_leaves_image_alone", 2048 * mib, 0},
		{"negative_disk_mb_leaves_image_alone", 2048 * mib, -1},
		{"request_smaller_than_base_never_shrinks", 2048 * mib, 512},
		{"request_equal_to_base_is_a_no_op", 2048 * mib, 2048},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeImage(t, tc.current)
			// Empty PATH proves these paths return before ever looking
			// for resize2fs.
			t.Setenv("PATH", "")

			got, err := growRootfs(context.Background(), p, tc.diskMB)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.current {
				t.Fatalf("provisioned = %d, want unchanged %d", got, tc.current)
			}
			info, err := os.Stat(p)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Size() != tc.current {
				t.Fatalf("file resized to %d, want untouched %d", info.Size(), tc.current)
			}
		})
	}
}

func TestGrowRootfs_MissingToolIsRecoverable(t *testing.T) {
	p := writeImage(t, 2048*mib)
	t.Setenv("PATH", "")

	got, err := growRootfs(context.Background(), p, 20480)
	if !errors.Is(err, errResizeToolMissing) {
		t.Fatalf("err = %v, want errResizeToolMissing", err)
	}
	// The caller bills against what came back, so it must report the
	// size actually on disk, not the size that was asked for.
	if got != 2048*mib {
		t.Fatalf("provisioned = %d, want base size %d", got, 2048*mib)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 2048*mib {
		t.Fatalf("image grew to %d despite missing resize2fs, want %d", info.Size(), 2048*mib)
	}
}

func TestGrowRootfs_MissingImage(t *testing.T) {
	if _, err := growRootfs(context.Background(), filepath.Join(t.TempDir(), "absent.ext4"), 4096); err == nil {
		t.Fatal("expected an error for a missing image")
	}
}

// TestGrowRootfs_RealResize exercises the actual resize2fs path. It
// needs mkfs.ext4 + resize2fs, so it only runs on Linux hosts that have
// e2fsprogs; elsewhere it skips.
func TestGrowRootfs_RealResize(t *testing.T) {
	for _, bin := range []string{"mkfs.ext4", "resize2fs", "dumpe2fs"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	p := writeImage(t, 64*mib)
	if out, err := exec.Command("mkfs.ext4", "-F", "-q", p).CombinedOutput(); err != nil {
		t.Skipf("mkfs.ext4 failed (needs a permissive environment): %v: %s", err, out)
	}

	got, err := growRootfs(context.Background(), p, 256)
	if err != nil {
		t.Fatalf("growRootfs: %v", err)
	}
	if got != 256*mib {
		t.Fatalf("provisioned = %d, want %d", got, 256*mib)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 256*mib {
		t.Fatalf("image size = %d, want %d", info.Size(), 256*mib)
	}
	// The filesystem inside must actually address the new space,
	// otherwise the guest sees the old size and the whole point is lost.
	out, err := exec.Command("dumpe2fs", "-h", p).CombinedOutput()
	if err != nil {
		t.Fatalf("dumpe2fs: %v: %s", err, out)
	}
	if !containsBlockCountFor(string(out), 256*mib) {
		t.Fatalf("filesystem did not grow to fill the image:\n%s", out)
	}
}

// containsBlockCountFor reports whether dumpe2fs output describes a
// filesystem whose block count * block size equals want.
func containsBlockCountFor(dump string, want int64) bool {
	var blockCount, blockSize int64
	for _, line := range splitLines(dump) {
		if v, ok := fieldValue(line, "Block count:"); ok {
			blockCount = v
		}
		if v, ok := fieldValue(line, "Block size:"); ok {
			blockSize = v
		}
	}
	return blockCount > 0 && blockSize > 0 && blockCount*blockSize == want
}
