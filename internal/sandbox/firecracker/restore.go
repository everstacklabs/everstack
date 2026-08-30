package firecracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// RestoreOpts is the set of inputs RestoreSnapshot needs to bring a
// microVM back from object storage. It's distinct from FirecrackerConfig
// + InstanceConfig because the restore path uses *less* of those — most
// VM parameters come from the snapshot itself.
type RestoreOpts struct {
	// SandboxID is the original sandbox ID; used to compute the
	// snapshot prefix in object storage and to name the workdir.
	SandboxID string
	// TenantID scopes the snapshot lookup. Required.
	TenantID string
	// AgentID is the persistent agent that owns this sandbox, if any.
	// Used purely for logging/labels — not load-bearing.
	AgentID string
	// FCBinary is the path to the firecracker binary on the host
	// performing the restore.
	FCBinary string
	// WorkDirRoot is the parent directory under which a fresh
	// per-VM workdir will be created (matches FirecrackerConfig.WorkDir
	// semantics).
	WorkDirRoot string
	// Network, when non-nil, is the host-side network setup that has
	// already been created (TAP device etc.) and is ready for the
	// restored VM to attach to. Restore does NOT allocate networks
	// itself — the caller does that so it can reuse the existing
	// SetupNetwork code path and preserve CIDR assignment policy.
	Network *NetworkConfig
}

// RestoreSnapshot downloads a previously-saved firecracker snapshot
// from `store` and boots a new microVM directly from it on the local
// host. The VM resumes execution from the exact instruction where it
// was paused; in-memory state, open files, and network connections
// are preserved.
//
// Network reattachment caveat: a snapshot captures iface_id + MAC but
// the host_dev_name (TAP device) must exist on the new host before
// LoadSnapshot is called. Pass opts.Network with a freshly-allocated
// TAP whose name matches the one used at snapshot time — otherwise
// the VM boots without functional networking. The caller is
// responsible for that mapping.
func RestoreSnapshot(ctx context.Context, store snapshot.Store, opts RestoreOpts) (*MicroVM, error) {
	if store == nil {
		return nil, errors.New("firecracker.RestoreSnapshot: nil store")
	}
	if opts.SandboxID == "" || opts.TenantID == "" {
		return nil, errors.New("firecracker.RestoreSnapshot: SandboxID and TenantID required")
	}
	if opts.FCBinary == "" || opts.WorkDirRoot == "" {
		return nil, errors.New("firecracker.RestoreSnapshot: FCBinary and WorkDirRoot required")
	}

	manifest, err := store.GetManifest(ctx, opts.TenantID, opts.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: manifest: %w", err)
	}
	if manifest.Backend != "firecracker" {
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: manifest backend is %q, not firecracker", manifest.Backend)
	}

	vmWorkDir := filepath.Join(opts.WorkDirRoot, opts.SandboxID)
	if err := os.MkdirAll(vmWorkDir, 0o750); err != nil {
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: workdir: %w", err)
	}

	// Download all three streams to the workdir. The rootfs becomes
	// the VM's live disk; state + memory feed the load_snapshot call
	// and can be unlinked after load (the kernel mmaps them through
	// the snapshot path).
	statePath := filepath.Join(vmWorkDir, "snapshot-state.fcsnap")
	memPath := filepath.Join(vmWorkDir, "snapshot-memory.bin")
	rootfsPath := filepath.Join(vmWorkDir, "rootfs.ext4")

	if err := downloadKind(ctx, store, opts, snapshot.KindState, statePath); err != nil {
		return nil, err
	}
	if err := downloadKind(ctx, store, opts, snapshot.KindMemory, memPath); err != nil {
		return nil, err
	}
	if err := downloadKind(ctx, store, opts, snapshot.KindRootfs, rootfsPath); err != nil {
		return nil, err
	}

	socketPath := filepath.Join(vmWorkDir, "firecracker.sock")
	vsockPath := filepath.Join(vmWorkDir, "vsock.sock")
	_ = os.Remove(socketPath)
	_ = os.Remove(vsockPath)

	cmd := exec.Command(opts.FCBinary,
		"--api-sock", socketPath,
		"--level", "Warning",
	)
	cmd.Dir = vmWorkDir
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: spawn: %w", err)
	}
	killAndReap := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if err := waitForSocket(socketPath, 30*time.Second); err != nil {
		killAndReap()
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: socket not ready: %w", err)
	}

	apiClient := newVMAPIClient(socketPath)

	// LoadSnapshot supplies machine-config, boot-source, drives, and
	// vsock all in one call — no individual PUTs needed. Network
	// reattachment is handled via a per-iface host_dev_name override:
	// the snapshot's MAC + iface_id are preserved (the guest kernel
	// sees no interface change) while the host-side TAP is whatever
	// SetupNetwork allocated for us on this host.
	var overrides []NetworkOverride
	switch {
	case manifest.Network != nil && opts.Network != nil:
		overrides = []NetworkOverride{{
			IfaceID:     manifest.Network.IfaceID,
			HostDevName: opts.Network.TapDevice,
		}}
	case manifest.Network != nil && opts.Network == nil:
		// Snapshot had a network but caller didn't allocate one.
		// Don't load — networking would be dangling and the VM
		// useless. Fail loudly rather than ship a broken VM.
		killAndReap()
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: snapshot has network but RestoreOpts.Network is nil")
	case manifest.Network == nil && opts.Network != nil:
		// Caller pre-allocated a TAP but the snapshot was deny-mode.
		// Drop it so we don't leak; load with no overrides preserves
		// the snapshot's intent.
		CleanupNetwork(opts.Network)
		opts.Network = nil
	}

	loadCtx, loadCancel := context.WithTimeout(ctx, 60*time.Second)
	defer loadCancel()
	if err := apiClient.LoadSnapshot(loadCtx, statePath, memPath, true, overrides...); err != nil {
		killAndReap()
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: load: %w", err)
	}

	// The vsock guest CID is captured in the snapshot, so the
	// vsockClient must be reconstructed with the same CID. The
	// snapshot manifest doesn't currently carry it; we read the
	// state.json that lives alongside the snapshot (written by
	// recovery.writeVMState during the original Create) to recover
	// the CID + MemoryMB + VCPUs. Snapshot Save uploads vm.workDir's
	// state.json into KindState, so this read works after restore.
	//
	// For Phase 2 first cut, we accept that without state.json we
	// can't fully reconstitute the high-level MicroVM struct — only
	// the running process. Callers that need vsock should pass the
	// CID separately or extend this signature.
	// NOTE: per-VM :8080 auth token. The restored guest still holds the token
	// captured in its memory snapshot, so it stays default-deny protected
	// against peers — this restored MicroVM just has no host-side token (and,
	// per the Phase-2 note above, no reconstructed Vsock), so the host cannot
	// call its authenticated :8080 endpoints. When the vsock reconstruction
	// above is completed, also generate a fresh token and push it via
	// vsockClient.SetAgentToken (as recovery.go does) before any :8080 use.
	vm := &MicroVM{
		ID:         opts.SandboxID,
		SocketPath: socketPath,
		Process:    cmd.Process,
		VsockPath:  vsockPath,
		RootfsPath: rootfsPath,
		Status:     sandbox.StatusRunning,
		CreatedAt:  time.Now().UTC(),
		Network:    opts.Network,
		workDir:    vmWorkDir,
	}
	if opts.AgentID != "" {
		vm.Config.AgentID = opts.AgentID
	}
	logger.WithFields(
		"sandbox_id", opts.SandboxID,
		"tenant_id", opts.TenantID,
		"taken_at", manifest.TakenAt.Format(time.RFC3339),
	).Info("firecracker.RestoreSnapshot: VM resumed from snapshot")

	return vm, nil
}

func downloadKind(ctx context.Context, store snapshot.Store, opts RestoreOpts, kind snapshot.Kind, destPath string) error {
	rc, err := store.GetStream(ctx, opts.TenantID, opts.SandboxID, kind)
	if err != nil {
		return fmt.Errorf("firecracker.RestoreSnapshot: get %s: %w", kind, err)
	}
	defer rc.Close()
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("firecracker.RestoreSnapshot: create %s: %w", destPath, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("firecracker.RestoreSnapshot: write %s: %w", destPath, err)
	}
	return nil
}
