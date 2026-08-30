//go:build aix || solaris || (!darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows)

package credentials

import (
	"context"
	"errors"
)

func lockCredentialsFile(context.Context, string) (func(), error) {
	return nil, errors.New("cross-process credential locking is not supported on this platform")
}
