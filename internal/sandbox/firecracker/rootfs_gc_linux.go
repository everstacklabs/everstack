//go:build linux

package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
)

// openFileIDs returns the inode of every regular file held open by any
// process visible in /proc. This is the lsof check the 2026-10-13
// cleanup ran by hand.
//
// It sees host processes only because the fcagent DaemonSet runs with
// hostPID: true (see firecracker-agent-daemonset.yaml), so an operator's
// cp or a VM on the host shows up here. Inodes, not paths, are compared:
// a host process names the file by its host path while the agent may
// see it through a different mount.
//
// Processes that exit mid-scan, or whose fd table is unreadable, are
// skipped. If not a single fd table could be read the scan is reported
// as failed so the GC deletes nothing.
func openFileIDs() (map[fileID]struct{}, error) {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	open := map[fileID]struct{}{}
	readable := 0
	for _, p := range procs {
		if !p.IsDir() || !isAllDigits(p.Name()) {
			continue
		}
		fdDir := filepath.Join("/proc", p.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		readable++
		for _, fd := range fds {
			meta, err := statFile(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			open[meta.id] = struct{}{}
		}
	}
	if readable == 0 {
		return nil, fmt.Errorf("no readable fd table under /proc")
	}
	return open, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
