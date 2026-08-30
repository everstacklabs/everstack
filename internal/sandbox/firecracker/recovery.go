package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
)

// vmState is the on-disk snapshot of a MicroVM, persisted to
// <workDir>/<id>/state.json after Create. The agent reads these files
// on startup and rehydrates b.vms so an agent restart doesn't orphan
// every running sandbox.
//
// Field-for-field with the runtime MicroVM struct minus the live
// process handle (re-resolved via os.FindProcess) and vsock client
// (reconstructed from CID + path).
type vmState struct {
	ID         string                 `json:"id"`
	SocketPath string                 `json:"socket_path"`
	VsockPath  string                 `json:"vsock_path"`
	GuestCID   uint32                 `json:"guest_cid"`
	RootfsPath string                 `json:"rootfs_path"`
	PID        int                    `json:"pid"`
	Config     sandbox.InstanceConfig `json:"config"`
	CreatedAt  time.Time              `json:"created_at"`
	ExpiresAt  time.Time              `json:"expires_at"`
	VCPUs      int                    `json:"vcpus"`
	MemoryMB   int64                  `json:"memory_mb"`
	Network    *NetworkConfig         `json:"network,omitempty"`
	WorkDir    string                 `json:"work_dir"`
	// ScopeName records the systemd transient-scope unit owning
	// this VM's firecracker process, when CreateVM took the
	// supervisor-mediated launch path. Empty for VMs created via
	// the direct-exec fallback. Persisted so Destroy after a
	// fcagent restart hits the right tear-down primitive — see
	// launcher.go stopVMProcess.
	ScopeName string `json:"scope_name,omitempty"`
}

func stateFilePath(workDir string) string {
	return filepath.Join(workDir, "state.json")
}

// writeVMState serialises the live MicroVM into <workDir>/state.json.
// Called from Create after the VM is fully initialised; idempotent so
// future state changes (e.g. ExpiresAt extension) can re-snapshot.
func writeVMState(vm *MicroVM) error {
	if vm == nil || vm.workDir == "" {
		return errors.New("writeVMState: vm has no workdir")
	}
	s := vmState{
		ID:         vm.ID,
		SocketPath: vm.SocketPath,
		VsockPath:  vm.VsockPath,
		RootfsPath: vm.RootfsPath,
		Config:     vm.Config,
		CreatedAt:  vm.CreatedAt,
		ExpiresAt:  vm.ExpiresAt,
		VCPUs:      vm.VCPUs,
		MemoryMB:   vm.MemoryMB,
		Network:    vm.Network,
		WorkDir:    vm.workDir,
		ScopeName:  vm.ScopeName,
	}
	if vm.Process != nil {
		s.PID = vm.Process.Pid
	}
	if vm.Vsock != nil {
		s.GuestCID = vm.Vsock.GuestCID()
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vmState: %w", err)
	}
	// Atomic write: tmp + rename, so a crash mid-write doesn't leave a
	// truncated state.json that the next startup would reject.
	tmp := stateFilePath(vm.workDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp state: %w", err)
	}
	return os.Rename(tmp, stateFilePath(vm.workDir))
}

// processAlive returns true if the given PID still maps to a running
// process. Uses signal-0 (no-op signal) — succeeds when the process
// exists, fails with ESRCH when it doesn't. Requires hostPID=true on
// the container so PIDs outside our isolated PID namespace are visible.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// Recover walks the agent's work directory, finds every <id>/state.json,
// and either rehydrates a live VM into b.vms or cleans up the leftovers
// of a dead one. Called from main.go before the gRPC server starts
// accepting requests, so by the time Shell/Exec/Logs lookups happen
// the map is consistent with the actual host state.
//
// Liveness check has two stages:
//  1. signal-0 to the recorded PID (kernel says "is this process still
//     here"). Cheap and necessary — if the firecracker process died
//     during the agent's downtime there's nothing to recover.
//  2. vsock probe via the guest agent's WaitReady. Catches the case
//     where firecracker is alive but the guest kernel hung. Only run
//     for processes that pass stage 1.
//
// Dead VMs get their network state cleaned (TAP, iptables rules) and
// their work directory removed so a future provision of the same ID
// starts clean.
func (b *FirecrackerBackend) Recover(ctx context.Context) error {
	workRoot := b.config.WorkDir
	if workRoot == "" {
		logger.Info("firecracker_recover: WorkDir empty, nothing to recover")
		return nil
	}

	entries, err := os.ReadDir(workRoot)
	if err != nil {
		if os.IsNotExist(err) {
			logger.WithFields("dir", workRoot).Info("firecracker_recover: work dir missing, nothing to recover")
			return nil
		}
		return fmt.Errorf("read workdir %s: %w", workRoot, err)
	}

	var recovered, cleaned, skipped int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		vmDir := filepath.Join(workRoot, id)
		statePath := stateFilePath(vmDir)

		raw, err := os.ReadFile(statePath)
		if err != nil {
			if os.IsNotExist(err) {
				// Workdir without state.json — probably a half-created
				// sandbox from a crash mid-Create. Clean it up.
				logger.WithFields("dir", vmDir).
					Info("firecracker_recover: no state.json, removing orphan workdir")
				_ = os.RemoveAll(vmDir)
				cleaned++
				continue
			}
			logger.WithFields("dir", vmDir, "error", err.Error()).
				Warn("firecracker_recover: state.json unreadable, skipping")
			skipped++
			continue
		}

		var s vmState
		if err := json.Unmarshal(raw, &s); err != nil {
			logger.WithFields("dir", vmDir, "error", err.Error()).
				Warn("firecracker_recover: state.json corrupt, removing")
			_ = os.RemoveAll(vmDir)
			cleaned++
			continue
		}

		if !processAlive(s.PID) {
			logger.WithFields("sandbox_id", id, "pid", s.PID).
				Info("firecracker_recover: firecracker process gone, cleaning up")
			CleanupNetwork(s.Network)
			_ = os.RemoveAll(vmDir)
			cleaned++
			continue
		}

		// Process is alive. Confirm the guest agent is responsive via
		// vsock — firecracker can be "running" with a wedged guest.
		vsockClient := NewVsockClient(s.GuestCID, s.VsockPath)
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = vsockClient.WaitReady(probeCtx, 3*time.Second)
		cancel()
		if err != nil {
			logger.WithFields("sandbox_id", id, "pid", s.PID, "error", err.Error()).
				Warn("firecracker_recover: vsock unresponsive; killing wedged VM and cleaning up")
			if proc, ferr := os.FindProcess(s.PID); ferr == nil {
				_ = proc.Kill()
			}
			CleanupNetwork(s.Network)
			_ = os.RemoveAll(vmDir)
			cleaned++
			continue
		}

		// Verify the TAP device from the persisted state still exists.
		// With hostNetwork: true the TAP lives in the host netns and
		// survives pod replacement. If it's gone (manual cleanup, host
		// reboot), treat the VM as dead — the guest has no network.
		if s.Network != nil && s.Network.TapDevice != "" {
			if _, tapErr := net.InterfaceByName(s.Network.TapDevice); tapErr != nil {
				logger.WithFields("sandbox_id", id, "tap", s.Network.TapDevice).
					Warn("firecracker_recover: TAP device gone — network unrecoverable, killing VM")
				if proc, ferr := os.FindProcess(s.PID); ferr == nil {
					_ = proc.Kill()
				}
				CleanupNetwork(s.Network)
				_ = os.RemoveAll(vmDir)
				cleaned++
				continue
			}
		}

		// Everything checks out. Rebuild the MicroVM and put it in the
		// map. We don't re-Wait() the process — we didn't fork it this
		// time around, so cmd.Wait semantics don't apply. Kill() works
		// regardless of parentage for cleanup later.
		proc, _ := os.FindProcess(s.PID)
		vm := &MicroVM{
			ID:         s.ID,
			SocketPath: s.SocketPath,
			Process:    proc,
			VsockPath:  s.VsockPath,
			RootfsPath: s.RootfsPath,
			Status:     sandbox.StatusRunning,
			Config:     s.Config,
			CreatedAt:  s.CreatedAt,
			ExpiresAt:  s.ExpiresAt,
			VCPUs:      s.VCPUs,
			MemoryMB:   s.MemoryMB,
			Vsock:      vsockClient,
			Network:    s.Network,
			workDir:    s.WorkDir,
			ScopeName:  s.ScopeName,
		}

		// Re-claim the VM's /30 subnet in the allocator so a future
		// AllocateSubnet on this host doesn't hand the same slot to a
		// new VM. The iptables rules + TAP device survived the agent
		// restart unmodified, so the slot really is still in use.
		if s.Network != nil && s.Network.HostIP != "" {
			if n, parseErr := subnetFromHostIP(s.Network.HostIP); parseErr == nil {
				if reserveErr := ReserveSubnet(n); reserveErr != nil {
					logger.WithFields("sandbox_id", id, "subnet", n, "error", reserveErr.Error()).
						Warn("firecracker_recover: subnet already claimed during recovery — collision risk")
				}
			}
		}

		// Re-apply iptables rules for the recovered VM. With hostNetwork
		// the rules survived in the host netns, but an operator may have
		// flushed them during debugging, or a kube-proxy reconcile may
		// have disturbed the chain ordering. configureIptables is
		// idempotent (flush + recreate), so this is safe.
		if s.Network != nil && s.Network.Mode != sandbox.NetworkDeny {
			if iptErr := configureIptables(s.Network); iptErr != nil {
				logger.WithFields("sandbox_id", id, "error", iptErr.Error()).
					Warn("firecracker_recover: failed to re-apply iptables — guest egress may be broken")
			}
		}

		// Non-deny VMs depend on a per-VM DNS proxy listening on the
		// TAP's host IP. The iptables rules direct DNS at hostIP:53
		// — we just need to put the listener back. AllowedHosts and
		// DNSServers were persisted; reconstruct from those.
		if s.Network != nil && s.Network.Mode != sandbox.NetworkDeny {
			proxy, perr := startVMDNSProxy(s.ID, s.Network.HostIP, s.Network.Mode, s.Network.AllowedHosts, s.Network.DNSServers)
			if perr != nil {
				logger.WithFields("sandbox_id", id, "error", perr.Error()).
					Warn("firecracker_recover: failed to restart DNS proxy — guest DNS will fail until next reprovision")
			} else if vm.Network != nil {
				vm.Network.DNSProxy = proxy
			}
		}

		// Restart the health monitor for recovered VMs. The agent
		// inside the guest is still running (recovery only happens
		// when the firecracker process survived our restart), so
		// /health should answer immediately. If it doesn't, the
		// monitor will flip to unhealthy on the first tick and the
		// operator gets a clear signal that this recovered VM is
		// actually broken.
		// Seed lifecycle state for the recovered VM. We don't know if
		// it's actually healthy until the monitor's first probe — but
		// it WAS running when we wrote state.json, so treat it as
		// Healthy and let the next tick flip to Unhealthy if the
		// agent has since died. Avoids a "thought VM was dead, was
		// actually fine" cleanup race on rehydration.
		vm.Lifecycle.Init(LifecycleHealthy)

		if vm.Network != nil && vm.Network.GuestIP != "" && vm.Network.Mode != sandbox.NetworkDeny {
			vm.Health = NewHealthMonitor(id, vm.Network.GuestIP)
			vm.Health.Start(context.Background())
			recoveredVM := vm
			vm.Health.SetTransitionHook(func(healthy bool) {
				if healthy {
					_, _ = recoveredVM.Lifecycle.Transition(recoveredVM.ID, LifecycleHealthy)
				} else {
					_, _ = recoveredVM.Lifecycle.Transition(recoveredVM.ID, LifecycleUnhealthy)
				}
			})
		}

		b.mu.Lock()
		b.vms[id] = vm
		b.mu.Unlock()

		// Re-establish the per-VM :8080 auth token. This fcagent process
		// restarted and lost the in-memory token, but the guest agent kept
		// running (a fcagent restart doesn't restart the guest) and still holds
		// its original boot token. Generate a fresh token and push it over
		// vsock (host-only) so host and guest agree again — set_agent_token
		// overwrites the guest's value. Without this, every authenticated :8080
		// call below (and after) would 401 and silently fall back to vsock.
		if vm.Network != nil && vm.Network.GuestIP != "" && vm.Vsock != nil {
			if token, terr := generateAgentToken(); terr == nil {
				tokCtx, tokCancel := context.WithTimeout(ctx, 3*time.Second)
				if perr := vm.Vsock.SetAgentToken(tokCtx, token); perr == nil {
					vm.AgentToken = token
				} else {
					logger.WithFields("sandbox_id", id, "error", perr.Error()).
						Warn("firecracker_recover: failed to re-push agent token; :8080 will fall back to vsock until next reprovision")
				}
				tokCancel()
			}
		}

		// Probe the guest for its live tmux sessions and record the
		// count. The sessions themselves survived the agent restart
		// (they live inside the VM, not in this process); this just
		// confirms the host can talk to them. Best-effort — a probe
		// failure means the host can still create new sessions, just
		// without the "you have N pre-existing sessions" UX hint.
		probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
		sessions, sErr := vm.ToolboxListSessions(probeCtx)
		probeCancel()

		// Surface the recovery in the per-VM log so operators viewing
		// the sandbox can see when the agent reattached.
		recoveryLine := "agent restarted; sandbox state recovered from disk"
		if sErr == nil && len(sessions) > 0 {
			recoveryLine = fmt.Sprintf("agent restarted; %d persistent shell session(s) preserved", len(sessions))
		}
		b.getOrCreateLogs(id).Append(logbuffer.Entry{
			Timestamp: time.Now(),
			Stream:    "system",
			Line:      recoveryLine,
		})
		logger.WithFields("sandbox_id", id, "pid", s.PID, "shell_sessions", len(sessions)).
			Info("firecracker_recover: VM rehydrated")
		recovered++
	}

	logger.WithFields("recovered", recovered, "cleaned", cleaned, "skipped", skipped).
		Info("firecracker_recover: complete")
	return nil
}
