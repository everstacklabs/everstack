package firecracker

// Firecracker launch dispatch — single decision point for "do we
// route through the host-side supervisor or do we exec firecracker
// directly?"
//
// When FC_SUPERVISOR_SOCKET is set AND the supervisor is reachable
// at backend init, every CreateVM goes through the supervisor.
// Otherwise we fall back to the legacy direct-exec path (children
// of fcagent's pod cgroup; killed on pod replacement). The
// fallback exists so deployments without the supervisor installed
// keep working — flag flip is the only thing that changes
// behavior.
//
// Design rationale: docs/design/fc-supervisor.md.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// consoleTailBytes caps how much of the guest serial console is replayed
// into the agent log when a VM exits. ~16 KiB carries a kernel panic plus
// the OOM-killer process table that precedes it without flooding logs.
const consoleTailBytes = 16 * 1024

// readConsoleTail returns the last max bytes of the guest console log as a
// string for post-mortem logging. Best-effort: any read error yields a
// short marker rather than failing the VM-exit path, and an empty file (a
// VM that died before printing anything) reports "<empty>".
func readConsoleTail(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("<unavailable: %v>", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Sprintf("<unavailable: %v>", err)
	}
	if info.Size() == 0 {
		return "<empty>"
	}
	if info.Size() > max {
		if _, err := f.Seek(info.Size()-max, io.SeekStart); err != nil {
			return fmt.Sprintf("<unavailable: %v>", err)
		}
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return fmt.Sprintf("<unavailable: %v>", err)
	}
	if s := strings.TrimSpace(string(buf)); s != "" {
		return s
	}
	return "<empty>"
}

// supervisorEnvVar is the env var fcagent reads at startup to
// decide whether to route VM launches through the host-side
// supervisor. Value is the path to the supervisor's unix socket
// (typically /var/run/everstack/fc-supervisor.sock).
const supervisorEnvVar = "FC_SUPERVISOR_SOCKET"

// supervisorOnce + supervisorClient + supervisorReady are the
// package-level wiring for the optional supervisor path. Computed
// lazily on first CreateVM and cached so we pay the env lookup +
// health probe once, not per request.
var (
	supervisorOnce    sync.Once
	supervisorClient  *SupervisorClient
	supervisorReady   bool
	supervisorInitErr error
)

// initSupervisor probes FC_SUPERVISOR_SOCKET and decides whether
// fcagent should route through the supervisor for VM lifecycle.
// Idempotent + safe for concurrent use via sync.Once.
//
// Failure modes:
//   - env var unset             → supervisorReady=false, no error logged
//   - socket missing / unreach  → supervisorReady=false, WARN log
//   - reachable                 → supervisorReady=true,  INFO log
//
// Once decided, the answer is sticky for the process lifetime.
// Operator changes to the env require a fcagent restart to pick
// up (acceptable — this is a deployment-level switch, not a
// runtime toggle).
func initSupervisor() {
	supervisorOnce.Do(func() {
		socket := os.Getenv(supervisorEnvVar)
		if socket == "" {
			return
		}
		client := NewSupervisorClient(socket)
		probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Health(probeCtx); err != nil {
			log.Printf("firecracker: %s set to %q but supervisor unreachable: %v — falling back to direct exec", supervisorEnvVar, socket, err)
			supervisorInitErr = err
			return
		}
		supervisorClient = client
		supervisorReady = true
		log.Printf("firecracker: supervisor at %s is reachable — VM launches will go through it", socket)
	})
}

// launchResult holds whatever CreateVM needs from a successful
// launch — regardless of whether the supervisor or direct exec
// produced it. The two paths converge here.
type launchResult struct {
	// Process is the firecracker process handle. With direct exec
	// this is cmd.Process (we own it). With the supervisor this
	// is os.FindProcess(MainPID) — visible because hostPID=true,
	// but NOT a child of fcagent; signals work, Wait() does not.
	Process *os.Process

	// ScopeName is non-empty only on the supervisor path. Stored
	// on MicroVM.ScopeName so Destroy knows whether to call
	// supervisor.Stop or Process.Kill.
	ScopeName string

	// killAndReap is the cleanup primitive create-error paths
	// invoke when something goes wrong AFTER launch but BEFORE
	// CreateVM returns. Both paths produce one; semantics are
	// equivalent (best-effort tear down everything we just
	// brought up).
	killAndReap func()
}

// launchFirecracker is the single point where we decide between
// supervisor-mediated launch and direct exec.Command. Returns the
// same shape for both paths so CreateVM stays branch-free below
// this call.
func launchFirecracker(cfg FirecrackerConfig, opts LaunchOptions) (*launchResult, error) {
	initSupervisor()

	// Supervisor path: try first if we decided at init time it
	// was reachable. A per-request failure here is NOT silently
	// converted to the direct-exec fallback — once we've committed
	// to the supervisor for this process lifetime, a failed spawn
	// is a real error the caller should surface. (Flipping
	// strategies mid-stream would create scope vs. pod-child
	// inconsistency across VMs on the same node, which is exactly
	// the kind of mixed state #244 had to revert.)
	if supervisorReady {
		return launchViaSupervisor(opts)
	}

	// Direct exec fallback: legacy path, same behavior as
	// pre-supervisor. Children of fcagent's pod cgroup; pod
	// replacement kills them. Acceptable for deployments without
	// the supervisor installed.
	return launchViaDirectExec(cfg, opts)
}

// launchViaSupervisor delegates to the host-side daemon.
func launchViaSupervisor(opts LaunchOptions) (*launchResult, error) {
	spawnCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := supervisorClient.Spawn(spawnCtx, opts)
	if err != nil {
		if errors.Is(err, ErrSupervisorUnreachable) {
			// Supervisor was reachable at init but died since.
			// Don't silently fall back — the deployment is in a
			// degraded state operators need to see. Surface as a
			// hard error so the caller's retry path can do
			// something user-visible.
			return nil, fmt.Errorf("supervisor became unreachable: %w", err)
		}
		return nil, fmt.Errorf("supervisor spawn: %w", err)
	}
	// os.FindProcess on Linux always succeeds for any numeric PID
	// regardless of liveness — we get a *Process we can Signal()
	// but cannot Wait() on (it's not our child). That's fine:
	// killAndReap delegates to the supervisor's Stop which does
	// the systemctl dance + cgroup teardown, no Wait needed.
	proc, _ := os.FindProcess(resp.MainPID)
	return &launchResult{
		Process:   proc,
		ScopeName: resp.ScopeName,
		killAndReap: func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer stopCancel()
			_ = supervisorClient.Stop(stopCtx, opts.SandboxID)
		},
	}, nil
}

// launchViaDirectExec is the legacy fcagent-as-parent launcher.
// Behaviorally identical to the pre-supervisor world, with two
// additions: the guest serial console is captured to a file, and the
// child is reaped (and its exit logged) the moment it dies.
func launchViaDirectExec(cfg FirecrackerConfig, opts LaunchOptions) (*launchResult, error) {
	cmd := exec.Command(opts.BinaryPath,
		"--api-sock", opts.APISocket,
		"--level", "Warning",
	)
	cmd.Dir = opts.WorkDir

	// Guest console (console=ttyS0 lands on firecracker's stdout) plus
	// firecracker's own stderr go to <workdir>/console.log. Without
	// this the console went to /dev/null, and since the boot args use
	// "panic=1 reboot=k" (any guest panic or reboot exits the VMM),
	// every in-guest crash was invisible: the VM just vanished and the
	// only trace was a health-probe miss up to 20s later.
	consolePath := filepath.Join(opts.WorkDir, "console.log")
	console, consoleErr := os.OpenFile(consolePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if consoleErr != nil {
		log.Printf("firecracker: cannot open %s: %v (guest console will be discarded)", consolePath, consoleErr)
	} else {
		cmd.Stdout = console
		cmd.Stderr = console
	}

	if err := cmd.Start(); err != nil {
		if console != nil {
			_ = console.Close()
		}
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	// Reap the child as soon as it exits. This avoids zombie
	// firecracker processes (nothing else Waits until Destroy) and,
	// more importantly, records the exact death time + exit status of
	// VMs that die outside our control (guest panic, host OOM kill).
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		waitErr := cmd.Wait()
		if console != nil {
			_ = console.Close()
		}
		// Replay the guest's final serial console into the agent's own
		// log (kubectl logs) BEFORE the workdir reconciler reaps
		// console.log ~5min later. Without this the kernel panic /
		// OOM-killer table that actually killed the VM lives only in a
		// file that's about to be deleted, so a 'vm_not_found' death
		// stays undiagnosable (see project_sandbox_vm_5min_death). The
		// tail is centralized + durable; the file remains for deep dives
		// until reap.
		tail := readConsoleTail(consolePath, consoleTailBytes)
		if waitErr != nil {
			log.Printf("firecracker: VM process for sandbox %s exited: %v (guest console: %s)\n--- guest console tail (%s) ---\n%s\n--- end guest console tail ---",
				opts.SandboxID, waitErr, consolePath, opts.SandboxID, tail)
		} else {
			log.Printf("firecracker: VM process for sandbox %s exited with status 0, guest-initiated shutdown or reboot (guest console: %s)\n--- guest console tail (%s) ---\n%s\n--- end guest console tail ---",
				opts.SandboxID, consolePath, opts.SandboxID, tail)
		}
	}()

	// Reference cfg so any future use of legacy-path-specific
	// fields (e.g., binary path overrides via cfg) compiles
	// without churn. Today the only field we need is BinaryPath
	// which already lives on LaunchOptions.
	_ = cfg
	return &launchResult{
		Process: cmd.Process,
		killAndReap: func() {
			_ = cmd.Process.Kill()
			// The wait goroutine above is the single reaper; block
			// until it has collected the exit status.
			<-waitDone
		},
	}, nil
}

// stopVMProcess is the Destroy-side counterpart to launchFirecracker.
// Dispatches on vm.ScopeName: supervisor stop when non-empty, direct
// signal kill when empty (legacy path). Idempotent in both cases.
func stopVMProcess(ctx context.Context, vm *MicroVM) {
	if vm == nil {
		return
	}
	if vm.ScopeName != "" {
		stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := supervisorClient.Stop(stopCtx, vm.ID); err != nil {
			log.Printf("firecracker: supervisor stop %s failed (vm may linger): %v", vm.ID, err)
		}
		return
	}
	if vm.Process != nil {
		_ = vm.Process.Kill()
		// Wait only works for processes we forked; harmless to
		// call but it'll return an error for inherited PIDs.
		// Best-effort.
		_, _ = vm.Process.Wait()
	}
}
