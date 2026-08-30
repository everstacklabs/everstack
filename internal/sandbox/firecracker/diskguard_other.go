//go:build !linux && !darwin

package firecracker

import "errors"

// availableBytes is unavailable on this platform. checkDiskHeadroom
// treats the error as "admit anyway", so non-unix builds simply run
// without the guard.
func availableBytes(string) (int64, error) {
	return 0, errors.New("firecracker: statfs unsupported on this platform")
}
