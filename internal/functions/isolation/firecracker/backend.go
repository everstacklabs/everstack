// Package firecracker provides a Firecracker-based isolated function backend.
//
// This is the in-gateway (local) backend: it boots a microVM in the same
// process, so it requires KVM + the firecracker binary on the host. On
// the VPS/managed topology the gateway pod has neither — use the
// firecracker-agent backend (internal/functions/isolation/fcagent),
// which dials the remote KVM-enabled agents instead. Both share the same
// runtime dispatch via internal/functions/isolation/fnexec.
package firecracker

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	sandboxfirecracker "github.com/everstacklabs/everstack/internal/sandbox/firecracker"
	"github.com/google/uuid"
)

// Backend implements isolation.Backend using one Firecracker microVM per execution.
type Backend struct {
	config     Config
	execLogger *isolation.ExecutionLogger

	totalExecutions atomic.Int64
	activeRequests  atomic.Int32
	coldStarts      atomic.Int64
	totalDurationMs atomic.Int64
	totalErrors     atomic.Int64

	mu      sync.RWMutex
	running bool
}

// Config contains Firecracker backend configuration for isolated functions.
type Config struct {
	isolation.Config

	// Firecracker VM config (binary/kernel/rootfs/workdir/pool defaults).
	Firecracker sandboxfirecracker.FirecrackerConfig

	// GuestWorkDir is where execution files are written inside the VM.
	GuestWorkDir string

	// RuntimeRootfs maps runtime -> rootfs image name (without ".ext4").
	// Example: nodejs20 -> nodejs20 (resolves to <rootfs_dir>/nodejs20.ext4).
	RuntimeRootfs map[isolation.Runtime]string

	// Optional DNS servers applied to guest networking.
	DNSServers []string

	// CleanupOnExit removes execution trooper after each run.
	CleanupOnExit bool
}

// DefaultConfig returns sensible defaults for Firecracker isolated execution.
func DefaultConfig() Config {
	return Config{
		Config:       isolation.DefaultConfig(),
		Firecracker:  sandboxfirecracker.DefaultFirecrackerConfig(),
		GuestWorkDir: "/tmp/function",
		RuntimeRootfs: map[isolation.Runtime]string{
			isolation.RuntimeNodeJS20: "nodejs20",
			isolation.RuntimeDeno:     "deno",
			isolation.RuntimePython3:  "python3",
		},
		CleanupOnExit: true,
	}
}

// New creates a new Firecracker isolation backend.
func New(cfg Config) (*Backend, error) {
	if cfg.GuestWorkDir == "" {
		cfg.GuestWorkDir = "/tmp/function"
	}
	if cfg.RuntimeRootfs == nil {
		cfg.RuntimeRootfs = DefaultConfig().RuntimeRootfs
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	return &Backend{
		config:     cfg,
		execLogger: isolation.NewExecutionLogger("firecracker"),
	}, nil
}

// Name returns the backend name.
func (b *Backend) Name() string {
	return "firecracker"
}

// Start marks the backend as running.
func (b *Backend) Start(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = true
	return nil
}

// Stop marks the backend as stopped.
func (b *Backend) Stop(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = false
	return nil
}

// Execute runs the function inside a dedicated Firecracker VM.
func (b *Backend) Execute(ctx context.Context, req isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	b.activeRequests.Add(1)
	defer b.activeRequests.Add(-1)
	defer b.totalExecutions.Add(1)
	b.coldStarts.Add(1)

	b.mu.RLock()
	running := b.running
	b.mu.RUnlock()
	if !running {
		return &isolation.ExecutionResult{
			Success:   false,
			Error:     "firecracker isolation backend is not started",
			ErrorType: isolation.ErrorTypeRuntime,
		}, nil
	}

	b.config.Config.ApplyDefaults(&req)
	b.execLogger.LogExecutionStarted(req, false)

	vmID := buildVMID(req.RequestID)
	vmCfg := sandbox.InstanceConfig{
		Image:          b.rootfsForRuntime(req.Runtime),
		CPULimit:       float64(req.VCPUs),
		MemoryMB:       int64(req.MemoryMB),
		TimeoutSeconds: timeoutSeconds(req.TimeoutMS),
		NetworkMode:    toSandboxNetworkMode(req.NetworkMode),
		AllowedHosts:   req.AllowedHosts,
		DNSServers:     b.config.DNSServers,
		WorkDir:        "/workspace",
	}

	vm, err := sandboxfirecracker.CreateVM(b.config.Firecracker, vmID, vmCfg)
	if err != nil {
		result := &isolation.ExecutionResult{
			Success:   false,
			Error:     fmt.Sprintf("failed to create firecracker VM: %v", err),
			ErrorType: isolation.ErrorTypeRuntime,
		}
		b.totalErrors.Add(1)
		b.execLogger.LogExecutionError(req, result, false, nil)
		return result, nil
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if derr := vm.Destroy(destroyCtx); derr != nil {
			logger.WithFields("vm_id", vmID, "error", derr.Error()).
				Warn("firecracker_isolation: failed to destroy VM")
		}
	}()

	dispatchStart := time.Now()
	result := fnexec.Dispatch(ctx, microVMExecer{vm: vm}, b.config.GuestWorkDir, b.config.CleanupOnExit, req)
	if result != nil {
		result.DurationMS = time.Since(dispatchStart).Milliseconds()
	}
	b.totalDurationMs.Add(result.DurationMS)
	if !result.Success {
		b.totalErrors.Add(1)
		b.execLogger.LogExecutionError(req, result, false, nil)
	} else {
		b.execLogger.LogExecutionCompleted(req, result, false)
	}
	return result, nil
}

// SupportsRuntime checks if a runtime is supported.
func (b *Backend) SupportsRuntime(runtime isolation.Runtime) bool {
	return fnexec.SupportsRuntime(runtime)
}

// Stats returns backend statistics.
func (b *Backend) Stats() isolation.BackendStats {
	return isolation.BackendStats{
		Name:            "firecracker",
		ActiveRequests:  int(b.activeRequests.Load()),
		TotalExecutions: b.totalExecutions.Load(),
		ColdStarts:      b.coldStarts.Load(),
		TotalDurationMs: b.totalDurationMs.Load(),
		TotalErrors:     b.totalErrors.Load(),
		RuntimeStats: map[isolation.Runtime]isolation.RuntimeStats{
			isolation.RuntimeNodeJS20: {},
			isolation.RuntimeDeno:     {},
			isolation.RuntimePython3:  {},
		},
	}
}

// microVMExecer adapts a Firecracker MicroVM's toolbox to fnexec.Execer.
type microVMExecer struct {
	vm *sandboxfirecracker.MicroVM
}

func (e microVMExecer) Exec(ctx context.Context, cmd fnexec.ExecCall) (*fnexec.ExecOutcome, error) {
	res, err := e.vm.ToolboxExec(ctx, sandboxfirecracker.ExecCommand{
		Command:   cmd.Command,
		WorkDir:   cmd.WorkDir,
		Env:       cmd.Env,
		TimeoutMS: cmd.TimeoutMS,
	})
	if err != nil {
		return nil, err
	}
	return &fnexec.ExecOutcome{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		TimedOut: res.TimedOut,
	}, nil
}

func (e microVMExecer) WriteFile(ctx context.Context, path string, content []byte) error {
	return e.vm.ToolboxWriteFile(ctx, path, content)
}

func buildVMID(requestID string) string {
	requestID = fnexec.SafeID(requestID)
	if len(requestID) > 20 {
		requestID = requestID[:20]
	}
	return "fn-" + requestID + "-" + uuid.NewString()[:8]
}

func (b *Backend) rootfsForRuntime(rt isolation.Runtime) string {
	if v, ok := b.config.RuntimeRootfs[rt]; ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return "base"
}

func timeoutSeconds(timeoutMS int) int {
	if timeoutMS <= 0 {
		return 30
	}
	sec := timeoutMS / 1000
	if timeoutMS%1000 != 0 {
		sec++
	}
	if sec < 1 {
		sec = 1
	}
	return sec
}

func toSandboxNetworkMode(m isolation.NetworkMode) sandbox.NetworkMode {
	switch m {
	case isolation.NetworkAllow:
		return sandbox.NetworkAllow
	case isolation.NetworkWhitelist:
		return sandbox.NetworkWhitelist
	default:
		return sandbox.NetworkDeny
	}
}

func validateConfig(cfg Config) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("firecracker isolated backend requires Linux host, got %s", runtime.GOOS)
	}
	if strings.TrimSpace(cfg.Firecracker.BinaryPath) == "" {
		return fmt.Errorf("firecracker binary path is empty")
	}
	if strings.TrimSpace(cfg.Firecracker.KernelPath) == "" {
		return fmt.Errorf("firecracker kernel path is empty")
	}
	if strings.TrimSpace(cfg.Firecracker.RootfsDir) == "" {
		return fmt.Errorf("firecracker rootfs dir is empty")
	}
	if strings.TrimSpace(cfg.Firecracker.WorkDir) == "" {
		return fmt.Errorf("firecracker work dir is empty")
	}
	if _, err := os.Stat(cfg.Firecracker.BinaryPath); err != nil {
		return fmt.Errorf("firecracker binary not found at %s: %w", cfg.Firecracker.BinaryPath, err)
	}
	if _, err := os.Stat(cfg.Firecracker.KernelPath); err != nil {
		return fmt.Errorf("firecracker kernel not found at %s: %w", cfg.Firecracker.KernelPath, err)
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		return fmt.Errorf("kvm not available: %w", err)
	}
	return nil
}
