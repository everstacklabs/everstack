package firecracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// SnapshotTrigger labels why a snapshot was taken so the manifest /
// metrics can distinguish user-initiated sleep from background
// periodic snapshots.
type SnapshotTrigger string

const (
	TriggerSleep    SnapshotTrigger = "sleep"
	TriggerPeriodic SnapshotTrigger = "periodic"
	TriggerShutdown SnapshotTrigger = "shutdown"
)

// SaveSnapshot pauses the VM, writes a firecracker snapshot to disk,
// streams (state file, memory image, rootfs overlay) to the
// snapshot.Store, then resumes the VM. On error the VM is best-effort
// resumed so a failed snapshot doesn't leave a running sandbox stuck
// in the Paused state.
//
// The function is safe to call against a paused VM (Pause is
// idempotent at the Firecracker API level for "already paused" only
// in newer versions — we treat the error as fatal so callers don't
// silently snapshot a VM whose state they didn't intend to change).
func SaveSnapshot(ctx context.Context, vm *MicroVM, tenantID, agentID string, store snapshot.Store, trigger SnapshotTrigger) (*snapshot.Manifest, error) {
	if vm == nil {
		return nil, errors.New("firecracker.SaveSnapshot: nil vm")
	}
	if store == nil {
		return nil, errors.New("firecracker.SaveSnapshot: nil store")
	}
	if tenantID == "" {
		return nil, errors.New("firecracker.SaveSnapshot: tenantID required")
	}

	client := newVMAPIClient(vm.SocketPath)

	// Stage local file paths inside the VM's workdir so cleanup is
	// scoped and snapshots from one VM never collide with another.
	stage := filepath.Join(vm.workDir, "snapshot")
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return nil, fmt.Errorf("firecracker.SaveSnapshot: stage dir: %w", err)
	}
	statePath := filepath.Join(stage, "state.fcsnap")
	memPath := filepath.Join(stage, "memory.bin")

	// Pause + snapshot + resume must form one logical operation. If
	// we fail to resume after a successful snapshot, the VM stays
	// paused and the next request to it will hang. Always attempt
	// resume in a deferred path.
	pauseCtx, pauseCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pauseCancel()
	if err := client.Pause(pauseCtx); err != nil {
		return nil, fmt.Errorf("firecracker.SaveSnapshot: pause: %w", err)
	}
	resumed := false
	defer func() {
		if resumed {
			return
		}
		resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer resumeCancel()
		if err := client.Resume(resumeCtx); err != nil {
			logger.WithFields("vm_id", vm.ID, "error", err.Error()).
				Error("firecracker.SaveSnapshot: failed to resume after snapshot error; VM may be stuck paused")
		}
	}()

	createCtx, createCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer createCancel()
	if err := client.CreateSnapshot(createCtx, statePath, memPath, SnapshotTypeFull); err != nil {
		return nil, fmt.Errorf("firecracker.SaveSnapshot: create: %w", err)
	}

	// Resume now — the snapshot files are on disk, the VM can keep
	// serving traffic while we upload. Upload failures don't roll
	// back the snapshot (firecracker has no concept of un-snapshot),
	// they just leave us without a remote copy this cycle. The next
	// trigger will try again.
	resumeCtx, resumeCancel := context.WithTimeout(ctx, 5*time.Second)
	if err := client.Resume(resumeCtx); err != nil {
		resumeCancel()
		return nil, fmt.Errorf("firecracker.SaveSnapshot: resume: %w", err)
	}
	resumeCancel()
	resumed = true

	manifest := &snapshot.Manifest{
		SandboxID: vm.ID,
		TenantID:  tenantID,
		AgentID:   agentID,
		Backend:   "firecracker",
		TakenAt:   time.Now().UTC(),
		Trigger:   string(trigger),
	}
	// Capture network identity so the restore path can patch the
	// firecracker config with a matching guest MAC + iface_id before
	// load_snapshot. Without this the restored VM either boots with a
	// different MAC (guest kernel sees an interface flap) or no host-
	// side TAP attached at all.
	if vm.Network != nil {
		manifest.Network = &snapshot.NetworkSpec{
			IfaceID:    "eth0",
			GuestMAC:   vm.Network.GuestMAC,
			GuestIP:    vm.Network.GuestIP,
			HostIP:     vm.Network.HostIP,
			MTU:        vm.Network.MTU,
			DNSServers: vm.Network.GuestResolvers(),
		}
	}

	// Stream each kind to the store as a file reader so we never
	// buffer multi-GB rootfs in memory.
	for _, item := range []struct {
		kind        snapshot.Kind
		localPath   string
		contentType string
	}{
		{snapshot.KindState, statePath, "application/octet-stream"},
		{snapshot.KindMemory, memPath, "application/octet-stream"},
		{snapshot.KindRootfs, vm.RootfsPath, "application/octet-stream"},
	} {
		rec, err := streamFileToStore(ctx, store, tenantID, vm.ID, item.kind, item.contentType, item.localPath)
		if err != nil {
			return nil, fmt.Errorf("firecracker.SaveSnapshot: upload %s: %w", item.kind, err)
		}
		manifest.Streams = append(manifest.Streams, rec)
	}

	if err := store.PutManifest(ctx, *manifest); err != nil {
		return nil, fmt.Errorf("firecracker.SaveSnapshot: manifest: %w", err)
	}

	// Snapshot files are reproducible from R2 now — drop the local
	// copies so we don't bloat the host. Keep rootfs (vm.RootfsPath)
	// alone: it's the VM's live disk, not a snapshot artifact.
	_ = os.Remove(statePath)
	_ = os.Remove(memPath)

	logger.WithFields(
		"vm_id", vm.ID,
		"tenant_id", tenantID,
		"trigger", string(trigger),
		"streams", len(manifest.Streams),
	).Info("firecracker.SaveSnapshot: complete")
	return manifest, nil
}

func streamFileToStore(ctx context.Context, store snapshot.Store, tenantID, sandboxID string, kind snapshot.Kind, contentType, localPath string) (snapshot.StreamRecord, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return snapshot.StreamRecord{}, fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()
	rec, err := store.PutStream(ctx, tenantID, sandboxID, kind, contentType, f)
	if err != nil {
		return snapshot.StreamRecord{}, err
	}
	return rec, nil
}
