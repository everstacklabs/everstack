//go:build linux || darwin

package firecracker

import "golang.org/x/sys/unix"

// availableBytes reports the space available to an unprivileged writer
// on the filesystem containing path.
//
// Bavail rather than Bfree: Bfree counts blocks reserved for root, which
// the agent cannot rely on even when it runs as root inside a container,
// and counting them would let the guard admit a VM into space the
// filesystem will not actually hand out.
func availableBytes(path string) (int64, error) {
	_, avail, err := storeBytes(path)
	return avail, err
}

// storeBytes returns the filesystem's total size and the bytes available
// to an unprivileged writer (see availableBytes for why Bavail).
func storeBytes(path string) (total, avail int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize), nil
}
