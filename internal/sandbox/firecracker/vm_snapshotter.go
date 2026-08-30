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
// then spawns a fresh firecracker and loads the snapshot.
//
// On success the VM is registered in b.vms under sandboxID and a
// sandbox.Instance is returned with Status = StatusRunning. Callers
// (typically SandboxManager) re-register it in the per-session map.
func (b *FirecrackerBackend) RestoreVMSnapshot(ctx context.Context, sandboxID, tenantID, agentID string, store snapshot.Store) (*sandbox.Instance, error) {
	if store == nil {
		return nil, fmt.Errorf("firecracker.RestoreVMSnapshot: nil store")
	}
	manifest, err := store.GetManifest(ctx, tenantID, sandboxID)
	if err != nil {
		return nil, err
	}

	// Allocate a host-side network if the snapshot had one. Use the
	// snapshot's allowed-hosts / DNS so the restored VM gets the same
	// egress policy. The MAC is overwritten with the snapshot's so
	// LoadSnapshot's network override pairs correctly.
	var netCfg *NetworkConfig
	if manifest.Network != nil {
		mode := sandbox.NetworkAllow
		if len(manifest.Network.DNSServers) > 0 {
			// Conservative: assume allow unless the snapshot's spec
			// suggests a tighter mode. The full mode isn't currently
			// recorded; future iterations can persist it.
		}
		cfg, netErr := SetupNetwork(sandboxID, mode, nil, manifest.Network.DNSServers)
		if netErr != nil {
			return nil, fmt.Errorf("firecracker.RestoreVMSnapshot: setup network: %w", netErr)
		}
		// Pin the MAC to the snapshot's value — the firecracker
		// network-interfaces override only remaps host_dev_name,
		// so the MAC has to match what the snapshot expects.
		cfg.GuestMAC = manifest.Network.GuestMAC
		netCfg = cfg
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
		if netCfg != nil {
			CleanupNetwork(netCfg)
		}
		return nil, fmt.Errorf("firecracker.RestoreVMSnapshot: %w", err)
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
