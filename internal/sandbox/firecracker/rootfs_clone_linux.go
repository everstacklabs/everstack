//go:build linux

package firecracker

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// ficlone invokes the Linux FICLONE ioctl which makes dst a reflink of
// src. Atomic; no partial result on failure. The destination must be
// freshly created (caller opens with O_TRUNC). Filesystems without
// reflink support (default ext4, tmpfs, etc.) return ENOTSUP /
// EOPNOTSUPP / EINVAL; cross-filesystem clones return EXDEV — all of
// which the caller treats as "fall through to copy_file_range".
func ficlone(src, dst *os.File) error {
	if err := unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())); err != nil {
		if isFastPathUnsupported(err) {
			return errReflinkUnsupported
		}
		return err
	}
	return nil
}

// copyFileRange uses copy_file_range(2) to do an in-kernel byte copy.
// The kernel may return short on each call; we loop until source is
// exhausted. RSS stays flat regardless of file size — bytes never
// transit through the agent's userspace heap.
func copyFileRange(src, dst *os.File) error {
	srcInfo, err := src.Stat()
	if err != nil {
		return err
	}
	remaining := srcInfo.Size()
	if remaining == 0 {
		return nil
	}
	for remaining > 0 {
		// nil offsets → kernel uses each fd's current position and
		// advances it as bytes move.
		n, err := unix.CopyFileRange(int(src.Fd()), nil, int(dst.Fd()), nil, int(remaining), 0)
		if err != nil {
			if isFastPathUnsupported(err) {
				return errReflinkUnsupported
			}
			return err
		}
		if n == 0 {
			// EOF without progress — treat as unsupported so we
			// fall through to io.Copy rather than infinite-looping.
			return errReflinkUnsupported
		}
		remaining -= int64(n)
	}
	return nil
}

// isReflinkUnsupported reports whether err is a sentinel from this
// package's fast-path stubs. The Linux implementations wrap errnos
// into errReflinkUnsupported themselves, so the caller just needs an
// errors.Is check.
func isReflinkUnsupported(err error) bool {
	return errors.Is(err, errReflinkUnsupported)
}

// isFastPathUnsupported recognises errnos that mean "this filesystem /
// kernel doesn't support that syscall, try a different method".
func isFastPathUnsupported(err error) bool {
	switch {
	case errors.Is(err, unix.ENOTSUP),
		errors.Is(err, unix.EOPNOTSUPP),
		errors.Is(err, unix.EINVAL),
		errors.Is(err, unix.EXDEV):
		return true
	}
	return false
}
