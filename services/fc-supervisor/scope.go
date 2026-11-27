package main

// Scope management — the only thing this binary actually does.
//
// Wraps `systemd-run --scope` for spawn, `systemctl stop` for
// teardown, and `systemctl list-units` for enumeration. Since the
// supervisor runs on the host as a systemd service, it talks to
// systemd over its native D-Bus path with no nsenter or
// privilege gymnastics — the kind of clean call site #242 needed
// and didn't get.

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// scopeNamePrefix prefixes every transient scope unit we create.
	// Matches the convention designed in #242 so the host doesn't
	// see two different naming schemes during transition.
	scopeNamePrefix = "everstack-fc-"

	// scopeSlice groups all our scopes under one slice for ops
	// visibility and aggregate accounting. systemd-run creates the
	// slice on first use, no pre-registration needed.
	defaultScopeSlice = "everstack-firecracker.slice"
)

// scopeNameForSandbox returns the systemd unit name we use for a
// given sandbox.
func scopeNameForSandbox(sandboxID string) string {
	return scopeNamePrefix + sandboxID + ".scope"
}

// sandboxIDFromScopeName is the inverse of scopeNameForSandbox.
// Returns ("", false) for unit names that don't match the prefix
// so the lister can skip non-everstack scopes safely.
func sandboxIDFromScopeName(unit string) (string, bool) {
	if !strings.HasPrefix(unit, scopeNamePrefix) {
		return "", false
	}
	if !strings.HasSuffix(unit, ".scope") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(unit, scopeNamePrefix), ".scope"), true
}

// supervisorOpts is the small per-binary config the scope manager
// needs. Kept tiny so main.go's flag handling stays trivial.
type supervisorOpts struct {
	slice            string
	systemdRunPath   string // "systemd-run" — overridable for tests
	systemctlPath    string // "systemctl"   — overridable for tests
	spawnTimeout     time.Duration
	stopTimeout      time.Duration
	queryTimeout     time.Duration
}

func defaultSupervisorOpts() supervisorOpts {
	return supervisorOpts{
		slice:          defaultScopeSlice,
		systemdRunPath: "systemd-run",
		systemctlPath:  "systemctl",
		// Spawn budget: systemd-run --scope returns once the unit
		// is registered AND the leading exec has fork+exec'd.
		// 10s covers a slow systemd manager + slow firecracker
		// fork on a loaded host. Hard cap so a stuck spawn doesn't
		// hold an HTTP handler forever.
		spawnTimeout: 10 * time.Second,
		// Stop budget: systemctl stop blocks until the scope is
		// fully torn down. With KillMode=mixed the SIGTERM →
		// SIGKILL window can run a few seconds. 15s is the
		// outer cap.
		stopTimeout: 15 * time.Second,
		// Query budget: list-units / show are local D-Bus reads,
		// should be sub-second. 5s is plenty of headroom.
		queryTimeout: 5 * time.Second,
	}
}

// spawnVM registers a transient scope owning a fresh firecracker
// process. Blocks until the scope is registered and its MainPID is
// observable; returns those two fields. Errors are wrapped with
// enough context that the HTTP handler can produce useful 5xx
// responses without parsing the underlying systemd error text.
func spawnVM(ctx context.Context, opts supervisorOpts, req SpawnRequest) (*SpawnResponse, error) {
	unit := scopeNameForSandbox(req.SandboxID)
	// The scope holds the firecracker VMM process, whose resident
	// memory is the guest RAM (every page the guest touches) PLUS the
	// VMM's own overhead: heap, virtio queues, page tables. Capping at
	// exactly the guest size OOM-kills the VMM the moment the guest
	// touches all of its pages, which a busy guest does within minutes
	// of boot. 10% + 128MB of headroom covers the observed overhead
	// with margin while keeping the blast radius predictable.
	memBytes := (req.MemoryMB + req.MemoryMB/10 + 128) * 1024 * 1024
	cpuQuota := req.VCPUs * 100 // N vCPUs ⇒ N*100% of one CPU

	args := []string{
		"--scope",
		"--unit=" + unit,
		"--slice=" + opts.slice,
		"--working-directory=" + req.WorkDir,
		"--property=KillMode=mixed",
		// MemoryMax: hard ceiling. Deliberately NOT MemoryHigh
		// (throttle threshold, lets a runaway VM steal pages from
		// peers). Hard cap = predictable per-VM blast radius.
		fmt.Sprintf("--property=MemoryMax=%d", memBytes),
		fmt.Sprintf("--property=CPUQuota=%d%%", cpuQuota),
		// Delegate=yes hands cgroup-controller management of the
		// scope's children to the scope itself. Firecracker doesn't
		// create child cgroups today, but Delegate=yes is the
		// systemd-recommended setting for transient scopes that
		// host their own resource-managed children.
		"--property=Delegate=yes",
		"--",
		req.BinaryPath,
		"--api-sock", req.APISocket,
		"--level", "Warning",
	}

	cmdCtx, cancel := context.WithTimeout(ctx, opts.spawnTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, opts.systemdRunPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("systemd-run scope %s: %w (%s)", unit, err, strings.TrimSpace(string(out)))
	}

	// Query MainPID. systemd populates it asynchronously after the
	// scope's leading exec fork+execs; tight poll loop with a
	// bounded deadline.
	pid, err := waitForMainPID(ctx, opts, unit, 5*time.Second)
	if err != nil {
		// Best-effort cleanup: stop the scope so we don't leak a
		// running firecracker with no record. Ignore the stop error
		// — there's nothing useful at this layer to do with it; the
		// next listing will reveal any lingering unit.
		_ = stopScope(context.Background(), opts, unit)
		return nil, fmt.Errorf("read MainPID for %s: %w", unit, err)
	}

	return &SpawnResponse{ScopeName: unit, MainPID: pid}, nil
}

// stopScope asks systemd to stop the named scope. systemd sends
// SIGTERM, waits for the KillMode-defined window, then SIGKILL.
// Idempotent at the supervisor level: stopping an already-gone
// scope returns nil so callers can treat 404s uniformly.
func stopScope(ctx context.Context, opts supervisorOpts, unit string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, opts.stopTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, opts.systemctlPath, "stop", unit)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// "Unit not loaded" / "not found" means already gone — idempotent.
	msg := strings.ToLower(string(out))
	if strings.Contains(msg, "not loaded") || strings.Contains(msg, "not found") {
		return nil
	}
	return fmt.Errorf("systemctl stop %s: %w (%s)", unit, err, strings.TrimSpace(string(out)))
}

// listScopes returns every active scope under our slice. Source
// of truth is systemd — supervisor caches nothing.
func listScopes(ctx context.Context, opts supervisorOpts) ([]VMInfo, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, opts.queryTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, opts.systemctlPath,
		"list-units",
		"--type=scope",
		"--no-legend",
		"--plain",
		"--all",
		scopeNamePrefix+"*.scope",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var vms []VMInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		unit := fields[0]
		sandboxID, ok := sandboxIDFromScopeName(unit)
		if !ok {
			continue
		}
		// Best-effort detail lookup. If a particular scope's
		// properties can't be read (race with teardown, weird
		// state), skip rather than fail the whole list.
		info, err := describeScope(ctx, opts, unit)
		if err != nil {
			log.Printf("supervisor: skipping %s in list: %v", unit, err)
			continue
		}
		info.SandboxID = sandboxID
		vms = append(vms, *info)
	}
	return vms, nil
}

// describeScope reads MainPID + WorkDir for a single scope.
func describeScope(ctx context.Context, opts supervisorOpts, unit string) (*VMInfo, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, opts.queryTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, opts.systemctlPath,
		"show", unit,
		"-p", "MainPID",
		"-p", "WorkingDirectory",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("systemctl show %s: %w (%s)", unit, err, strings.TrimSpace(string(out)))
	}
	info := &VMInfo{ScopeName: unit}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "MainPID":
			info.MainPID, _ = strconv.Atoi(v)
		case "WorkingDirectory":
			info.WorkDir = v
		}
	}
	return info, nil
}

// mainPIDPattern matches `MainPID=12345` from systemctl show.
var mainPIDPattern = regexp.MustCompile(`(?m)^MainPID=(\d+)$`)

// waitForMainPID polls until the scope's MainPID is non-zero or
// the deadline expires. systemd updates it asynchronously when
// the scope's leading exec fork+execs.
func waitForMainPID(ctx context.Context, opts supervisorOpts, unit string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		pid, err := readMainPID(ctx, opts, unit)
		if err == nil && pid > 0 {
			return pid, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return 0, err
			}
			return 0, fmt.Errorf("MainPID never became non-zero")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// readMainPID is the single-shot variant. Returns (0, nil) when
// the unit exists but its MainPID is still zero — caller decides
// whether to retry.
func readMainPID(ctx context.Context, opts supervisorOpts, unit string) (int, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, opts.queryTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, opts.systemctlPath, "show", "-p", "MainPID", unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("systemctl show: %w", err)
	}
	m := mainPIDPattern.FindStringSubmatch(string(out))
	if len(m) < 2 {
		return 0, fmt.Errorf("MainPID line missing in systemctl show output")
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("parse MainPID %q: %w", m[1], err)
	}
	return pid, nil
}
