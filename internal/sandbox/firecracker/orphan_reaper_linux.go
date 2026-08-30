//go:build linux

package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroupOwnerID returns a stable fingerprint of a process's cgroup that
// identifies which fcagent (pod) owns it. Returns "" when the cgroup
// can't be read — callers MUST treat "" as "ownership unknown" and
// refuse to kill (fail safe), because over-killing a peer DaemonSet's
// live VMs is exactly the bug this guards against. The parsing rules
// live in parseCgroupOwner (platform-agnostic + unit-tested).
func cgroupOwnerID(pid string) string {
	data, err := os.ReadFile(filepath.Join("/proc", pid, "cgroup"))
	if err != nil {
		return ""
	}
	return parseCgroupOwner(string(data))
}

// findFirecrackerChildren scans /proc and returns every running
// process named "firecracker". The previous PPID-equals-fcagent
// filter was too narrow: it missed any firecracker process that
// had been re-parented after its original parent died. Concrete
// failure mode observed on 2026-05-22: after a fcagent pod
// replacement, a firecracker VM was found alive with PPID pointing
// at a `sh` from an old kubectl-exec — the reaper's "is this mine"
// check excluded it, the workdir reconciler swept its workdir, and
// the process sat orphaned, RAM still allocated, no record of it in
// the backend's b.vms.
//
// Now: we return every non-zombie process whose comm is
// "firecracker". The reaper's outer protection logic is unchanged
// — tracked PIDs (b.vms + warm pool) AND the grace-period gate
// (process must be ≥5min old). That combination already prevents
// false positives for active and in-flight VMs.
//
// The parentPID argument is retained for API stability and is
// recorded on each candidate so callers / tests can still see the
// original parent if they want to (the orphan-reaper itself no
// longer filters on it).
//
// Ownership: on a host that runs OTHER fcagent DaemonSets (our real
// topology — everstack-dev AND everstack-prod fcagent are colocated on
// the single node), a comm-only scan sees a peer DaemonSet's VMs, and
// since they aren't in THIS backend's tracked set the reaper SIGKILLs
// them at the 5-min grace. That was THE "5-minute sandbox death": the
// prod reaper killing dev VMs and vice-versa (bpftrace-confirmed
// 2026-07-06, see project_sandbox_vm_5min_death). So we now filter
// candidates to processes in OUR OWN cgroup (pod slice) — a firecracker
// VM inherits its launching fcagent's cgroup, so own-cgroup membership
// is a reparenting-proof "is this mine" that also distinguishes dev from
// prod (unlike /proc/<pid>/exe, which is identical across DaemonSets).
// If we cannot determine our own cgroup we fail safe and return nothing:
// under-reaping leaks disk, but over-reaping kills live tenant VMs.
//
// StartedAt is derived from os.Stat ModTime of /proc/<pid>, the
// same source the cluster CronJob used.
func findFirecrackerChildren(parentPID int) ([]firecrackerChild, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	// Fail safe: with no own-cgroup fingerprint we can't prove ownership
	// of any candidate, so reap nothing rather than risk a peer's VMs.
	ownCgroup := cgroupOwnerID("self")
	if ownCgroup == "" {
		return nil, nil
	}
	var out []firecrackerChild
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			// Process exited between ReadDir and Read — fine.
			continue
		}
		if strings.TrimSpace(string(comm)) != "firecracker" {
			continue
		}
		// Only reap firecracker processes that share our cgroup (i.e.
		// were launched by THIS fcagent). A peer DaemonSet's VM lives in
		// a different pod slice and must never be touched.
		if cgroupOwnerID(e.Name()) != ownCgroup {
			continue
		}
		// We still parse state so we can skip zombies — SIGKILL
		// on a defunct entry is a no-op and re-killing every
		// sweep just spams the log. The PPID is read for
		// telemetry only; no longer a filter.
		_, state, err := readPPIDAndState(pid)
		if err != nil {
			continue
		}
		if state == "Z" {
			continue
		}
		info, err := os.Stat(filepath.Join("/proc", e.Name()))
		if err != nil {
			continue
		}
		out = append(out, firecrackerChild{PID: pid, StartedAt: info.ModTime()})
	}
	// parentPID was the old filter argument; kept in the signature
	// so external callers (tests, future code) don't break. The
	// blank assignment documents the intentional non-use.
	_ = parentPID
	return out, nil
}

// readPPIDAndState parses /proc/<pid>/stat for fields 3 (state) and
// 4 (PPID). Field 2 (comm) is in parens and may itself contain
// spaces and parens — split off everything up to the LAST ')' first.
// After the prefix, field 3 → index 0, field 4 → index 1.
func readPPIDAndState(pid int) (int, string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, "", err
	}
	s := string(data)
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+2 > len(s) {
		return 0, "", fmt.Errorf("malformed stat line")
	}
	fields := strings.Fields(s[rparen+2:])
	if len(fields) < 2 {
		return 0, "", fmt.Errorf("stat too short: %d fields", len(fields))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", err
	}
	return ppid, fields[0], nil
}
