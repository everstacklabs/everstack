//go:build !linux

package firecracker

import (
	"errors"
	"os"
)

// Non-Linux builds (developer macOS, CI on darwin) don't actually run
// the firecracker backend — KVM is Linux-only. These stubs let the
// rootfs_clone.go compile so the package builds for tests / IDE; they
// always report unsupported, so the io.Copy fallback handles any
// integration-test scenarios that exercise the path on a non-Linux
// box.

func ficlone(_, _ *os.File) error {
	return errReflinkUnsupported
}

func copyFileRange(_, _ *os.File) error {
	return errReflinkUnsupported
}

func isReflinkUnsupported(err error) bool {
	return errors.Is(err, errReflinkUnsupported)
}
