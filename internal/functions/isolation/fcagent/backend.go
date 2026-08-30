// Package fcagent provides an isolated-function execution backend that
// runs functions on remote Firecracker agents.
//
// It is the functions counterpart to the sandbox FCAgentBackend: the
// gateway never boots a microVM itself (it has no KVM). Instead it dials
// the firecracker-agent fleet — the KVM-enabled worker nodes — over gRPC
// and issues an InvokeFunction RPC. The agent owns the persistent
// execution environment (a reused microVM); this backend is a thin,
// stateless proxy that only has to pick a healthy agent and forward.
//
// It reuses the sandbox fcagent package's health-gated round-robin
// discovery + load balancer (NewClientPool). It deliberately does NOT
// reuse the sticky per-sandbox routing / agent_target machinery: a
// function invocation is ephemeral and has no VM to route back to.
package fcagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
	sandboxfcagent "github.com/everstacklabs/everstack/internal/sandbox/fcagent"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Config configures the firecracker-agent isolated-function backend.
type Config struct {
	// Config carries the generic isolation defaults (timeout/memory/
	// vcpus/network + per-tenant overrides). Applied before every RPC.
	isolation.Config

	// Agent points at the firecracker-agent fleet — the SAME service the
	// sandbox backend dials. Reuses its DNS discovery + mTLS wiring.
	Agent sandboxfcagent.Config
}

// Backend implements isolation.Backend by proxying to remote agents.
type Backend struct {
	baseCfg isolation.Config
	disc    *sandboxfcagent.Discovery
	lb      *sandboxfcagent.LoadBalancer

	totalExecutions atomic.Int64
	activeRequests  atomic.Int32
	coldStarts      atomic.Int64
	totalDurationMs atomic.Int64
	totalErrors     atomic.Int64
}

// New builds a firecracker-agent function backend over the agent fleet.
func New(cfg Config) (*Backend, error) {
	disc, lb, err := sandboxfcagent.NewClientPool(cfg.Agent)
	if err != nil {
		return nil, err
	}
	return &Backend{
		baseCfg: cfg.Config,
		disc:    disc,
		lb:      lb,
	}, nil
}

func (b *Backend) Name() string { return "firecracker-agent" }

// Start warms DNS discovery so the first invocation doesn't pay a cold
// resolve. Best-effort — a transient resolve failure isn't fatal; the
// refresh loop retries.
func (b *Backend) Start(_ context.Context) error {
	_ = b.disc.RefreshNow()
	return nil
}

// Stop tears down the discovery loop and its gRPC connections.
func (b *Backend) Stop(_ context.Context) error {
	b.disc.Stop()
	return nil
}

func (b *Backend) SupportsRuntime(runtime isolation.Runtime) bool {
	return fnexec.SupportsRuntime(runtime)
}

// Execute forwards one invocation to a healthy agent. Transport/agent
// failures surface as an error (the caller decides how to present them);
// function-level failures come back inside the ExecutionResult with a
// nil error, matching the docker/firecracker backends.
func (b *Backend) Execute(ctx context.Context, req isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	b.activeRequests.Add(1)
	defer b.activeRequests.Add(-1)
	defer b.totalExecutions.Add(1)

	b.baseCfg.ApplyDefaults(&req)

	if !fnexec.SupportsRuntime(req.Runtime) {
		return &isolation.ExecutionResult{
			Success:   false,
			Error:     fmt.Sprintf("unsupported runtime: %q", req.Runtime),
			ErrorType: isolation.ErrorTypeRuntime,
		}, nil
	}

	fcReq := &fcpb.InvokeFunctionRequest{
		FunctionId:   req.FunctionID,
		TenantId:     req.TenantID,
		RequestId:    req.RequestID,
		EnvKey:       computeEnvKey(req),
		Runtime:      string(req.Runtime),
		Code:         req.Code,
		Packages:     req.Packages,
		ArgsJson:     marshalArgs(req.Arguments),
		TimeoutMs:    int32(req.TimeoutMS),
		MemoryMb:     int64(req.MemoryMB),
		Vcpus:        float64(req.VCPUs),
		NetworkMode:  string(req.NetworkMode),
		AllowedHosts: req.AllowedHosts,
	}

	res, _, err := sandboxfcagent.TryEach(b.lb, ctx,
		func(ctx context.Context, _ string, cli fcpb.FirecrackerAgentClient) (*fcpb.InvokeFunctionResult, bool, error) {
			out, callErr := cli.InvokeFunction(ctx, fcReq)
			if callErr != nil {
				return nil, isRetryableInvokeError(callErr), callErr
			}
			return out, false, nil
		})
	if err != nil {
		b.totalErrors.Add(1)
		return nil, fmt.Errorf("firecracker-agent invoke function: %w", err)
	}

	result := resultFromProto(res)
	b.totalDurationMs.Add(result.DurationMS)
	if res.GetColdStart() {
		b.coldStarts.Add(1)
	}
	if !result.Success {
		b.totalErrors.Add(1)
	}
	return result, nil
}

func (b *Backend) Stats() isolation.BackendStats {
	return isolation.BackendStats{
		Name:            b.Name(),
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

// isRetryableInvokeError reports whether a failed InvokeFunction call
// should be retried on a different agent. Two cases:
//   - capacity-class errors ("this node is full") — the node can't take
//     the work, another can.
//   - gRPC Unavailable — the standard "connection failed / server not
//     ready" signal, which means the request did not reach a serving
//     agent, so retrying elsewhere is safe. We deliberately do NOT retry
//     other post-dispatch errors: a function may have side effects, and
//     re-running it on another agent would double-execute.
func isRetryableInvokeError(err error) bool {
	if err == nil {
		return false
	}
	if sandboxfcagent.IsCapacityError(err) {
		return true
	}
	return status.Code(err) == codes.Unavailable
}

func resultFromProto(r *fcpb.InvokeFunctionResult) *isolation.ExecutionResult {
	if r == nil {
		return &isolation.ExecutionResult{
			Success:   false,
			Error:     "empty result from agent",
			ErrorType: isolation.ErrorTypeRuntime,
		}
	}
	out := &isolation.ExecutionResult{
		Success:    r.GetSuccess(),
		Stdout:     r.GetStdout(),
		Stderr:     r.GetStderr(),
		Error:      r.GetError(),
		ErrorType:  isolation.ErrorType(r.GetErrorType()),
		DurationMS: r.GetDurationMs(),
	}
	if rj := r.GetResultJson(); rj != "" {
		var v interface{}
		if err := json.Unmarshal([]byte(rj), &v); err == nil {
			out.Result = v
		} else {
			out.Result = rj
		}
	}
	return out
}

func marshalArgs(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

// computeEnvKey derives the persistent-environment key for an
// invocation. It hashes everything that determines how the env's VM is
// built and what runs in it — runtime, code, packages, resource sizing,
// and network policy — plus function/tenant identity so distinct
// functions never share an env even with byte-identical code. Any change
// to these spins a fresh env on the agent and lets the old one idle out.
//
// This is the phase-1 stand-in for the phase-2 content-addressed
// function revision: once revisions exist, the revision's content hash
// becomes this key directly.
func computeEnvKey(req isolation.ExecutionRequest) string {
	h := sha256.New()
	fmt.Fprintf(h, "runtime=%s\x00", req.Runtime)
	fmt.Fprintf(h, "mem=%d\x00vcpu=%d\x00net=%s\x00", req.MemoryMB, req.VCPUs, req.NetworkMode)
	for _, host := range req.AllowedHosts {
		fmt.Fprintf(h, "host=%s\x00", host)
	}
	for _, p := range req.Packages {
		fmt.Fprintf(h, "pkg=%s\x00", p)
	}
	fmt.Fprintf(h, "fn=%s\x00tenant=%s\x00code=", req.FunctionID, req.TenantID)
	h.Write([]byte(req.Code))
	return hex.EncodeToString(h.Sum(nil))
}
