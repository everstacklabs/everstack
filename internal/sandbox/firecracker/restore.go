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
// was paused; in-memory state and open files are preserved.
//
// Network: a snapshot captures iface_id + MAC, and LoadSnapshot remaps
// the host_dev_name to opts.Network's freshly allocated TAP. That TAP
// sits on a new /30, but the guest's memory still has eth0 on the /30
// it had at capture. So after the load, the guest is re-addressed over
// vsock (eth0 address, default route, resolv.conf) to opts.Network,
// given a fresh :8080 token, and probed over the TAP. Any failure there
// kills the VM and returns an error: a restored VM is never handed out
// half-networked. Releasing opts.Network stays the caller's job, as it
// owns the allocation.
func RestoreSnapshot(ctx context.Context, store snapshot.Store, opts RestoreOpts) (vm *MicroVM, err error) {
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
	// The store keys manifests by tenant, so this only trips on a
	// corrupt or misplaced manifest. Refuse rather than boot another
	// tenant's memory image under this tenant's ID.
	// An empty manifest tenant also refuses: the store has never written
	// one (it rejects a manifest without a tenant), so a blank value is
	// exactly the corrupt or misplaced manifest this check exists for.
	if manifest.TenantID != opts.TenantID {
		return nil, errors.New("firecracker.RestoreSnapshot: manifest belongs to another tenant")
	}

	// Network reattachment is a per-iface host_dev_name override: the
	// snapshot's MAC + iface_id are preserved (the guest kernel sees no
	// interface change) while the host-side TAP is whatever
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
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: snapshot has network but RestoreOpts.Network is nil")
	case manifest.Network == nil && opts.Network != nil:
		// Caller pre-allocated a TAP but the snapshot was deny-mode.
		// The caller owns the allocation and releases it on any error
		// from here; releasing it here too would free the /30 twice.
		return nil, errors.New("firecracker.RestoreSnapshot: snapshot has no network but RestoreOpts.Network is set")
	}

	boot, err := bootFromSnapshotFn(ctx, store, opts, overrides)
	if err != nil {
		return nil, err
	}
	// From here a booted firecracker process exists. A panic must not
	// leak it: kill it and turn the panic into an error, so the caller's
	// error path releases the TAP, the iptables rules and the /30.
	defer func() {
		if r := recover(); r != nil {
			boot.kill()
			vm, err = nil, fmt.Errorf("firecracker.RestoreSnapshot: panic after boot: %v", r)
		}
	}()

	// The vsock guest CID lives in the snapshot state and is not in the
	// manifest. Host-initiated vsock (CONNECT over the UDS) does not need
	// it, so the restored VM's Vsock client carries 0 (see
	// readdressRestoredGuest).
	vm = &MicroVM{
		ID:         opts.SandboxID,
		SocketPath: boot.socketPath,
		Process:    boot.process,
		VsockPath:  boot.vsockPath,
		RootfsPath: boot.rootfsPath,
		Status:     sandbox.StatusRunning,
		CreatedAt:  time.Now().UTC(),
		Network:    opts.Network,
		workDir:    boot.workDir,
	}
	// TenantID is the one the caller scoped the snapshot lookup and the
	// sandbox record read by, so tenant-scoped checks on the Instance
	// (which copies Config) see the owning tenant, not "".
	vm.Config.TenantID = opts.TenantID
	if opts.AgentID != "" {
		vm.Config.AgentID = opts.AgentID
	}

	if vm.Network != nil {
		if err := readdressGuestFn(ctx, vm); err != nil {
			boot.kill()
			return nil, fmt.Errorf("firecracker.RestoreSnapshot: re-address guest: %w", err)
		}
	}

	logger.WithFields(
		"sandbox_id", opts.SandboxID,
		"tenant_id", opts.TenantID,
		"taken_at", manifest.TakenAt.Format(time.RFC3339),
	).Info("firecracker.RestoreSnapshot: VM resumed from snapshot")

	return vm, nil
}

// snapshotBoot is a firecracker process running a loaded snapshot.
type snapshotBoot struct {
	process    *os.Process
	socketPath string
	vsockPath  string
	rootfsPath string
	workDir    string
	// kill stops and reaps the process.
	kill func()
}

// bootFromSnapshotFn is bootFromSnapshot, swappable in tests: the real
// one needs a firecracker binary and KVM.
var bootFromSnapshotFn = bootFromSnapshot

// readdressGuestFn is readdressRestoredGuest, swappable in tests.
var readdressGuestFn = readdressRestoredGuest

// newGuestAgentClientFn builds the vsock client for a restored guest,
// swappable in tests.
var newGuestAgentClientFn = func(udsPath string) guestAgentClient {
	return NewVsockClient(0, udsPath)
}

// bootFromSnapshot downloads the snapshot streams into a fresh workdir,
// spawns firecracker and loads the snapshot with the VM resumed.
func bootFromSnapshot(ctx context.Context, store snapshot.Store, opts RestoreOpts, overrides []NetworkOverride) (*snapshotBoot, error) {
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

	// Firecracker re-binds the vsock UDS at the path recorded in the
	// snapshot; it is not overridable on load. CreateVM records
	// <WorkDir>/<id>/vsock.sock, which is this path whenever the restore
	// runs with the same WorkDir. Otherwise the guest agent is
	// unreachable and readdressRestoredGuest fails the restore.
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

	// LoadSnapshot supplies machine-config, boot-source, drives, and
	// vsock all in one call — no individual PUTs needed.
	apiClient := newVMAPIClient(socketPath)
	loadCtx, loadCancel := context.WithTimeout(ctx, 60*time.Second)
	defer loadCancel()
	if err := apiClient.LoadSnapshot(loadCtx, statePath, memPath, true, overrides...); err != nil {
		killAndReap()
		return nil, fmt.Errorf("firecracker.RestoreSnapshot: load: %w", err)
	}

	return &snapshotBoot{
		process:    cmd.Process,
		socketPath: socketPath,
		vsockPath:  vsockPath,
		rootfsPath: rootfsPath,
		workDir:    vmWorkDir,
		kill:       killAndReap,
	}, nil
}

// readdressRestoredGuest moves a resumed guest onto vm.Network, the /30
// allocated for this restore. The guest's memory still holds eth0 on the
// /30 it had at capture, with a default route and resolv.conf pointing at
// the old host IP; the new TAP's anti-spoof rule drops anything not
// sourced from the new GuestIP. This runs the same vsock setup CreateVM
// does, in readdress mode (flush eth0, add the new address, replace the
// default route), writes resolv.conf to point at the new host IP (the
// per-VM DNS proxy), and pushes a fresh :8080 token: the token in the
// guest's memory belonged to the old VM, and no host holds it any more.
//
// The final step probes the agent over the TAP at the new GuestIP, so a
// nil return means the anti-spoof pin and the guest agree on its address.
// Unlike CreateVM, vsock transport errors are not waved through: a
// guest that cannot be re-addressed has no usable network.
func readdressRestoredGuest(ctx context.Context, vm *MicroVM) error {
	if vm == nil || vm.Network == nil {
		return errors.New("no network to re-address")
	}
	client := newGuestAgentClientFn(vm.VsockPath)
	if err := client.WaitReady(ctx, 10*time.Second); err != nil {
		return fmt.Errorf("guest agent not ready: %w", err)
	}
	token, err := generateAgentToken()
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		lastErr = configureGuestNetworkMode(ctx, vm.ID, client, vm.Network, token, true)
		if lastErr == nil {
			break
		}
		logger.WithFields("sandbox_id", vm.ID, "attempt", attempt, "error", lastErr.Error()).
			Warn("firecracker.RestoreSnapshot: guest re-address failed")
		if attempt == 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	if lastErr != nil {
		return lastErr
	}

	if vc, ok := client.(*VsockClient); ok {
		vm.Vsock = vc
	}
	vm.AgentToken = token
	return nil
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
