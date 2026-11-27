//go:build !linux

package firecracker

import "errors"

// openFileIDs has no /proc to scan here. The GC treats the error as
// "cannot verify" and deletes nothing.
func openFileIDs() (map[fileID]struct{}, error) {
	return nil, errors.New("firecracker: open-file scan needs /proc (linux only)")
}
