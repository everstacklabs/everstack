package firecracker

import (
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// DefaultMinFreeDiskMB is the headroom the VM store must retain before a
// new microVM is admitted.
//
// Sandbox disks are thin: a VM's rootfs is a reflink clone of base.ext4
// that costs only the blocks the guest dirties, and growRootfs extends
// it to the requested DiskMB for roughly a megabyte. That makes the
// store heavily over-committed by design. Every template currently asks
// for 20 GiB, and a cap of 20 VMs per environment means the two agents
// can promise ~800 GiB against a 20 GiB store.
//
// Over-commit is a deliberate policy, the same trade as memory
// over-commit, but it needs a floor. Without one the failure mode is
// correlated and ugly: the store hits ENOSPC and writes start failing
// inside every running VM at once, across tenants, with no single
// culprit. Refusing to admit new VMs while the store is nearly full
// converts that into a bounded, attributable error on one create path.
//
// 2 GiB is chosen to comfortably exceed the largest single allocation a
// create performs, which is a full non-reflink copy of base.ext4 on a
// host whose store lacks reflink support.
const DefaultMinFreeDiskMB = 2048

// ErrInsufficientDiskHeadroom is returned when the VM store lacks the
// configured free space for a new microVM. Callers should surface this
// as a capacity error rather than retrying: retrying cannot free space.
type ErrInsufficientDiskHeadroom struct {
	Path        string
	AvailableMB int64
	RequiredMB  int64
}

func (e *ErrInsufficientDiskHeadroom) Error() string {
	return fmt.Sprintf(
		"firecracker: insufficient disk headroom in VM store %s: %d MiB available, %d MiB required",
		e.Path, e.AvailableMB, e.RequiredMB,
	)
}

// checkDiskHeadroom refuses a create when the filesystem backing the VM
// work directory has less than minFreeMB available.
//
// This gates on free space rather than on summed DiskMB promises on
// purpose. Promised size says nothing about consumption when disks are
// thin, so admitting against it would refuse almost every create while
// the store sat nearly empty. Free space is what actually runs out.
//
// A minFreeMB of 0 or less disables the check. An unreadable filesystem
// also admits: failing every create because statfs misbehaved would be a
// worse outage than the one this guards against.
func checkDiskHeadroom(workDir string, minFreeMB int64) error {
	if minFreeMB <= 0 {
		return nil
	}
	availBytes, err := availableBytes(workDir)
	if err != nil {
		logger.WithFields("path", workDir, "error", err.Error()).
			Warn("firecracker: could not stat VM store for headroom check, admitting anyway")
		return nil
	}
	availMB := availBytes / (1024 * 1024)
	if availMB >= minFreeMB {
		return nil
	}
	return &ErrInsufficientDiskHeadroom{Path: workDir, AvailableMB: availMB, RequiredMB: minFreeMB}
}
