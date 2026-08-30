package firecracker

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckDiskHeadroom_DisabledByZero(t *testing.T) {
	// A path that cannot be stat'd proves the zero check short-circuits
	// before touching the filesystem.
	for _, min := range []int64{0, -1} {
		if err := checkDiskHeadroom("/definitely/not/a/real/path", min); err != nil {
			t.Fatalf("minFreeMB=%d should disable the check, got %v", min, err)
		}
	}
}

func TestCheckDiskHeadroom_UnstatableFilesystemAdmits(t *testing.T) {
	// Failing every create because statfs misbehaved would be a worse
	// outage than the one the guard exists to prevent.
	if err := checkDiskHeadroom("/definitely/not/a/real/path", 1024); err != nil {
		t.Fatalf("unstatable path should admit, got %v", err)
	}
}

func TestCheckDiskHeadroom_AdmitsWhenSpaceAvailable(t *testing.T) {
	// 1 MiB against a temp dir on any developer or CI machine.
	if err := checkDiskHeadroom(t.TempDir(), 1); err != nil {
		t.Fatalf("expected admission with ample free space, got %v", err)
	}
}

func TestCheckDiskHeadroom_RefusesWhenBelowFloor(t *testing.T) {
	// An absurd floor forces the refusal path on any real filesystem.
	const floorMB = 1 << 40 // 1 EiB
	dir := t.TempDir()

	err := checkDiskHeadroom(dir, floorMB)
	if err == nil {
		t.Fatal("expected refusal against a 1 EiB floor")
	}

	var headroom *ErrInsufficientDiskHeadroom
	if !errors.As(err, &headroom) {
		t.Fatalf("err = %T, want *ErrInsufficientDiskHeadroom", err)
	}
	if headroom.RequiredMB != floorMB {
		t.Fatalf("RequiredMB = %d, want %d", headroom.RequiredMB, floorMB)
	}
	if headroom.Path != dir {
		t.Fatalf("Path = %q, want %q", headroom.Path, dir)
	}
	// The message has to name both numbers: a capacity error that does
	// not say how short it was sends the operator back to the shell.
	for _, want := range []string{"available", "required", dir} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestAvailableBytes_ReportsSomethingForARealPath(t *testing.T) {
	got, err := availableBytes(t.TempDir())
	if err != nil {
		t.Skipf("statfs unsupported here: %v", err)
	}
	if got <= 0 {
		t.Fatalf("availableBytes = %d, want a positive figure", got)
	}
}
