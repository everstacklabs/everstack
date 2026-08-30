package firecracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// resizeTimeout bounds the resize2fs call. Measured at ~55ms for a
// 2 GiB -> 20 GiB grow on a freshly-cloned image; 60s is three orders
// of magnitude of headroom so a hung binary fails the create instead
// of wedging it.
const resizeTimeout = 60 * time.Second

// errResizeToolMissing means resize2fs is not on PATH. Callers treat
// this as "provision the base size and warn" rather than a hard
// failure: a working smaller sandbox beats no sandbox, and the agent
// image may legitimately predate the e2fsprogs-extra dependency.
var errResizeToolMissing = errors.New("firecracker: resize2fs not found on PATH")

// growRootfs expands a per-VM ext4 rootfs image to diskMB, so the guest
// actually gets the disk the caller asked for (and was billed for).
//
// Cheap by construction: the file is extended with truncate, which only
// adds a hole, and resize2fs writes the new block-group metadata
// sparsely. Measured on btrfs, growing a reflinked 2 GiB clone to 20 GiB
// costs about 1 MiB of real space, so honoring a large DiskMB does not
// undo the reflink savings.
//
// Grow only, never shrink. A request smaller than the base image leaves
// the image alone: shrinking would mean discarding blocks the base
// rootfs is already using, and resize2fs shrink is both slow and able
// to fail halfway. Handing a customer more disk than they asked for is
// harmless; handing them a corrupt rootfs is not.
//
// Returns the size the image actually ended up at, so callers can log
// or meter against what was really provisioned rather than what was
// requested.
func growRootfs(ctx context.Context, path string, diskMB int64) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("firecracker.growRootfs: stat: %w", err)
	}
	current := info.Size()

	if diskMB <= 0 {
		return current, nil
	}
	target := diskMB * 1024 * 1024
	if target <= current {
		// Already at or above the request. Not an error: base.ext4 is
		// 2 GiB and plenty of templates ask for less.
		return current, nil
	}

	if _, err := exec.LookPath("resize2fs"); err != nil {
		return current, errResizeToolMissing
	}

	if err := os.Truncate(path, target); err != nil {
		return current, fmt.Errorf("firecracker.growRootfs: truncate to %d: %w", target, err)
	}

	// resize2fs with no size argument grows the filesystem to fill the
	// whole file. A freshly-cloned image is clean (base.ext4 comes
	// straight from mkfs and is never mounted on the host), so no
	// e2fsck pass is needed on the happy path.
	resizeCtx, cancel := context.WithTimeout(ctx, resizeTimeout)
	defer cancel()
	out, err := exec.CommandContext(resizeCtx, "resize2fs", path).CombinedOutput()
	if err != nil {
		// Roll the file back so it does not sit there claiming space
		// the filesystem inside it cannot address.
		if truncErr := os.Truncate(path, current); truncErr != nil {
			logger.WithFields("path", path, "error", truncErr.Error()).
				Warn("firecracker.growRootfs: rollback truncate failed")
		}
		return current, fmt.Errorf("firecracker.growRootfs: resize2fs: %w (output: %s)", err, string(out))
	}

	return target, nil
}

// applyDiskSize grows the cloned rootfs to the requested DiskMB and
// reports what was provisioned.
//
// A resize failure does not fail the VM create. The sandbox still boots,
// just with the base image's size. That is deliberate: rolling out this
// code ahead of an agent image that carries resize2fs would otherwise
// break every sandbox create. The warning is loud and names both numbers
// because the gap is a billing problem: usage_meter bills DiskMB at
// DiskGBPerHourUSD whether or not it was ever provisioned.
func applyDiskSize(ctx context.Context, rootfsPath string, diskMB int64) {
	provisioned, err := growRootfs(ctx, rootfsPath, diskMB)
	switch {
	case errors.Is(err, errResizeToolMissing):
		logger.WithFields(
			"path", rootfsPath,
			"requested_mb", diskMB,
			"provisioned_mb", provisioned/(1024*1024),
		).Warn("firecracker: resize2fs unavailable, sandbox provisioned at base image size while billed for the requested size; add e2fsprogs-extra to the agent image")
	case err != nil:
		logger.WithFields(
			"path", rootfsPath,
			"requested_mb", diskMB,
			"provisioned_mb", provisioned/(1024*1024),
			"error", err.Error(),
		).Warn("firecracker: rootfs grow failed, sandbox provisioned at base image size while billed for the requested size")
	case provisioned > 0 && diskMB > 0 && provisioned == diskMB*1024*1024:
		logger.WithFields("path", rootfsPath, "disk_mb", diskMB).
			Debug("firecracker: rootfs grown to requested disk size")
	}
}
