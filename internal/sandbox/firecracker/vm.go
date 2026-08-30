package firecracker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// MicroVM represents a running Firecracker microVM instance.
type MicroVM struct {
	ID         string
	SocketPath string // Unix socket for Firecracker API
	Process    *os.Process
	VsockPath  string // Unix socket for vsock host-side
	RootfsPath string // Copy-on-write overlay of base rootfs
	Status     sandbox.Status
	Config     sandbox.InstanceConfig
	CreatedAt  time.Time
	ExpiresAt  time.Time
	VCPUs      int
	MemoryMB   int64
	Vsock      *VsockClient
	Network    *NetworkConfig

	// AgentToken is the per-VM bearer token required on every authenticated
	// :8080 endpoint. Generated at create, pushed to the guest over vsock
	// (host-only), and held in memory ONLY — never persisted to state.json, so
	// a live RCE-grade secret is never written to disk. On fcagent restart the
	// recovery path regenerates and re-pushes it (recovery.go). Empty for
	// NetworkDeny / no-guest-IP VMs, which never use :8080.
	AgentToken string

	// Health monitors the in-guest agent's HTTP /health endpoint at
	// 20s × 100ms ticks. Started once the readiness probe passes in
	// CreateVM, stopped in Destroy. IsHealthy() gives a lock-free
	// read of the latest known liveness state — suitable for
	// scheduler placement decisions and "is this VM still usable"
	// gates. Nil when network is disabled (NetworkDeny mode).
	Health *HealthMonitor

	// Lifecycle is the validated state machine. Every meaningful
	// VM state change goes through Lifecycle.Transition, producing
	// a single audit trail in logs + the everstack_fcagent_vm_
	// transitions_total Prometheus counter. The legacy Status field
	// above is kept for compatibility with sandbox.Backend's
	// interface contract; Lifecycle is the source of truth for
	// internal placement / health / cleanup decisions.
	Lifecycle LifecycleState

	// ScopeName is the systemd transient-scope unit owning this
	// VM's firecracker process when the host-side supervisor was
	// the launcher. Empty when CreateVM took the direct-exec
	// fallback path (no FC_SUPERVISOR_SOCKET, or supervisor
	// unreachable at launch time). Destroy branches on this:
	// non-empty → systemctl-mediated stop via the supervisor;
	// empty → Process.Kill on the child we own.
	ScopeName string

	workDir string
}

// vmAPIClient wraps HTTP calls to the Firecracker REST API over a Unix socket.
type vmAPIClient struct {
	socketPath string
	httpClient *http.Client
}

func newVMAPIClient(socketPath string) *vmAPIClient {
	return &vmAPIClient{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", socketPath, 5*time.Second)
				},
			},
		},
	}
}

func (c *vmAPIClient) put(ctx context.Context, path string, body interface{}) error {
	return c.do(ctx, "PUT", path, body)
}

// patch sends a PATCH request to the Firecracker API. Used for VM
// state transitions (Paused/Resumed) and machine-config edits — the
// Firecracker spec uses PATCH for these so we can't reuse PUT.
func (c *vmAPIClient) patch(ctx context.Context, path string, body interface{}) error {
	return c.do(ctx, "PATCH", path, body)
}

func (c *vmAPIClient) do(ctx context.Context, method, path string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("API %s %s returned %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(respBody))
	}
	return nil
}

// CreateVM creates a new Firecracker microVM with the given configuration.
func CreateVM(cfg FirecrackerConfig, id string, instanceCfg sandbox.InstanceConfig) (*MicroVM, error) {
	networkMode, err := normalizeNetworkMode(instanceCfg.NetworkMode)
	if err != nil {
		return nil, err
	}
	instanceCfg.NetworkMode = networkMode

	vmWorkDir := filepath.Join(cfg.WorkDir, id)
	if err := os.MkdirAll(vmWorkDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create VM work directory: %w", err)
	}

	socketPath := filepath.Join(vmWorkDir, "firecracker.sock")
	vsockPath := filepath.Join(vmWorkDir, "vsock.sock")

	// Clean leftover sockets from a prior failed create on the same
	// sandbox ID. Firecracker fails to bind --api-sock when the path
	// already exists, which then trips waitForSocket. The reconciler
	// retries every ~13s with the same ID/workdir, so without this
	// cleanup the second-and-subsequent attempts cascade-fail off
	// the corpse of the first.
	_ = os.Remove(socketPath)
	_ = os.Remove(vsockPath)

	// Refuse before allocating anything if the store is nearly full.
	// Placed ahead of the clone so a rejected create leaves no partial
	// rootfs behind to be reaped later.
	if err := checkDiskHeadroom(cfg.WorkDir, cfg.MinFreeDiskMB); err != nil {
		return nil, err
	}

	// Create copy-on-write rootfs overlay
	rootfsPath, err := createRootfsOverlay(cfg.RootfsDir, vmWorkDir, instanceCfg.Image, instanceCfg.DiskMB)
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs overlay: %w", err)
	}

	// Resolve vcpus/memoryMB BEFORE the launch — the supervisor
	// path needs them as scope properties (MemoryMax / CPUQuota)
	// so the kernel enforces the per-VM cap regardless of the
	// fcagent pod's resource limits. The direct-exec path also
	// uses these values later for the /machine-config API call;
	// computing them once up front means both paths see the same
	// numbers.
	vcpus := cfg.VM.VCPUs
	memoryMB := cfg.VM.MemoryMB
	if instanceCfg.CPULimit > 0 {
		vcpus = int(instanceCfg.CPULimit)
	}
	if instanceCfg.MemoryMB > 0 {
		memoryMB = instanceCfg.MemoryMB
	}
	// Firecracker requires vcpu_count to be 1 or an even number when SMT is
	// enabled on the host (which it almost always is). Round odd values >1
	// up to the next even number so fractional/odd InstanceConfig.CPULimit
	// values still produce a valid machine-config.
	if vcpus < 1 {
		vcpus = 1
	} else if vcpus > 1 && vcpus%2 != 0 {
		vcpus++
	}

	// The standard guest rootfs is a full Ubuntu with systemd, dockerd
	// and sshd; small guests are prone to in-guest OOM within minutes
	// of boot once services settle and the page cache fills, and with
	// boot args "panic=1 reboot=k" a fatal guest OOM exits the VMM
	// (the sandbox just vanishes). Warn rather than override: sizing
	// is the caller's billing-visible choice, but the operator needs
	// a breadcrumb when a VM dies shortly after boot.
	if memoryMB < 2048 {
		logger.WithFields("sandbox_id", id, "memory_mb", memoryMB).
			Warn("firecracker: guest memory below 2048MB, the default Ubuntu rootfs (systemd+dockerd) may OOM under load")
	}

	// Launch firecracker via the dispatcher. Goes through the
	// host-side supervisor when FC_SUPERVISOR_SOCKET is set and
	// the supervisor is reachable; falls back to direct exec
	// otherwise. See launcher.go for the decision tree.
	launch, err := launchFirecracker(cfg, LaunchOptions{
		SandboxID:  id,
		BinaryPath: cfg.BinaryPath,
		APISocket:  socketPath,
		WorkDir:    vmWorkDir,
		MemoryMB:   memoryMB,
		VCPUs:      vcpus,
	})
	if err != nil {
		return nil, fmt.Errorf("launch firecracker: %w", err)
	}
	killAndReap := launch.killAndReap

	// Wait for socket to become available. 30s budget instead of the
	// original 5s because on the OVH dev cluster (k3s on a single
	// CCX33), firecracker startup is consistently overshooting 5s
	// under load — every create errors at the socket-not-ready check
	// and persistent troopers can't reprovision. The wait itself is
	// cheap (50ms poll on a unix socket), so the higher ceiling
	// only matters when the binary is actually slow.
	if err := waitForSocket(socketPath, 30*time.Second); err != nil {
		killAndReap()
		return nil, fmt.Errorf("Firecracker socket not ready: %w", err)
	}

	apiClient := newVMAPIClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Configure VM via Firecracker API
	if err := apiClient.put(ctx, "/machine-config", map[string]interface{}{
		"vcpu_count":   vcpus,
		"mem_size_mib": memoryMB,
	}); err != nil {
		killAndReap()
		return nil, fmt.Errorf("failed to configure machine: %w", err)
	}

	// random.trust_cpu=on credits the CPU's RDRAND/RDSEED as initialized
	// entropy at boot, so the kernel CRNG is ready immediately. Without it
	// a Firecracker microVM has no entropy source: systemd-random-seed and
	// any crypto-dependent unit (the guest agent's shell server) block on
	// /dev/random for minutes, hanging boot. Captured in production console
	// tails as "A start job is running for Save Random Seed (…/10min)" — the
	// guest never finishes booting and the VM is reaped ~5min in. The
	// virtio-rng device below provides ongoing entropy; this flag fixes the
	// initial-seed blockage on every modern x86 host (RDRAND present).
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off random.trust_cpu=on"
	if err := apiClient.put(ctx, "/boot-source", map[string]interface{}{
		"kernel_image_path": cfg.KernelPath,
		"boot_args":         bootArgs,
	}); err != nil {
		killAndReap()
		return nil, fmt.Errorf("failed to configure boot source: %w", err)
	}

	if err := apiClient.put(ctx, "/drives/rootfs", map[string]interface{}{
		"drive_id":       "rootfs",
		"path_on_host":   rootfsPath,
		"is_root_device": true,
		"is_read_only":   false,
	}); err != nil {
		killAndReap()
		return nil, fmt.Errorf("failed to configure rootfs drive: %w", err)
	}

	// Configure vsock for host↔guest communication
	guestCID := allocateCID()
	if err := apiClient.put(ctx, "/vsock", map[string]interface{}{
		"guest_cid": guestCID,
		"uds_path":  vsockPath,
	}); err != nil {
		killAndReap()
		return nil, fmt.Errorf("failed to configure vsock: %w", err)
	}

	// Attach a virtio-rng entropy device so the guest has an ongoing
	// hardware RNG (/dev/hwrng) feeding its CRNG. Complements
	// random.trust_cpu=on in the boot args (which handles the initial
	// seed): together they stop the guest blocking on /dev/random.
	// Best-effort — a Firecracker build or guest kernel without virtio-rng
	// support should not fail VM creation, since random.trust_cpu already
	// covers the boot-critical seeding. Must be configured before
	// InstanceStart, like the other devices.
	if err := apiClient.put(ctx, "/entropy", map[string]interface{}{}); err != nil {
		logger.WithFields("vm_id", id, "error", err.Error()).
			Warn("firecracker: virtio-rng entropy device not attached (continuing; random.trust_cpu covers initial seed)")
	}

	// Configure network if not denied
	var netCfg *NetworkConfig
	if instanceCfg.NetworkMode != sandbox.NetworkDeny {
		netCfg, err = SetupNetwork(id, instanceCfg.NetworkMode, instanceCfg.AllowedHosts, instanceCfg.DNSServers)
		if err != nil {
			killAndReap()
			return nil, fmt.Errorf("failed to setup network: %w", err)
		}
		if err := apiClient.put(ctx, "/network-interfaces/eth0", map[string]interface{}{
			"iface_id":      "eth0",
			"host_dev_name": netCfg.TapDevice,
			"guest_mac":     netCfg.GuestMAC,
		}); err != nil {
			CleanupNetwork(netCfg)
			killAndReap()
			return nil, fmt.Errorf("failed to attach network interface: %w", err)
		}
	}

	// Start the VM
	if err := apiClient.put(ctx, "/actions", map[string]interface{}{
		"action_type": "InstanceStart",
	}); err != nil {
		killAndReap()
		if netCfg != nil {
			CleanupNetwork(netCfg)
		}
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	// Create vsock client and wait for guest agent readiness
	vsockClient := NewVsockClient(guestCID, vsockPath)
	if err := vsockClient.WaitReady(ctx, 10*time.Second); err != nil {
		killAndReap()
		if netCfg != nil {
			CleanupNetwork(netCfg)
		}
		return nil, fmt.Errorf("guest agent not ready: %w", err)
	}

	// Configure guest networking + run an end-to-end self-test before
	// surfacing the VM as ready. Firecracker VMs don't have DHCP, so the
	// guest network must be configured explicitly after boot. The probe
	// catches the failure modes that look healthy from the host side
	// (TAP up, iptables installed) but die inside the guest — typically
	// the DNS resolver writing the wrong nameserver, or a guest agent
	// race where eth0 is up but hasn't seen the route yet.
	//
	// One retry: transient vsock-write failures, kernel asking for a
	// moment between iface up and route insertion, that sort of thing.
	// Two attempts is enough to absorb those without masking a real bug.
	//
	// vsock i/o timeouts are treated differently: when the transport
	// itself is broken (write/read timeouts that aren't about network
	// reachability), gating the VM on the probe just turns one bug
	// into many — every provision fails, the user can't access any
	// sandbox to investigate, and the real problem (guest agent dead,
	// host disk full, firecracker zombies) is hidden behind a cascade
	// of self-test failures. We log loudly and let the VM through; the
	// caller will fail fast on the first real Exec when they try to
	// use it, giving the operator a clearer signal.
	// Per-VM :8080 auth token. Generated once here and pushed to the guest
	// over vsock inside configureGuestNetwork (host-only channel), before the
	// host ever issues an authenticated HTTP request. NetworkDeny VMs skip the
	// whole block below and never use :8080, so they keep an empty token.
	agentToken, tokErr := generateAgentToken()
	if tokErr != nil {
		killAndReap()
		if netCfg != nil {
			CleanupNetwork(netCfg)
		}
		return nil, tokErr
	}

	if netCfg != nil {
		var lastErr error
		for attempt := 1; attempt <= 2; attempt++ {
			lastErr = configureGuestNetwork(ctx, id, vsockClient, netCfg, agentToken)
			if lastErr == nil {
				break
			}
			logger.WithFields("sandbox_id", id, "attempt", attempt, "error", lastErr.Error()).
				Warn("firecracker_vm: guest network self-test failed, retrying")
			// Small backoff before retry. Long enough for transient
			// kernel state to settle, short enough not to add real
			// latency to the cold-start path.
			time.Sleep(500 * time.Millisecond)
		}
		if lastErr != nil {
			if isVsockTransportError(lastErr) {
				// Don't tear down on a transport-level failure —
				// the guest agent or the firecracker process is
				// broken in a way the probe can't diagnose. Mark
				// the VM healthy at the network layer and let the
				// next real interaction surface the underlying
				// fault.
				logger.WithFields(
					"sandbox_id", id,
					"error", lastErr.Error(),
				).Error("firecracker_vm: vsock transport failed during self-test; surfacing VM anyway (operator must investigate guest agent / host resources)")
			} else {
				killAndReap()
				CleanupNetwork(netCfg)
				return nil, fmt.Errorf("guest network self-test failed after retry: %w", lastErr)
			}
		}
	}

	vm := &MicroVM{
		ID:         id,
		SocketPath: socketPath,
		Process:    launch.Process,
		ScopeName:  launch.ScopeName,
		VsockPath:  vsockPath,
		RootfsPath: rootfsPath,
		Status:     sandbox.StatusRunning,
		Config:     instanceCfg,
		CreatedAt:  time.Now(),
		VCPUs:      vcpus,
		MemoryMB:   memoryMB,
		Vsock:      vsockClient,
		Network:    netCfg,
		AgentToken: agentToken,
		workDir:    vmWorkDir,
	}
	// Bring the state machine up to the post-readiness state. The
	// path from Unknown→Healthy ran implicitly through the create
	// steps above — we model it as one Init+Transition pair here
	// rather than threading state across every error branch.
	vm.Lifecycle.Init(LifecycleProvisioning)
	vm.Lifecycle.MustTransition(id, LifecycleBooting)
	vm.Lifecycle.MustTransition(id, LifecycleHealthy)

	// Start the continuous health monitor. The readiness probe above
	// confirmed the agent is up *right now*; the monitor watches that
	// liveness for the lifetime of the VM and flips IsHealthy() on
	// edge transitions. Skip when there's no network (Deny mode) —
	// no TAP IP means no HTTP path to the agent.
	if netCfg != nil && netCfg.GuestIP != "" {
		vm.Health = NewHealthMonitor(id, netCfg.GuestIP)
		vm.Health.Start(context.Background())
		// Bridge health-monitor edges to the lifecycle state
		// machine: when the probe flips, the VM state moves
		// Healthy ⇄ Unhealthy. Lifecycle metrics + logs get the
		// transition; HealthMonitor's own edge log stays for
		// callers who only want the binary signal.
		vm.Health.SetTransitionHook(func(healthy bool) {
			if healthy {
				_, _ = vm.Lifecycle.Transition(id, LifecycleHealthy)
			} else {
				_, _ = vm.Lifecycle.Transition(id, LifecycleUnhealthy)
			}
		})
	}

	return vm, nil
}

// Destroy halts the VM and cleans up all resources.
func (vm *MicroVM) Destroy(ctx context.Context) error {
	vm.Status = sandbox.StatusStopped
	_, _ = vm.Lifecycle.Transition(vm.ID, LifecycleTerminating)

	// Stop the health monitor first so we don't log "agent unhealthy"
	// transitions during teardown (the probe will fail the moment the
	// VM halts; that's expected and uninteresting). Also detach its
	// transition hook so it can't race with the Terminating→Terminated
	// transition below.
	if vm.Health != nil {
		vm.Health.SetTransitionHook(nil)
		vm.Health.Stop()
		vm.Health = nil
	}

	// Try graceful halt first
	apiClient := newVMAPIClient(vm.SocketPath)
	haltCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = apiClient.put(haltCtx, "/actions", map[string]interface{}{
		"action_type": "InstanceHalt",
	})

	// Tear down the firecracker process. Dispatches to the
	// supervisor when this VM was launched through one (vm.ScopeName
	// non-empty), otherwise falls back to direct Process.Kill on
	// the child we own. See launcher.go for the matched pair.
	stopVMProcess(ctx, vm)

	// Clean up network
	if vm.Network != nil {
		CleanupNetwork(vm.Network)
	}

	// Clean up work directory
	if vm.workDir != "" {
		_ = os.RemoveAll(vm.workDir)
	}

	_, _ = vm.Lifecycle.Transition(vm.ID, LifecycleTerminated)
	return nil
}

// createRootfsOverlay creates a copy-on-write rootfs for the VM and
// grows it to diskMB when the caller asked for more than the base image
// provides. See growRootfs for why growing is cheap and why it never
// shrinks.
//
// Rootfs file selection:
//  1. Parse the image tag (e.g. `ghcr.io/.../sandbox:node` → "node").
//  2. Try `<rootfsDir>/<tag>.ext4`.
//  3. If missing, fall back to `<rootfsDir>/base.ext4` so the sandbox
//     still boots — the per-template rootfs build pipeline lags the
//     template catalog, and erroring would silently downgrade UX (the
//     user sees "create failed" with no path to recovery).
//
// Tag identity stays on the row regardless of which file boots, so a
// later `node.ext4` drop on the host is a zero-code change.
func createRootfsOverlay(rootfsDir, vmWorkDir, imageName string, diskMB int64) (string, error) {
	requestedTag := "base"
	if imageName != "" {
		if parts := strings.Split(imageName, ":"); len(parts) > 1 {
			requestedTag = parts[len(parts)-1]
		}
	}

	srcPath := filepath.Join(rootfsDir, requestedTag+".ext4")
	if _, err := os.Stat(srcPath); err != nil {
		// Per-template rootfs not on disk yet — fall back to base if
		// it exists. Log loudly so operators know the template isn't
		// fully baked.
		basePath := filepath.Join(rootfsDir, "base.ext4")
		if _, baseErr := os.Stat(basePath); baseErr != nil {
			return "", fmt.Errorf("rootfs not found: tried %s and %s", srcPath, basePath)
		}
		logger.WithFields(
			"requested_tag", requestedTag,
			"requested_path", srcPath,
			"fallback_path", basePath,
		).Warn("firecracker: per-template rootfs missing, booting from base.ext4")
		srcPath = basePath
	}

	// Per-VM rootfs copy. cloneRootfs runs the FICLONE →
	// copy_file_range → io.Copy fallback chain so the agent's RSS
	// stays flat regardless of rootfs size. The old os.ReadFile +
	// os.WriteFile path allocated the entire ext4 (default 2 GiB)
	// into the Go heap on every VM create — three concurrent creates
	// crossed the cgroup limit and OOMKilled the agent.
	dstPath := filepath.Join(vmWorkDir, "rootfs.ext4")
	method, err := cloneRootfs(srcPath, dstPath)
	if err != nil {
		return "", fmt.Errorf("failed to clone rootfs: %w", err)
	}
	logger.WithFields(
		"src", srcPath,
		"dst", dstPath,
		"method", method,
	).Debug("firecracker: rootfs cloned")

	// Grow the clone to the requested DiskMB. Must happen before
	// firecracker is told to attach the drive, which is why it lives
	// here rather than alongside the /machine-config call. CreateVM
	// takes no context; growRootfs bounds itself with resizeTimeout.
	applyDiskSize(context.Background(), dstPath, diskMB)

	return dstPath, nil
}

// waitForSocket waits for a Unix socket to become available.
func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("socket %s not available after %v", path, timeout)
}

// validateBinaryExists checks that the Firecracker binary is accessible.
func validateBinaryExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("binary not found: %s", path)
	}
	return nil
}

// validateKVMAccess checks that /dev/kvm is accessible.
func validateKVMAccess() error {
	if _, err := os.Stat("/dev/kvm"); err != nil {
		return fmt.Errorf("/dev/kvm not accessible: %w", err)
	}
	return nil
}

// cidCounter is a simple monotonic counter for guest CIDs.
// In production, use a proper allocator with persistence.
var cidCounter uint32 = 3 // Start at 3 (0, 1, 2 are reserved)

// generateAgentToken returns a random per-VM bearer token for the guest's :8080
// control plane (32 bytes of crypto/rand, hex-encoded → 64 chars). Distinct per
// VM: see the SECURITY INVARIANT in cmd/sandbox-agent/auth.go — never share one
// token across VMs.
func generateAgentToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate agent token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func allocateCID() uint32 {
	cidCounter++
	return cidCounter
}

// configureGuestNetwork writes the guest's /etc/resolv.conf, brings up
// eth0 + default route via the early-boot vsock channel, then waits for
// the in-guest agent's HTTP control plane to respond over the TAP. The
// HTTP probe is the canonical readiness signal — vsock is reserved for
// the network-bootstrap steps that have to happen before HTTP can work
// at all.
//
// Why HTTP instead of vsock-Exec for the probe:
//
// The previous design ran getent+nc inside the guest via vsock-Exec.
// That conflated three failure modes onto one channel: vsock wedged,
// agent dead, or actual network broken. When vsock wedged (a known
// failure class in production — see the 2026-05-20 incident), every
// new sandbox failed provisioning even though the agent was fine.
// HTTP-over-TAP is what e2b/Modal-style providers use precisely
// because it makes "agent alive" observable independent of the
// command channel.
//
// The egress validation (can the guest reach the public internet) is
// no longer part of provisioning. It's the user's contract: if they
// can't reach a thing, their app surfaces the error directly. Gating
// every provision on a public-internet check turned the platform into
// "the internet is up" rather than "the agent is up."
func configureGuestNetwork(ctx context.Context, sandboxID string, vsockClient *VsockClient, netCfg *NetworkConfig, agentToken string) error {
	// /etc/resolv.conf — vsock-only because eth0 isn't routable yet.
	var resolv strings.Builder
	for _, ns := range netCfg.GuestResolvers() {
		resolv.WriteString("nameserver " + ns + "\n")
	}
	if err := vsockClient.WriteFile(ctx, "/etc/resolv.conf", []byte(resolv.String())); err != nil {
		return fmt.Errorf("write /etc/resolv.conf: %w", err)
	}

	// Push the per-VM :8080 bearer token over vsock (host-only) BEFORE the guest
	// serves any authenticated HTTP. Until this lands the guest middleware
	// default-denies every non-/health request, so a co-resident guest can't
	// call /toolbox/exec during the boot window. Idempotent across the retry
	// loop in CreateVM (a repeat push just overwrites the same value).
	if agentToken != "" {
		if err := vsockClient.SetAgentToken(ctx, agentToken); err != nil {
			return fmt.Errorf("push agent token: %w", err)
		}
	}

	// Pin the guest's eth0 MTU to the same value we put on the TAP so the
	// kernel inside the VM doesn't emit 1500-byte packets into an overlay
	// path that can only carry ~1450. Without this, TCP handshakes succeed
	// (small packets) but data transfer stalls (TLS cert response gets
	// black-holed). Curl reports this as "SSL connection timeout".
	mtu := netCfg.MTU
	if mtu <= 0 {
		mtu = 1400
	}
	netCmd := fmt.Sprintf(
		"ip addr add %s/30 dev eth0 && ip link set eth0 mtu %d && ip link set eth0 up && ip route add default via %s",
		netCfg.GuestIP, mtu, netCfg.HostIP,
	)
	if res, err := vsockClient.Exec(ctx, ExecCommand{
		ID:        "net-setup",
		Command:   []string{"sh", "-c", netCmd},
		TimeoutMS: 5000,
	}); err != nil {
		return fmt.Errorf("configure eth0: %w", err)
	} else if res != nil && res.ExitCode != 0 {
		// Tolerate "File exists" / "RTNETLINK answers: File exists" on
		// retry — the iface is already configured from the previous
		// attempt and the second ip-addr-add reports nonzero. As long
		// as the agent probe below passes, we're fine.
		if !strings.Contains(res.Stderr, "File exists") {
			return fmt.Errorf("configure eth0 nonzero exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	}

	// Agent readiness via HTTP — the canonical "VM is healthy" signal.
	// Replaces the old vsock-Exec DNS+TCP probes. If this fails it
	// means: the agent didn't start, eth0 isn't routable from the host,
	// or the host's per-VM forward rules are misconfigured. All three
	// are real failures we want to surface.
	if err := WaitForAgentReady(ctx, netCfg.GuestIP); err != nil {
		logger.WithFields(
			"sandbox_id", sandboxID,
			"guest_ip", netCfg.GuestIP,
			"host_ip", netCfg.HostIP,
			"error", err.Error(),
		).Warn("firecracker_vm: agent HTTP /health probe failed")
		return fmt.Errorf("agent readiness probe: %w", err)
	}
	return nil
}

// isVsockTransportError returns true when the error indicates the
// host↔guest IPC channel itself is broken (firecracker process dead,
// guest agent hung, kernel deadlocked) rather than a semantic failure
// of the request running inside the guest. We treat these differently
// because gating provisioning on a broken transport just multiplies
// the visible failures: every new VM fails, the operator can't get
// into any sandbox to triage, and the underlying issue (disk full,
// FD exhaustion, guest panic) stays hidden.
//
// Pattern-matches the vsock unix-socket timeout signatures we observe
// in production. Update this list as new symptoms appear.
func isVsockTransportError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, fragment := range []string{
		"vsock.sock: i/o timeout",
		"failed to send request",
		"failed to read response",
		"connection refused",
		"broken pipe",
	} {
		if strings.Contains(s, fragment) {
			return true
		}
	}
	return false
}
