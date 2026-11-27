//go:build linux || darwin

package firecracker

import (
	"time"

	"golang.org/x/sys/unix"
)

// statFile follows symlinks, like the open-file scan does, so both sides
// of the in-use comparison name the same inode.
func statFile(path string) (fileMeta, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return fileMeta{}, err
	}
	return fileMeta{
		id:        fileID{dev: uint64(st.Dev), ino: st.Ino}, //nolint:gosec // dev is a device number, never negative
		ctime:     time.Unix(st.Ctim.Unix()),
		allocated: st.Blocks * 512,
	}, nil
}

type lstatMeta struct {
	id      fileID
	regular bool
}

// lstatFile does not follow symlinks: it describes the name itself.
func lstatFile(path string) (lstatMeta, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return lstatMeta{}, err
	}
	return lstatMeta{
		id:      fileID{dev: uint64(st.Dev), ino: st.Ino}, //nolint:gosec // dev is a device number, never negative
		regular: st.Mode&unix.S_IFMT == unix.S_IFREG,
	}, nil
}
