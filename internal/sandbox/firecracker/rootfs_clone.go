package firecracker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// cloneRootfs creates a per-VM copy of the source ext4 image at dst.
// Tries the cheapest method first and falls back as needed:
//
//	1. FICLONE (ioctl_ficlone): instant copy-on-write; the dst file
//	   shares pages with the src until either side is written to.
//	   Costs O(1) regardless of file size and zero RAM. Requires the
//	   filesystem to advertise reflink (xfs default-on, btrfs always-on,
//	   ext4 only with `mkfs.ext4 -O reflink` — default off on most
//	   distros, including OVH default).
//	2. copy_file_range(2): in-kernel byte copy. No COW semantics, but
//	   the data never round-trips through userspace, so RSS stays
//	   flat regardless of file size. Linux 4.5+. Works on any
//	   filesystem.
//	3. io.Copy with a 1 MiB buffer: last resort. Bounded RAM (1 MiB,
//	   not the full file), but does the actual byte movement through
//	   the agent's heap. Reserved for filesystems where copy_file_range
//	   refuses (cross-filesystem, certain procfs/sysfs paths, very
//	   old kernels).
//
// fsync is called on dst before close so the file is durable on
// disk before firecracker is told to attach it as a drive — without
// fsync, a crash during rootfs setup would leave a torn file that
// the next attempt's copy_file_range could read garbage from.
//
// Returns the method that actually succeeded so the caller can log
// it once. Subsequent calls don't re-probe; a process-wide
// "downgraded" marker remembers when FICLONE / CFR returned ENOTSUP
// so we don't keep paying the syscall + fallback cost on a
// filesystem that we already know lacks support.
func cloneRootfs(srcPath, dstPath string) (string, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("open src %s: %w", srcPath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return "", fmt.Errorf("create dst %s: %w", dstPath, err)
	}
	// Best-effort cleanup on partial-write failure paths so we don't
	// leave a 0-byte file that masquerades as a usable rootfs.
	dstClosed := false
	defer func() {
		if !dstClosed {
			_ = dst.Close()
		}
	}()

	method, err := tryCloneOrCopy(src, dst)
	if err != nil {
		_ = os.Remove(dstPath)
		return "", err
	}

	// fsync so the drive is durable before firecracker opens it.
	// Skip on filesystems where fsync isn't meaningful (procfs/sysfs)
	// — but for ext4/xfs/btrfs/tmpfs this is essential.
	if err := dst.Sync(); err != nil {
		_ = os.Remove(dstPath)
		return "", fmt.Errorf("fsync dst: %w", err)
	}
	if err := dst.Close(); err != nil {
		return "", fmt.Errorf("close dst: %w", err)
	}
	dstClosed = true
	return method, nil
}

// reflinkUnsupported caches a process-wide hint that FICLONE +
// copy_file_range have already returned ENOTSUP on this filesystem,
// so we don't keep re-trying them on every clone. The rootfs dir
// is one mount in practice; a future fallback to a different mount
// would just trigger one extra probe before re-marking.
var reflinkUnsupported atomic.Bool

// tryCloneOrCopy walks the fallback ladder. Returns the method name
// on success. The Linux-specific FICLONE / copy_file_range
// implementations live in rootfs_clone_linux.go; the stubs in
// rootfs_clone_other.go return an "unsupported" sentinel so non-Linux
// builds (developer macOS) just exercise the io.Copy path.
func tryCloneOrCopy(src, dst *os.File) (string, error) {
	if !reflinkUnsupported.Load() {
		// 1. FICLONE — instant. ENOTSUP / EOPNOTSUPP on filesystems
		//    that don't support reflink; EXDEV across mounts.
		if err := ficlone(src, dst); err == nil {
			return "ficlone", nil
		} else if !isReflinkUnsupported(err) {
			return "", fmt.Errorf("ficlone: %w", err)
		}
	}

	// 2. copy_file_range — in-kernel zero-userspace byte copy.
	if err := copyFileRange(src, dst); err == nil {
		return "copy_file_range", nil
	} else if !isReflinkUnsupported(err) {
		return "", fmt.Errorf("copy_file_range: %w", err)
	}
	// Both fast paths declined → cache it so we skip them next time.
	reflinkUnsupported.Store(true)
	logger.WithFields("dst", dst.Name()).
		Info("rootfs_clone: FICLONE + copy_file_range unsupported, falling through to io.Copy")

	// 3. Bounded io.Copy. 1 MiB buffer keeps heap residency tiny
	//    regardless of file size.
	if _, err := io.CopyBuffer(dst, src, make([]byte, 1<<20)); err != nil {
		return "", fmt.Errorf("io.Copy: %w", err)
	}
	return "io_copy", nil
}

// errReflinkUnsupported is the sentinel the platform-specific
// implementations return when the underlying syscall isn't available
// on this OS or filesystem. Wrapping unix errnos directly would make
// the caller import the unix package; this hides the platform.
var errReflinkUnsupported = errors.New("reflink/copy-file-range unsupported on this platform or filesystem")

