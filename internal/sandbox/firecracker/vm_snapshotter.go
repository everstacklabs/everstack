package firecracker

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// SaveVMSnapshot implements sandbox.VMSnapshotter. The backend looks up
// the live MicroVM by sandboxID, then delegates to the package-level
// SaveSnapshot orchestrator which handles pause/capture/upload/resume.
//
// Returns an error when the sandbox isn't currently running on this
// host — callers should not treat that as recoverable, it just means
// there's nothing to snapshot from here (the VM may live on a
// different host with the fcagent backend).
func (b *FirecrackerBackend) SaveVMSnapshot(ctx context.Context, sandboxID, tenantID, agentID string, store snapshot.Store, trigger string) (*snapshot.Manifest, error) {
	b.mu.RLock()
	vm, ok := b.vms[sandboxID]
	b.mu.RUnlock()
	if !ok || vm == nil {
		return nil, fmt.Errorf("firecracker.SaveVMSnapshot: vm %s not found on this host", sandboxID)
	}
	return SaveSnapshot(ctx, vm, tenantID, agentID, store, SnapshotTrigger(trigger))
}

// RestoreVMSnapshot implements sandbox.VMRestorer. Reads the manifest
// from the snapshot store, allocates a host-side TAP that matches the
// snapshot's guest MAC (so the guest kernel sees no interface change),
// then spawns a fresh firecracker, loads the snapshot and re-addresses
// the guest onto the new /30. The egress policy comes from policy (the
// sandbox record), never the manifest. On any failure the VM is killed
// and the host network, /30 included, is released.
//
// On success the VM is registered in b.vms under sandboxID and a
// sandbox.Instance is returned with Status = StatusRunning. Callers
// (typically SandboxManager) re-register it in the per-session map.
func (b *FirecrackerBackend) RestoreVMSnapshot(ctx context.Context, sandboxID, tenantID, agentID string, policy sandbox.RestoreNetworkPolicy, store snapshot.Store) (*sandbox.Instance, error) {
	if store == nil {
		return nil, fmt.Errorf("firecracker.RestoreVMSnapshot: nil store")
	}
	manifest, err := store.GetManifest(ctx, tenantID, sandboxID)
	if err != nil {
		return nil, err
	}

	netCfg, err := setupRestoreNetwork(sandboxID, manifest, policy)
	if err != nil {
		return nil, err
	}

	vm, err := RestoreSnapshot(ctx, store, RestoreOpts{
		SandboxID:   sandboxID,
		TenantID:    tenantID,
		AgentID:     agentID,
		FCBinary:    b.config.BinaryPath,
		WorkDirRoot: b.config.WorkDir,
		Network:     netCfg,
	})
	if err != nil {
		// Releases the TAP, iptables rules and the /30. RestoreSnapshot
		// has already killed the VM if it got as far as booting one.
		if netCfg != nil {
			cleanupNetworkFn(netCfg)
		}
		return nil, fmt.Errorf("firecracker.RestoreVMSnapshot: %w", err)
	}

	// Record the policy the VM actually runs under, so policy checks on
	// the Instance (port exposure etc.) see it. No interface means deny.
	if vm.Network == nil {
		vm.Config.NetworkMode = sandbox.NetworkDeny
	} else {
		vm.Config.NetworkMode = policy.Mode
		vm.Config.AllowedHosts = append([]string(nil), policy.AllowedHosts...)
	}

	// Register in the backend's vm map so subsequent backend.Status
	// and backend.Exec calls find the restored VM.
	b.mu.Lock()
	b.vms[sandboxID] = vm
	b.mu.Unlock()

	logger.WithFields(
		"sandbox_id", sandboxID,
		"tenant_id", tenantID,
		"taken_at", manifest.TakenAt.Format(time.RFC3339),
	).Info("firecracker.RestoreVMSnapshot: VM running")

	inst := &sandbox.Instance{
		ID:         sandboxID,
		Status:     sandbox.StatusRunning,
		Backend:    "firecracker",
		CreatedAt:  vm.CreatedAt,
		LastUsedAt: time.Now(),
		Config:     vm.Config,
		AgentID:    agentID,
		Persistent: agentID != "",
	}
	return inst, nil
}

// setupNetworkFn is SetupNetwork, swappable in tests: the real one needs
// root, ip and iptables.
var setupNetworkFn = SetupNetwork

// cleanupNetworkFn is CleanupNetwork, swappable in tests for the same
// reason.
var cleanupNetworkFn = CleanupNetwork

// setupRestoreNetwork allocates the host side of a restored VM's network
// under the record's policy.
//
// The DNS proxy's upstreams are always the operator's (nil here, so
// SetupNetwork falls back to readNodeNameservers / FCAGENT_DNS_UPSTREAMS).
// Nothing from the snapshot is ever used as an upstream: older manifests
// stored the guest's resolver, which is the old TAP host IP, and a proxy
// forwarding there would send this tenant's queries to whichever
// sandbox's proxy owns that address now.
//
// Returns nil when the snapshot has no network (deny mode at capture):
// the VM comes back without an interface whatever the policy says. A
// deny policy with a networked snapshot is refused outright, before any
// stream is downloaded: firecracker cannot load a snapshot whose
// interface has no host side, and giving it one would mean egress.
func setupRestoreNetwork(sandboxID string, manifest *snapshot.Manifest, policy sandbox.RestoreNetworkPolicy) (*NetworkConfig, error) {
	if manifest.Network == nil {
		return nil, nil
	}
	if policy.Mode == sandbox.NetworkDeny {
		return nil, fmt.Errorf("firecracker.RestoreVMSnapshot: sandbox %s has network mode deny but its snapshot has a network interface; refusing to restore", sandboxID)
	}
	cfg, err := setupNetworkFn(sandboxID, policy.Mode, policy.AllowedHosts, nil)
	if err != nil {
		return nil, fmt.Errorf("firecracker.RestoreVMSnapshot: setup network: %w", err)
	}
	// Pin the MAC to the snapshot's value: the firecracker
	// network-interfaces override only remaps host_dev_name, so the MAC
	// has to match what the snapshot expects.
	cfg.GuestMAC = manifest.Network.GuestMAC
	return cfg, nil
}
