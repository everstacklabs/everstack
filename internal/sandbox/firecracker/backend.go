// Package firecracker provides a Firecracker microVM-based sandbox backend.
// Requires Linux with KVM support (/dev/kvm) and the Firecracker binary.
package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
)

// FirecrackerBackend implements sandbox.Backend using Firecracker microVMs.
// Each sandbox gets a dedicated VM with hardware-level KVM isolation.
type FirecrackerBackend struct {
	config  FirecrackerConfig
	pool    *VMPool
	vms     map[string]*MicroVM // sandbox ID → running VM
	mu      sync.RWMutex
	logsMu  sync.RWMutex
	logs    map[string]*logbuffer.Buffer // sandbox ID → log buffer
	statsMu sync.Mutex
	stats   map[string]cpuSample // sandbox ID → previous CPU sample

	portMu       sync.Mutex
	portMappings map[string]map[int]*firecrackerPortMapping // sandbox ID -> guest port -> mapping
}

type cpuSample struct {
	Total uint64
	Idle  uint64
}

type firecrackerPortMapping struct {
	HostPort  int
	GuestPort int
	GuestIP   string
	Protocol  string
}

type guestStats struct {
	CPUTotal       uint64 `json:"cpu_total"`
	CPUIdle        uint64 `json:"cpu_idle"`
	MemTotalKB     uint64 `json:"mem_total_kb"`
	MemAvailableKB uint64 `json:"mem_available_kb"`
	PIDs           int    `json:"pids"`
	NetRX          uint64 `json:"net_rx"`
	NetTX          uint64 `json:"net_tx"`
	BlockRead      uint64 `json:"blk_read"`
	BlockWrite     uint64 `json:"blk_write"`
}

// FirecrackerConfig holds configuration for the Firecracker backend.
type FirecrackerConfig struct {
	BinaryPath string       // Path to firecracker binary (default: /usr/bin/firecracker)
	KernelPath string       // Path to guest kernel image (default: /var/lib/everstack/vmlinux.bin)
	RootfsDir  string       // Directory containing per-runtime rootfs images
	WorkDir    string       // Per-VM working directory base (default: /var/lib/everstack/vms/)
	Pool       VMPoolConfig // Warm VM pool settings
	VM         VMDefaults   // Default VM resource limits

	// MinFreeDiskMB is the headroom the VM store must keep before a new
	// microVM is admitted. 0 disables the check.
	// See DefaultMinFreeDiskMB for why over-committed thin disks need a
	// floor rather than promise-based accounting.
	MinFreeDiskMB int64
}

// VMDefaults holds default resource limits for VMs.
type VMDefaults struct {
	MemoryMB  int64 `json:"memory_mb"`
	VCPUs     int   `json:"vcpus"`
	TimeoutMs int   `json:"timeout_ms"`
}

// DefaultFirecrackerConfig returns sensible defaults.
func DefaultFirecrackerConfig() FirecrackerConfig {
	return FirecrackerConfig{
		BinaryPath: "/usr/bin/firecracker",
		KernelPath: "/var/lib/everstack/vmlinux.bin",
		RootfsDir:  "/var/lib/everstack/rootfs/",
		WorkDir:    "/var/lib/everstack/vms/",
		Pool:       DefaultVMPoolConfig(),
		VM: VMDefaults{
			MemoryMB:  512,
			VCPUs:     1,
			TimeoutMs: 30000,
		},
		MinFreeDiskMB: DefaultMinFreeDiskMB,
	}
}

// New creates a new Firecracker sandbox backend.
func New(cfg FirecrackerConfig) (*FirecrackerBackend, error) {
	// Validate pool sizing invariants before anything else — a
	// MaxSize > MaxTotal misconfig used to silently spin the
	// replenish loop forever after the hard cap was hit.
	if err := cfg.Pool.Validate(); err != nil {
		return nil, fmt.Errorf("pool config: %w", err)
	}

	// Validate Firecracker binary exists
	if err := validateBinaryExists(cfg.BinaryPath); err != nil {
		return nil, fmt.Errorf("firecracker binary not found at %s: %w", cfg.BinaryPath, err)
	}

	// Validate KVM support
	if err := validateKVMAccess(); err != nil {
		return nil, fmt.Errorf("KVM not available: %w", err)
	}

	pool := NewVMPool(cfg)

	b := &FirecrackerBackend{
		config: cfg,
		pool:   pool,
		vms:    make(map[string]*MicroVM),
		logs:   make(map[string]*logbuffer.Buffer),
		stats:  make(map[string]cpuSample),

		portMappings: make(map[string]map[int]*firecrackerPortMapping),
	}

	logger.WithFields(
		"binary", cfg.BinaryPath,
		"kernel", cfg.KernelPath,
		"rootfs_dir", cfg.RootfsDir,
	).Info("firecracker_sandbox: backend initialized")

	return b, nil
}

func (b *FirecrackerBackend) Name() string { return "firecracker" }

func (b *FirecrackerBackend) RunnerCapabilities() sandbox.RunnerCapabilities {
	caps := sandbox.RunnerCapabilities{
		Target:    b.Name(),
		Placement: sandbox.RunnerPlacementLocal,
		Health:    sandbox.RunnerHealthInGuestAgent,
		Features: sandbox.RunnerFeatures{
			WorkspaceSnapshot: true,
			VMSnapshot:        true,
			VMRestore:         true,
			PortExposure:      true,
			PersistentShell:   true,
			SSH:               true,
			Volumes:           true,
			ComputerUse:       true,
		},
	}
	if b != nil {
		caps.Capacity.WarmPoolSize = b.config.Pool.MaxSize
		caps.Capacity.WarmPoolLimit = b.config.Pool.MaxTotal
	}
	return caps
}

// TrackedPIDs returns the PIDs of every VM currently tracked by the
// backend (active sandboxes + warm pool). The orphan reaper uses
// this set to decide which child firecracker processes to kill.
func (b *FirecrackerBackend) TrackedPIDs() []int {
	b.mu.RLock()
	pids := make([]int, 0, len(b.vms))
	for _, vm := range b.vms {
		if vm != nil && vm.Process != nil {
			pids = append(pids, vm.Process.Pid)
		}
	}
	b.mu.RUnlock()
	if b.pool != nil {
		pids = append(pids, b.pool.TrackedPIDs()...)
	}
	return pids
}

// TrackedIDs returns the set of sandbox IDs currently held in the
// backend's live map (active VMs + warm pool slots). The workdir
// reconciler uses this to distinguish "this directory belongs to a
// live VM" from "this directory is an orphan." Returned as a set so
// callers can do constant-time membership checks during a sweep.
func (b *FirecrackerBackend) TrackedIDs() map[string]struct{} {
	b.mu.RLock()
	ids := make(map[string]struct{}, len(b.vms))
	for id := range b.vms {
		ids[id] = struct{}{}
	}
	b.mu.RUnlock()
	if b.pool != nil {
		for _, id := range b.pool.TrackedIDs() {
			ids[id] = struct{}{}
		}
	}
	return ids
}

// WorkDir returns the root directory under which every per-VM workdir
// lives. Exposed for the workdir reconciler, which has no other way to
// know where on disk to look.
func (b *FirecrackerBackend) WorkDir() string { return b.config.WorkDir }

// PoolMaxTotal returns the configured hard cap on this node's total VM
// count (warm + active). Used by the pressure collector to compute
// remaining capacity for the placement signal. Zero when the pool is
// not configured.
func (b *FirecrackerBackend) PoolMaxTotal() int {
	if b.pool == nil {
		return 0
	}
	return b.pool.MaxTotal()
}

// getOrCreateLogs returns the log buffer for a sandbox, creating one if needed.
func (b *FirecrackerBackend) getOrCreateLogs(id string) *logbuffer.Buffer {
	b.logsMu.RLock()
	sl, ok := b.logs[id]
	b.logsMu.RUnlock()
	if ok {
		return sl
	}

	b.logsMu.Lock()
	defer b.logsMu.Unlock()
	if sl, ok := b.logs[id]; ok {
		return sl
	}
	sl = logbuffer.NewBuffer()
	b.logs[id] = sl
	return sl
}

// Create provisions a new Firecracker microVM for the sandbox.
func (b *FirecrackerBackend) Create(ctx context.Context, id string, config sandbox.InstanceConfig) (*sandbox.Instance, error) {
	b.mu.Lock()

	// Try to acquire a warm VM from the pool
	vm, err := b.pool.Acquire(ctx, id, config)
	if err != nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("failed to create VM: %w", err)
	}

	b.vms[id] = vm

	expiresAt := time.Now().Add(time.Duration(config.TimeoutSeconds) * time.Second)
	vm.ExpiresAt = expiresAt
	vm.Config = config

	logger.WithFields("sandbox_id", id, "vcpus", vm.VCPUs, "memory_mb", vm.MemoryMB).
		Info("firecracker_sandbox: VM created")

	sl := b.getOrCreateLogs(id)
	sl.Append(logbuffer.Entry{
		Timestamp: time.Now(),
		Stream:    "system",
		Line:      fmt.Sprintf("sandbox created (backend=firecracker, vcpus=%d, memory_mb=%d)", vm.VCPUs, vm.MemoryMB),
	})

	// Persist the VM's metadata so a future agent restart can re-attach
	// to this still-running firecracker process. Without this, an agent
	// restart loses every active VM — the firecracker children survive
	// (thanks to hostPID=true on the DaemonSet) but the new agent has
	// no record of them and shell/exec/logs all fail with "VM not found."
	// Best-effort: log the error but don't fail Create — the VM is alive
	// and the user can still use it for this agent's lifetime.
	if err := writeVMState(vm); err != nil {
		logger.WithFields("sandbox_id", id, "error", err.Error()).
			Warn("firecracker_sandbox: failed to persist VM state; agent restart will lose this VM")
	}

	// Release the backend lock BEFORE any vsock I/O so a slow/hung guest can't
	// stall concurrent sandbox creates.
	b.mu.Unlock()

	// Deliver storage mounts to the guest toolbox. Firecracker has no
	// env-injection path (unlike docker/k8s, which read SANDBOX_MOUNTS_JSON
	// from the container env), so the mounts — including the per-mount,
	// tenant-scoped R2 credentials set by the everstack-volume rewrite — are
	// parsed from the same env var and pushed after acquire. Covers both cold
	// and warm-pool VMs since every sandbox passes through Create.
	// Best-effort: a failure is logged but doesn't fail creation.
	if raw := config.EnvVars["SANDBOX_MOUNTS_JSON"]; raw != "" && vm.Vsock != nil {
		var mounts []sandbox.StorageMountConfig
		if err := json.Unmarshal([]byte(raw), &mounts); err != nil {
			logger.WithFields("sandbox_id", id, "error", err.Error()).
				Warn("firecracker_sandbox: invalid SANDBOX_MOUNTS_JSON")
		} else if len(mounts) > 0 {
			if merr := vm.ToolboxConfigureMounts(ctx, mounts); merr != nil {
				logger.WithFields("sandbox_id", id, "error", merr.Error()).
					Warn("firecracker_sandbox: failed to configure storage mounts")
			}
		}
	}

	return &sandbox.Instance{
		ID:          id,
		ContainerID: fmt.Sprintf("fc-%s", vm.ID),
		Status:      sandbox.StatusRunning,
		Config:      config,
		CreatedAt:   vm.CreatedAt,
		ExpiresAt:   expiresAt,
		Backend:     "firecracker",
	}, nil
}

// Exec runs a command inside the microVM via the guest toolbox.
func (b *FirecrackerBackend) Exec(ctx context.Context, id string, req sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = time.Duration(b.config.VM.TimeoutMs) * time.Millisecond
	}

	cmd := req.Command
	if len(cmd) == 0 {
		return nil, fmt.Errorf("command is required")
	}

	sl := b.getOrCreateLogs(id)
	now := time.Now()
	if !req.SilentLog {
		sl.Append(logbuffer.Entry{
			Timestamp: now,
			Stream:    "system",
			Line:      fmt.Sprintf("$ %s", strings.Join(cmd, " ")),
		})
	}

	result, err := vm.ToolboxExec(ctx, ExecCommand{
		ID:        fmt.Sprintf("exec_%d", time.Now().UnixNano()),
		Command:   cmd,
		WorkDir:   req.WorkDir,
		Env:       req.Env,
		TimeoutMS: int(timeout.Milliseconds()),
	})
	if err != nil {
		if !req.SilentLog {
			sl.Append(logbuffer.Entry{
				Timestamp: time.Now(),
				Stream:    "stderr",
				Line:      "exec failed: " + err.Error(),
			})
		}
		return nil, err
	}

	if out := strings.TrimRight(result.Stdout, "\n"); out != "" && !req.SilentLog {
		for _, line := range strings.Split(out, "\n") {
			sl.Append(logbuffer.Entry{
				Timestamp: time.Now(),
				Stream:    "stdout",
				Line:      line,
			})
		}
	}
	if out := strings.TrimRight(result.Stderr, "\n"); out != "" && !req.SilentLog {
		for _, line := range strings.Split(out, "\n") {
			sl.Append(logbuffer.Entry{
				Timestamp: time.Now(),
				Stream:    "stderr",
				Line:      line,
			})
		}
	}

	result.Stdout = truncateOutput(result.Stdout)
	result.Stderr = truncateOutput(result.Stderr)
	return result, nil
}

// WriteFile writes content to a path inside the VM via the guest toolbox.
func (b *FirecrackerBackend) WriteFile(ctx context.Context, id string, path string, content []byte) error {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()

	if !ok {
		return fmt.Errorf("VM not found for sandbox %s", id)
	}

	err := vm.ToolboxWriteFile(ctx, path, content)
	if err != nil {
		b.getOrCreateLogs(id).Append(logbuffer.Entry{
			Timestamp: time.Now(),
			Stream:    "stderr",
			Line:      fmt.Sprintf("write_file failed (%s): %v", path, err),
		})
	}
	return err
}

// ReadFile reads content from a path inside the VM via the guest toolbox.
func (b *FirecrackerBackend) ReadFile(ctx context.Context, id string, path string) ([]byte, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}

	content, err := vm.ToolboxReadFile(ctx, path)
	if err != nil {
		b.getOrCreateLogs(id).Append(logbuffer.Entry{
			Timestamp: time.Now(),
			Stream:    "stderr",
			Line:      fmt.Sprintf("read_file failed (%s): %v", path, err),
		})
		return nil, err
	}
	return content, nil
}

// ListFiles lists directory contents inside the VM via the guest toolbox.
func (b *FirecrackerBackend) ListFiles(ctx context.Context, id string, path string) ([]sandbox.FileInfo, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}

	files, err := vm.ToolboxListFiles(ctx, path)
	if err != nil {
		b.getOrCreateLogs(id).Append(logbuffer.Entry{
			Timestamp: time.Now(),
			Stream:    "stderr",
			Line:      fmt.Sprintf("list_files failed (%s): %v", path, err),
		})
		return nil, err
	}
	return files, nil
}

// Destroy halts the microVM and cleans up all resources.
//
// Routes through pool.Release rather than calling vm.Destroy directly
// so the pool's `active` counter decrements with every successful
// destroy. Without this, every create/destroy cycle leaked one slot
// and the agent eventually reported "VM pool exhausted (N total VMs)"
// on an otherwise-idle node — visible on dev clusters after just a
// handful of sandbox cycles.
func (b *FirecrackerBackend) Destroy(ctx context.Context, id string) error {
	b.mu.Lock()
	vm, ok := b.vms[id]
	if ok {
		delete(b.vms, id)
	}
	b.mu.Unlock()

	if !ok {
		return nil
	}

	sl := b.getOrCreateLogs(id)
	sl.Append(logbuffer.Entry{
		Timestamp: time.Now(),
		Stream:    "system",
		Line:      "sandbox destroy requested",
	})

	b.unexposeAllPorts(id)

	// pool.Release decrements active AND calls vm.Destroy internally;
	// destroy errors are logged inside Release. We swallow them here
	// for the same reason — the slot is gone either way and a stuck
	// VM is already in a bad state.
	b.pool.Release(ctx, vm)

	b.logsMu.Lock()
	if sl, ok := b.logs[id]; ok {
		sl.Close()
		delete(b.logs, id)
	}
	b.logsMu.Unlock()

	b.statsMu.Lock()
	delete(b.stats, id)
	b.statsMu.Unlock()

	logger.WithFields("sandbox_id", id).Debug("firecracker_sandbox: VM destroyed")
	return nil
}

// Status returns the current state of a sandbox VM.
func (b *FirecrackerBackend) Status(ctx context.Context, id string) (*sandbox.Instance, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}

	return &sandbox.Instance{
		ID:           id,
		ContainerID:  fmt.Sprintf("fc-%s", vm.ID),
		Status:       vm.Status,
		Backend:      "firecracker",
		CreatedAt:    vm.CreatedAt,
		ExpiresAt:    vm.ExpiresAt,
		AgentHealthy: vmAgentHealthy(vm),
	}, nil
}

// vmAgentHealthy returns the current in-guest /health probe status
// for a VM. true when:
//   - there's no HealthMonitor (NetworkDeny VMs — no probe to run, so
//     we report healthy rather than artificially false-alarm); or
//   - the monitor's atomic flag says the last tick saw 204 No Content.
//
// false when the monitor's last tick failed. Lock-free read; safe to
// call from any goroutine without coordinating with the monitor's
// own locking.
func vmAgentHealthy(vm *MicroVM) bool {
	if vm == nil || vm.Health == nil {
		return true
	}
	return vm.Health.IsHealthy()
}

func (b *FirecrackerBackend) DescribePending(ctx context.Context, id string) string {
	return ""
}

// Healthy checks if the Firecracker binary is accessible and KVM is available.
func (b *FirecrackerBackend) Healthy(ctx context.Context) error {
	if err := validateBinaryExists(b.config.BinaryPath); err != nil {
		return err
	}
	return validateKVMAccess()
}

// Logs returns a stream of sandbox logs captured from exec/shell output.
func (b *FirecrackerBackend) Logs(ctx context.Context, id string, opts sandbox.LogsOptions) (io.ReadCloser, error) {
	b.mu.RLock()
	_, ok := b.vms[id]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}

	sl := b.getOrCreateLogs(id)
	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		entries := sl.Snapshot(opts.Tail)
		for _, e := range entries {
			if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
				continue
			}
			line := logbuffer.FormatEntry(e, opts.Timestamps)
			if _, err := pw.Write([]byte(line + "\n")); err != nil {
				return
			}
		}

		if !opts.Follow {
			return
		}

		ch := sl.Subscribe()
		defer sl.Unsubscribe(ch)

		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				line := logbuffer.FormatEntry(e, opts.Timestamps)
				if _, err := pw.Write([]byte(line + "\n")); err != nil {
					return
				}
			}
		}
	}()

	return pr, nil
}

// Stats returns a one-shot resource usage snapshot from inside the guest.
func (b *FirecrackerBackend) Stats(ctx context.Context, id string) (*sandbox.ContainerStats, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}

	statsCmd := []string{"sh", "-c", firecrackerStatsScript}
	result, err := vm.ToolboxExec(ctx, ExecCommand{
		ID:        fmt.Sprintf("stats_%d", time.Now().UnixNano()),
		Command:   statsCmd,
		WorkDir:   "/workspace",
		TimeoutMS: 5000,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to collect guest stats: %w", err)
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("failed to collect guest stats: %s", msg)
	}

	var gs guestStats
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &gs); err != nil {
		return nil, fmt.Errorf("failed to parse guest stats: %w", err)
	}

	memLimit := int64(gs.MemTotalKB) * 1024
	memUsage := int64(gs.MemTotalKB-gs.MemAvailableKB) * 1024
	if memUsage < 0 {
		memUsage = 0
	}

	memPercent := 0.0
	if memLimit > 0 {
		memPercent = float64(memUsage) / float64(memLimit) * 100
	}

	cpuPercent := 0.0
	b.statsMu.Lock()
	if prev, ok := b.stats[id]; ok {
		totalDelta := gs.CPUTotal - prev.Total
		idleDelta := gs.CPUIdle - prev.Idle
		if totalDelta > 0 && idleDelta <= totalDelta {
			cpuPercent = (1.0 - (float64(idleDelta) / float64(totalDelta))) * 100
		}
	}
	b.stats[id] = cpuSample{
		Total: gs.CPUTotal,
		Idle:  gs.CPUIdle,
	}
	b.statsMu.Unlock()

	return &sandbox.ContainerStats{
		CPUPercent:     cpuPercent,
		MemoryUsage:    memUsage,
		MemoryLimit:    memLimit,
		MemoryPercent:  memPercent,
		NetworkRxBytes: int64(gs.NetRX),
		NetworkTxBytes: int64(gs.NetTX),
		BlockRead:      int64(gs.BlockRead),
		BlockWrite:     int64(gs.BlockWrite),
		PIDs:           gs.PIDs,
		Timestamp:      time.Now(),
	}, nil
}

// UpdateInstanceWorkDir updates the WorkDir on the live VM struct so
// subsequent Shell() and Exec() calls land in the new directory.
//
// vm.Config is captured at CreateVM time — updating
// sandbox_instances.config in the DB alone isn't enough to affect a
// running VM's behavior. This method bridges that gap. The SandboxManager
// calls it from UpdateInstanceWorkDir when the composer's "Working
// Directory" field is edited on an agent that has a running sandbox.
//
// Returns silently when the VM isn't tracked on this backend — that's
// the expected case on fcagents that don't own the sandbox, and on a
// gateway with no fcagent route resolved yet.
func (b *FirecrackerBackend) UpdateInstanceWorkDir(id, newWorkDir string) {
	if newWorkDir == "" {
		return
	}
	b.mu.Lock()
	vm, ok := b.vms[id]
	if !ok {
		b.mu.Unlock()
		return
	}
	vm.Config.WorkDir = newWorkDir
	b.mu.Unlock()
}

// Shell opens an interactive shell session inside the microVM. The
// guest sandbox-agent attaches a `tmux` client to a persistent
// session and streams bytes over vsock using the shellframe binary
// protocol. The returned ShellSession exposes the bytes pipe and a
// working Resize callback that forwards TIOCSWINSZ to the guest.
//
// cmd is currently ignored — the guest always runs `bash -l` inside
// the tmux session as the sandbox user. Per-command exec lives on
// backend.Exec(); this path is exclusively for interactive sessions.
func (b *FirecrackerBackend) Shell(ctx context.Context, id string, cmd []string) (*sandbox.ShellSession, error) {
	return b.ShellWithSession(ctx, id, "", cmd)
}

// ShellWithSession honors the PersistentShellBackend contract. When
// shellSessionID is non-empty the guest reattaches to that existing
// tmux session; when empty it creates a fresh one and the assigned
// ID is returned on ShellSession.ShellSessionID so callers can
// remember it for reconnect.
func (b *FirecrackerBackend) ShellWithSession(ctx context.Context, id, shellSessionID string, cmd []string) (*sandbox.ShellSession, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("VM not found for sandbox %s", id)
	}

	// Initial working directory comes from the sandbox config — agents
	// and templates set this per their own convention (e.g. /repo for a
	// git-imported sandbox, /workspace for some templates). Falling back
	// to /workspace only when nothing is configured matches the behavior
	// the user lands in for a manual sandbox. The work_dir is only
	// honored when the guest creates a new tmux session; reattach
	// ignores it because the existing session already has a cwd.
	workDir := vm.Config.WorkDir
	if workDir == "" {
		workDir = "/workspace"
	}
	shellParams := ShellOpenParams{
		SessionID: shellSessionID,
		User:      "sandbox",
		WorkDir:   workDir,
		Rows:      24,
		Cols:      80,
	}

	// Prefer the HTTP/WebSocket toolbox path when the VM has a routable guest IP.
	// Vsock remains the compatibility fallback for old guests, early boot, and
	// deny-network sandboxes. Guest-level errors (for example session_gone) are
	// surfaced directly and do not fall back.
	var (
		conn   net.Conn
		result ShellOpenResult
		err    error
	)
	transport := selectShellTransport(vm)
	switch transport {
	case shellTransportWS:
		conn, result, err = OpenShellViaWS(ctx, vm.Network.GuestIP, vm.AgentToken, shellParams)
		if errors.Is(err, ErrShellWSUnavailable) {
			logger.WithFields("sandbox_id", id, "error", err.Error()).
				Debug("firecracker: shell WS unavailable, falling back to vsock")
			transport = shellTransportVsock
			conn, result, err = vm.Vsock.OpenShell(ctx, shellParams)
		}
	default:
		conn, result, err = vm.Vsock.OpenShell(ctx, shellParams)
	}
	if err != nil {
		return nil, fmt.Errorf("open guest shell (%s): %w", transport, err)
	}
	logger.WithFields("sandbox_id", id, "transport", transport, "session_id", result.SessionID, "reattached", result.Reattached).
		Debug("firecracker: shell opened")

	sc := newFirecrackerShellConn(ctx, b, id, conn)
	return &sandbox.ShellSession{
		Conn:           sc,
		Resize:         sc.Resize,
		ShellSessionID: result.SessionID,
		Reattached:     result.Reattached,
		Transport:      transport,
	}, nil
}

// ListShellSessions returns the persistent tmux sessions alive in the
// guest VM. Returns an empty slice for unknown sandbox IDs rather
// than an error — the admin UI uses this to render "no sessions yet"
// without having to special-case missing sandboxes.
func (b *FirecrackerBackend) ListShellSessions(ctx context.Context, id string) ([]sandbox.ShellSessionInfo, error) {
	sessions, _, err := b.ListShellSessionsWithClock(ctx, id)
	return sessions, err
}

// ListShellSessionsWithClock is the firecracker-specific extension
// that also returns the guest's wall-clock time at response-assembly.
// The agent gRPC server uses this to populate the proto's top-level
// NowUnix so the gateway can carry the raw clock through to UIs that
// want to render "last active 3m ago" without re-deriving it from
// the precomputed IdleSeconds (which loses the absolute timestamp).
func (b *FirecrackerBackend) ListShellSessionsWithClock(ctx context.Context, id string) ([]sandbox.ShellSessionInfo, int64, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()
	if !ok {
		return nil, 0, fmt.Errorf("VM not found for sandbox %s", id)
	}
	resp, err := vm.ToolboxListSessionsRaw(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]sandbox.ShellSessionInfo, 0, len(resp.Sessions))
	for _, s := range resp.Sessions {
		attached := 0
		if s.Attached {
			// The guest agent reports a bool (any clients attached vs.
			// none). Translate to a count of "at least one" — the
			// admin UI just needs to know if anyone is connected, not
			// the exact number.
			attached = 1
		}
		idle := int64(-1)
		if resp.NowUnix > 0 && s.LastActivityUnix > 0 {
			idle = resp.NowUnix - s.LastActivityUnix
			if idle < 0 {
				// Clock weirdness (last_activity in the future).
				// Treat as "just active" rather than as a huge
				// negative number that would confuse the reaper.
				idle = 0
			}
		}
		out = append(out, sandbox.ShellSessionInfo{
			ID:               s.ID,
			AttachedClients:  attached,
			CreatedUnix:      s.CreatedUnix,
			LastActivityUnix: s.LastActivityUnix,
			IdleSeconds:      idle,
		})
	}
	return out, resp.NowUnix, nil
}

// KillShellSession terminates a persistent shell session inside a
// running VM. Idempotent: killing a missing session is not an error.
func (b *FirecrackerBackend) KillShellSession(ctx context.Context, id, shellSessionID string) error {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()
	if !ok {
		return fmt.Errorf("VM not found for sandbox %s", id)
	}
	return vm.ToolboxKillSession(ctx, shellSessionID)
}

// List returns running Firecracker VMs known to this process.
func (b *FirecrackerBackend) List(_ context.Context) ([]*sandbox.Instance, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.vms) == 0 {
		return []*sandbox.Instance{}, nil
	}

	instances := make([]*sandbox.Instance, 0, len(b.vms))
	for id, vm := range b.vms {
		instances = append(instances, &sandbox.Instance{
			ID:           id,
			ContainerID:  fmt.Sprintf("fc-%s", vm.ID),
			Status:       vm.Status,
			Config:       vm.Config,
			CreatedAt:    vm.CreatedAt,
			ExpiresAt:    vm.ExpiresAt,
			Backend:      "firecracker",
			AgentHealthy: vmAgentHealthy(vm),
		})
	}
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].ID < instances[j].ID
	})
	return instances, nil
}

// ExposePort configures host-level DNAT rules to forward host traffic to the VM.
func (b *FirecrackerBackend) ExposePort(ctx context.Context, id string, port int, protocol string) (int, error) {
	b.mu.RLock()
	vm, ok := b.vms[id]
	b.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("VM not found for sandbox %s", id)
	}
	if vm.Network == nil {
		return 0, fmt.Errorf("sandbox %s has no network; enable network_mode allow/whitelist", id)
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid guest port %d", port)
	}

	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" {
		return 0, fmt.Errorf("firecracker backend only supports tcp port exposure, got %q", proto)
	}

	b.portMu.Lock()
	if existing, ok := b.portMappings[id][port]; ok {
		hostPort := existing.HostPort
		b.portMu.Unlock()
		return hostPort, nil
	}
	b.portMu.Unlock()

	hostPort, err := allocateHostPort()
	if err != nil {
		return 0, fmt.Errorf("failed to allocate host port: %w", err)
	}

	mapping := &firecrackerPortMapping{
		HostPort:  hostPort,
		GuestPort: port,
		GuestIP:   vm.Network.GuestIP,
		Protocol:  proto,
	}
	if err := applyPortMapping(mapping); err != nil {
		return 0, err
	}

	b.portMu.Lock()
	if b.portMappings[id] == nil {
		b.portMappings[id] = make(map[int]*firecrackerPortMapping)
	}
	b.portMappings[id][port] = mapping
	b.portMu.Unlock()

	logger.WithFields("sandbox_id", id, "guest_port", port, "host_port", hostPort).
		Info("firecracker_sandbox: port exposed")
	return hostPort, nil
}

// UnexposePort removes host-level DNAT rules for a previously exposed VM port.
func (b *FirecrackerBackend) UnexposePort(_ context.Context, id string, port int) error {
	b.portMu.Lock()
	mappings := b.portMappings[id]
	if mappings == nil {
		b.portMu.Unlock()
		return nil
	}
	mapping, ok := mappings[port]
	if !ok {
		b.portMu.Unlock()
		return nil
	}
	delete(mappings, port)
	if len(mappings) == 0 {
		delete(b.portMappings, id)
	}
	b.portMu.Unlock()

	if err := removePortMapping(mapping); err != nil {
		return err
	}
	logger.WithFields("sandbox_id", id, "guest_port", port, "host_port", mapping.HostPort).
		Debug("firecracker_sandbox: port unexposed")
	return nil
}

func truncateOutput(s string) string {
	const maxLen = 1 << 20 // 1MB
	if len(s) > maxLen {
		return s[:maxLen] + "\n... (output truncated at 1MB)"
	}
	return s
}

const firecrackerStatsScript = `cpu_line=$(grep '^cpu ' /proc/stat | head -n1);
set -- $cpu_line;
cpu_total=$(( ${2:-0}+${3:-0}+${4:-0}+${5:-0}+${6:-0}+${7:-0}+${8:-0}+${9:-0}+${10:-0}+${11:-0} ));
cpu_idle=${5:-0};
mem_total_kb=$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0);
mem_available_kb=$(awk '/^MemAvailable:/{print $2}' /proc/meminfo 2>/dev/null || echo 0);
pids=$(awk '{split($4,a,"/"); print a[2]}' /proc/loadavg 2>/dev/null || echo 0);
net_rx=$(awk -F'[: ]+' 'NR>2{rx+=$3} END{print rx+0}' /proc/net/dev 2>/dev/null || echo 0);
net_tx=$(awk -F'[: ]+' 'NR>2{tx+=$11} END{print tx+0}' /proc/net/dev 2>/dev/null || echo 0);
blk_read=$(awk '$3 ~ /^(vd|sd|xvd|nvme)/ {r+=$6} END{print (r+0)*512}' /proc/diskstats 2>/dev/null || echo 0);
blk_write=$(awk '$3 ~ /^(vd|sd|xvd|nvme)/ {w+=$10} END{print (w+0)*512}' /proc/diskstats 2>/dev/null || echo 0);
printf '{"cpu_total":%s,"cpu_idle":%s,"mem_total_kb":%s,"mem_available_kb":%s,"pids":%s,"net_rx":%s,"net_tx":%s,"blk_read":%s,"blk_write":%s}' \
  "$cpu_total" "$cpu_idle" "$mem_total_kb" "$mem_available_kb" "$pids" "$net_rx" "$net_tx" "$blk_read" "$blk_write"`

func allocateHostPort() (int, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr.Port <= 0 {
		return 0, fmt.Errorf("failed to allocate TCP port")
	}
	return addr.Port, nil
}

func applyPortMapping(pm *firecrackerPortMapping) error {
	hostPort := strconv.Itoa(pm.HostPort)
	guest := net.JoinHostPort(pm.GuestIP, strconv.Itoa(pm.GuestPort))
	guestPort := strconv.Itoa(pm.GuestPort)

	if err := runCmd("iptables", "-t", "nat", "-I", "PREROUTING", "1", "-p", pm.Protocol, "--dport", hostPort, "-j", "DNAT", "--to-destination", guest); err != nil {
		return fmt.Errorf("failed to add PREROUTING rule: %w", err)
	}
	if err := runCmd("iptables", "-t", "nat", "-I", "OUTPUT", "1", "-p", pm.Protocol, "-d", "127.0.0.1", "--dport", hostPort, "-j", "DNAT", "--to-destination", guest); err != nil {
		_ = runCmd("iptables", "-t", "nat", "-D", "PREROUTING", "-p", pm.Protocol, "--dport", hostPort, "-j", "DNAT", "--to-destination", guest)
		return fmt.Errorf("failed to add OUTPUT rule: %w", err)
	}
	if err := runCmd("iptables", "-I", "FORWARD", "1", "-p", pm.Protocol, "-d", pm.GuestIP, "--dport", guestPort, "-j", "ACCEPT"); err != nil {
		_ = runCmd("iptables", "-t", "nat", "-D", "OUTPUT", "-p", pm.Protocol, "-d", "127.0.0.1", "--dport", hostPort, "-j", "DNAT", "--to-destination", guest)
		_ = runCmd("iptables", "-t", "nat", "-D", "PREROUTING", "-p", pm.Protocol, "--dport", hostPort, "-j", "DNAT", "--to-destination", guest)
		return fmt.Errorf("failed to add FORWARD rule: %w", err)
	}
	return nil
}

func removePortMapping(pm *firecrackerPortMapping) error {
	hostPort := strconv.Itoa(pm.HostPort)
	guest := net.JoinHostPort(pm.GuestIP, strconv.Itoa(pm.GuestPort))
	guestPort := strconv.Itoa(pm.GuestPort)

	var firstErr error
	tryDelete := func(args ...string) {
		if err := runCmd("iptables", args...); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	tryDelete("-D", "FORWARD", "-p", pm.Protocol, "-d", pm.GuestIP, "--dport", guestPort, "-j", "ACCEPT")
	tryDelete("-t", "nat", "-D", "OUTPUT", "-p", pm.Protocol, "-d", "127.0.0.1", "--dport", hostPort, "-j", "DNAT", "--to-destination", guest)
	tryDelete("-t", "nat", "-D", "PREROUTING", "-p", pm.Protocol, "--dport", hostPort, "-j", "DNAT", "--to-destination", guest)

	if firstErr != nil {
		return fmt.Errorf("failed to remove firecracker port mapping host=%d guest=%s:%d: %w", pm.HostPort, pm.GuestIP, pm.GuestPort, firstErr)
	}
	return nil
}

func (b *FirecrackerBackend) unexposeAllPorts(id string) {
	b.portMu.Lock()
	mappings := b.portMappings[id]
	delete(b.portMappings, id)
	b.portMu.Unlock()

	for _, pm := range mappings {
		if err := removePortMapping(pm); err != nil {
			logger.WithFields(
				"sandbox_id", id,
				"guest_port", pm.GuestPort,
				"host_port", pm.HostPort,
				"error", err.Error(),
			).Warn("firecracker_sandbox: failed to cleanup port mapping")
		}
	}
}

// Ensure FirecrackerBackend implements optional sandbox backend interfaces.
var _ sandbox.PortExposer = (*FirecrackerBackend)(nil)
