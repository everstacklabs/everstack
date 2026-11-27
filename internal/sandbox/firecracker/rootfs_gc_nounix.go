//go:build !linux && !darwin

package firecracker

import "errors"

// statFile is unavailable here, which makes every GC candidate
// unverifiable and therefore kept.
func statFile(string) (fileMeta, error) {
	return fileMeta{}, errors.New("firecracker: stat unsupported on this platform")
}

type lstatMeta struct {
	id      fileID
	regular bool
}

func lstatFile(string) (lstatMeta, error) {
	return lstatMeta{}, errors.New("firecracker: lstat unsupported on this platform")
}
