package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// envBackend is the slice of the Firecracker backend the function env
// manager needs: create/reuse a tracked VM, run commands, write files,
// and tear it down. *firecracker.FirecrackerBackend satisfies it; the
// narrow interface keeps the manager unit-testable without KVM.
type envBackend interface {
	Create(ctx context.Context, id string, cfg sandbox.InstanceConfig) (*sandbox.Instance, error)
	Exec(ctx context.Context, id string, req sandbox.ExecRequest) (*sandbox.ExecResult, error)
	WriteFile(ctx context.Context, id, path string, content []byte) error
	Destroy(ctx context.Context, id string) error
}

// Environment sizing floors. The gateway normally sends resolved values
// (it applies tenant/deployment defaults before the RPC), but the agent
// guards against a zero/partial request so a bad caller can't create a
// 0-CPU / 0-MB VM.
const (
	defaultFnTimeoutMS = 30000
	defaultFnMemoryMB  = 512
	defaultFnVCPUs     = 1.0
)

// InvokeFunction runs an isolated function inside a persistent, per-
// function execution environment on this node. See FunctionEnvManager.
func (s *AgentServer) InvokeFunction(ctx context.Context, req *fcpb.InvokeFunctionRequest) (*fcpb.InvokeFunctionResult, error) {
	if req == nil {
		return nil, fmt.Errorf("invoke_function: request is nil")
	}
	if s.funcEnvs == nil {
		return nil, status.Error(codes.Unavailable, "function runtime not ready")
	}

	runtime := isolation.Runtime(req.Runtime)
	if !fnexec.SupportsRuntime(runtime) {
		return &fcpb.InvokeFunctionResult{
			Success:   false,
			Error:     fmt.Sprintf("unsupported runtime: %q", req.Runtime),
			ErrorType: string(isolation.ErrorTypeRuntime),
		}, nil
	}

	timeoutMS := int(req.TimeoutMs)
	if timeoutMS <= 0 {
		timeoutMS = defaultFnTimeoutMS
	}
	memoryMB := req.MemoryMb
	if memoryMB <= 0 {
		memoryMB = defaultFnMemoryMB
	}
	vcpus := req.Vcpus
	if vcpus <= 0 {
		vcpus = defaultFnVCPUs
	}
	networkMode := req.NetworkMode
	if networkMode == "" {
		networkMode = string(isolation.NetworkDeny)
	}

	execReq := isolation.ExecutionRequest{
		RequestID:    req.RequestId,
		FunctionID:   req.FunctionId,
		TenantID:     req.TenantId,
		Runtime:      runtime,
		Code:         req.Code,
		Packages:     req.Packages,
		TimeoutMS:    timeoutMS,
		MemoryMB:     int(memoryMB),
		VCPUs:        int(vcpus),
		NetworkMode:  isolation.NetworkMode(networkMode),
		AllowedHosts: req.AllowedHosts,
	}
	if req.ArgsJson != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(req.ArgsJson), &args); err != nil {
			return &fcpb.InvokeFunctionResult{
				Success:   false,
				Error:     fmt.Sprintf("invalid args_json: %v", err),
				ErrorType: string(isolation.ErrorTypeRuntime),
			}, nil
		}
		execReq.Arguments = args
	}

	// env_key must uniquely identify the (code, runtime, sizing) tuple.
	// Fall back to a per-function+runtime key if the gateway omitted it,
	// so a missing hash never collapses distinct functions onto one env.
	envKey := req.EnvKey
	if envKey == "" {
		envKey = fmt.Sprintf("%s:%s:%s", req.TenantId, req.FunctionId, req.Runtime)
	}

	spec := envSpec{
		runtime:      runtime,
		memoryMB:     memoryMB,
		vcpus:        vcpus,
		networkMode:  sandbox.NetworkMode(networkMode),
		allowedHosts: req.AllowedHosts,
		tenantID:     req.TenantId,
		functionID:   req.FunctionId,
	}

	result, coldStart, err := s.funcEnvs.Invoke(ctx, envKey, spec, execReq)
	if err != nil {
		return nil, fmt.Errorf("node %s: invoke function failed: %w", s.nodeName, err)
	}
	return executionResultToProto(result, coldStart), nil
}

func executionResultToProto(r *isolation.ExecutionResult, coldStart bool) *fcpb.InvokeFunctionResult {
	if r == nil {
		return &fcpb.InvokeFunctionResult{
			Success:   false,
			Error:     "nil execution result",
			ErrorType: string(isolation.ErrorTypeRuntime),
			ColdStart: coldStart,
		}
	}
	out := &fcpb.InvokeFunctionResult{
		Success:    r.Success,
		Stdout:     r.Stdout,
		Stderr:     r.Stderr,
		Error:      r.Error,
		ErrorType:  string(r.ErrorType),
		DurationMs: r.DurationMS,
		ColdStart:  coldStart,
	}
	if r.Result != nil {
		if b, err := json.Marshal(r.Result); err == nil {
			out.ResultJson = string(b)
		}
	}
	return out
}

// FunctionEnvManager owns the persistent execution environments backing
// isolated-function invocations on this agent node.
//
// An "environment" is a long-lived Firecracker microVM created through
// the SAME backend.Create path sandboxes use, so it lands in the
// backend's tracked set (TrackedPIDs/TrackedIDs) and is therefore safe
// from the orphan reaper and workdir reconciler, recoverable across
// agent restarts, and torn down with the ordinary Destroy path (no
// /workspace→R2 archive — that lives in the orchestrator, not Create).
//
// Environments are keyed by env_key (a gateway-computed hash over
// runtime+code+packages+sizing), reused across invocations, health-
// checked before every dispatch, transparently recreated if the guest
// has died, and idle-reaped after EnvLifetime. This is the phase-1
// "persistent env, trivial retention" policy behind the InvokeFunction
// contract; later phases raise retention and add snapshot restore
// without changing the wire shape.
type FunctionEnvManager struct {
	backend      envBackend
	guestWorkDir string
	dnsServers   []string
	// envLifetime doubles as the idle TTL (reap an env untouched this
	// long) and the VM's advisory TimeoutSeconds. It is deliberately
	// NOT the per-invocation execution timeout.
	envLifetime time.Duration

	mu   sync.Mutex
	envs map[string]*funcEnv

	stop chan struct{}
	once sync.Once
}

// funcEnv is one persistent environment. The VM's sandbox id is the
// unit of dispatch; active tracks in-flight invocations so the reaper
// never destroys a busy env.
type funcEnv struct {
	key     string
	runtime isolation.Runtime

	// ensureMu serializes create/recreate of the underlying VM. Dispatch
	// itself runs WITHOUT this lock (each invocation uses its own scratch
	// workdir), so invocations to a warm env run concurrently.
	ensureMu  sync.Mutex
	sandboxID string // guarded by ensureMu for writes; read after ensureAlive returns it

	active       atomic.Int32
	lastUsedNano atomic.Int64
	createdAt    time.Time
}

func (e *funcEnv) touch() { e.lastUsedNano.Store(time.Now().UnixNano()) }

// envSpec carries the sizing/isolation needed to CREATE an env's VM. It
// is fixed for the life of the env (env_key already folds these in, so
// a differently-sized invocation lands on a different env).
type envSpec struct {
	runtime      isolation.Runtime
	memoryMB     int64
	vcpus        float64
	networkMode  sandbox.NetworkMode
	allowedHosts []string
	tenantID     string
	functionID   string
}

const (
	defaultEnvLifetime  = 10 * time.Minute
	defaultGuestWorkDir = "/tmp/function"
	envReapInterval     = 30 * time.Second
	healthProbeTimeout  = 3 * time.Second

	// functionEnvIDPrefix marks a sandbox VM as an ephemeral function
	// environment. Distinct from sandbox ids (sbx_*/wks_*), so a startup
	// sweep can identify and reap recovered function envs.
	functionEnvIDPrefix = "fn-"
)

// NewFunctionEnvManager builds a manager over an initialized backend and
// starts its idle-reaper goroutine. Call Stop (or cancel via the caller
// wiring a goroutine on ctx.Done) to release it.
func NewFunctionEnvManager(backend envBackend, guestWorkDir string, dnsServers []string, envLifetime time.Duration) *FunctionEnvManager {
	if guestWorkDir == "" {
		guestWorkDir = defaultGuestWorkDir
	}
	if envLifetime <= 0 {
		envLifetime = defaultEnvLifetime
	}
	m := &FunctionEnvManager{
		backend:      backend,
		guestWorkDir: guestWorkDir,
		dnsServers:   dnsServers,
		envLifetime:  envLifetime,
		envs:         make(map[string]*funcEnv),
		stop:         make(chan struct{}),
	}
	go m.reapLoop()
	return m
}

// Stop halts the reaper. Existing env VMs are left to the backend's own
// teardown (agent shutdown tears the process down; hostPID VMs survive
// for the next agent to recover).
func (m *FunctionEnvManager) Stop() {
	m.once.Do(func() { close(m.stop) })
}

// Invoke runs one function invocation, creating or reusing the env for
// envKey. Returns the execution result plus whether this invocation
// cold-started the environment.
func (m *FunctionEnvManager) Invoke(ctx context.Context, envKey string, spec envSpec, req isolation.ExecutionRequest) (*isolation.ExecutionResult, bool, error) {
	if !fnexec.SupportsRuntime(spec.runtime) {
		return nil, false, fmt.Errorf("unsupported runtime: %s", spec.runtime)
	}

	// Compose the registry key from tenant + function + the gateway's
	// env_key. Defense in depth for tenant isolation: even if two tenants
	// ever produced a colliding env_key, the composite key keeps their
	// environments separate, so an invocation can never dispatch into
	// another tenant's (or another function's) VM. [[feedback_no_tenant_leaks]]
	regKey := spec.tenantID + "\x00" + spec.functionID + "\x00" + envKey

	// Reserve the env under the manager lock and mark it busy so the
	// reaper can never destroy a VM we are about to dispatch into. The
	// active++ MUST happen under m.mu, paired with the reaper's active
	// check under the same lock (see reapIdle).
	m.mu.Lock()
	env, ok := m.envs[regKey]
	if !ok {
		env = &funcEnv{key: regKey, runtime: spec.runtime}
		env.touch()
		m.envs[regKey] = env
	}
	env.active.Add(1)
	m.mu.Unlock()

	defer func() {
		env.active.Add(-1)
		env.touch()
	}()

	sandboxID, coldStart, err := m.ensureAlive(ctx, env, spec)
	if err != nil {
		return nil, false, fmt.Errorf("prepare function env: %w", err)
	}

	ex := &backendExecer{backend: m.backend, sandboxID: sandboxID}
	// cleanupOnExit=false: each invocation uses its own scratch dir, and
	// leaving them lets a warm env amortize; the VM is ephemeral anyway.
	start := time.Now()
	result := fnexec.Dispatch(ctx, ex, m.guestWorkDir, false, req)
	if result != nil {
		result.DurationMS = time.Since(start).Milliseconds()
	}
	return result, coldStart, nil
}

// ensureAlive guarantees env has a live VM, recreating it if the guest
// has died. Returns the sandbox id to dispatch into. Serialized per env
// via ensureMu; concurrent callers coalesce on the same VM.
func (m *FunctionEnvManager) ensureAlive(ctx context.Context, env *funcEnv, spec envSpec) (string, bool, error) {
	env.ensureMu.Lock()
	defer env.ensureMu.Unlock()

	if env.sandboxID != "" {
		if m.healthy(ctx, env.sandboxID) {
			return env.sandboxID, false, nil
		}
		// Health probe failed. Do NOT tear down a VM that concurrent
		// invocations may still be running in — a transient probe failure
		// under load must not nuke a live env out from under them. active
		// includes this caller, so >1 means another invocation holds the
		// VM; return it and let this dispatch attempt it (if the guest is
		// genuinely dead this caller fails, but concurrent invocations are
		// unharmed). Only the sole user recreates.
		if env.active.Load() > 1 {
			return env.sandboxID, false, nil
		}
		// Sole user — safe to replace the dead guest (the ~320s guest-death
		// class of failure, or any crash). Tear the corpse down first.
		logger.WithFields("env_key", env.key, "sandbox_id", env.sandboxID).
			Warn("firecracker-agent: function env guest unhealthy; recreating")
		destroyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = m.backend.Destroy(destroyCtx, env.sandboxID)
		cancel()
		env.sandboxID = ""
	}

	id := newFunctionEnvID(spec.functionID)
	cfg := sandbox.InstanceConfig{
		Image:        "base", // base.ext4 carries node + python3 toolchains
		CPULimit:     spec.vcpus,
		MemoryMB:     spec.memoryMB,
		NetworkMode:  spec.networkMode,
		AllowedHosts: spec.allowedHosts,
		DNSServers:   m.dnsServers,
		WorkDir:      "/workspace",
		TenantID:     spec.tenantID,
		// TimeoutSeconds is env-LIFETIME policy (advisory on the agent —
		// nothing enforces ExpiresAt here), never the invocation timeout.
		TimeoutSeconds: int(m.envLifetime / time.Second),
	}
	if _, err := m.backend.Create(ctx, id, cfg); err != nil {
		return "", false, err
	}
	env.sandboxID = id
	env.createdAt = time.Now()
	return id, true, nil
}

// healthy runs a trivial in-guest command as a definitive liveness
// probe. A dead guest surfaces as a vsock/route error or non-zero exit.
func (m *FunctionEnvManager) healthy(ctx context.Context, sandboxID string) bool {
	hctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	res, err := m.backend.Exec(hctx, sandboxID, sandbox.ExecRequest{
		Command: []string{"true"},
		Timeout: healthProbeTimeout,
	})
	return err == nil && res != nil && res.ExitCode == 0 && !res.TimedOut
}

func (m *FunctionEnvManager) reapLoop() {
	t := time.NewTicker(envReapInterval)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.reapIdle()
		}
	}
}

// reapIdle destroys environments untouched for longer than envLifetime
// and not currently servicing an invocation. The active check happens
// under m.mu, matching Invoke's active++ under the same lock, so a
// reserved env is never reaped out from under a dispatch.
func (m *FunctionEnvManager) reapIdle() {
	now := time.Now()
	var reap []*funcEnv

	m.mu.Lock()
	for key, env := range m.envs {
		if env.active.Load() != 0 {
			continue
		}
		last := time.Unix(0, env.lastUsedNano.Load())
		if now.Sub(last) <= m.envLifetime {
			continue
		}
		reap = append(reap, env)
		delete(m.envs, key)
	}
	m.mu.Unlock()

	for _, env := range reap {
		env.ensureMu.Lock()
		id := env.sandboxID
		env.sandboxID = ""
		env.ensureMu.Unlock()
		if id == "" {
			continue
		}
		destroyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := m.backend.Destroy(destroyCtx, id); err != nil {
			logger.WithFields("env_key", env.key, "sandbox_id", id, "error", err.Error()).
				Warn("firecracker-agent: failed to reap idle function env")
		}
		cancel()
	}
}

// newFunctionEnvID builds a tracked sandbox id for a function env VM.
// The "fn-" prefix distinguishes them in logs and tooling from user
// sandboxes; uniqueness comes from a fresh UUID so a recreated env never
// collides with the corpse still being torn down.
func newFunctionEnvID(functionID string) string {
	fid := fnexec.SafeID(functionID)
	if len(fid) > 16 {
		fid = fid[:16]
	}
	if fid == "" {
		fid = "fn"
	}
	return functionEnvIDPrefix + fid + "-" + uuid.NewString()[:8]
}

// SweepRecoveredFunctionEnvs destroys any function-environment VM that
// the backend re-attached to on startup (via Recover). Function envs are
// ephemeral and their registry is in-memory, so a survivor from a
// previous agent process would be tracked (hence reaper-safe) yet
// unmanaged — leaking against the node VM cap forever. Reaping them at
// startup is the correct policy: a warm env that outlives its agent has
// no business surviving, and the next invocation cold-starts a fresh one.
//
// Called once after backend.Recover, before the agent starts serving.
func SweepRecoveredFunctionEnvs(ctx context.Context, backend envLister) {
	instances, err := backend.List(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).
			Warn("firecracker-agent: function env startup sweep: list failed")
		return
	}
	for _, inst := range instances {
		if inst == nil || !strings.HasPrefix(inst.ID, functionEnvIDPrefix) {
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if derr := backend.Destroy(dctx, inst.ID); derr != nil {
			logger.WithFields("sandbox_id", inst.ID, "error", derr.Error()).
				Warn("firecracker-agent: failed to reap recovered function env")
		} else {
			logger.WithFields("sandbox_id", inst.ID).
				Info("firecracker-agent: reaped recovered function env from previous instance")
		}
		cancel()
	}
}

// envLister is the slice of the backend needed to sweep recovered
// function envs at startup.
type envLister interface {
	List(ctx context.Context) ([]*sandbox.Instance, error)
	Destroy(ctx context.Context, id string) error
}

// backendExecer adapts the sandbox.Backend Exec/WriteFile surface of a
// specific env VM to the fnexec.Execer interface.
type backendExecer struct {
	backend   envBackend
	sandboxID string
}

func (e *backendExecer) Exec(ctx context.Context, cmd fnexec.ExecCall) (*fnexec.ExecOutcome, error) {
	res, err := e.backend.Exec(ctx, e.sandboxID, sandbox.ExecRequest{
		Command: cmd.Command,
		WorkDir: cmd.WorkDir,
		Env:     cmd.Env,
		Timeout: time.Duration(cmd.TimeoutMS) * time.Millisecond,
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

func (e *backendExecer) WriteFile(ctx context.Context, path string, content []byte) error {
	return e.backend.WriteFile(ctx, e.sandboxID, path, content)
}
