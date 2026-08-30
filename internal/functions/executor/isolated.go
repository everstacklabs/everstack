package executor

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// IsolatedExecutor executes functions using an isolation backend (Docker or Firecracker).
type IsolatedExecutor struct {
	backend         isolation.Backend
	backendResolver isolation.BackendResolver
}

// NewIsolatedExecutor creates a new isolated executor with the given backend.
func NewIsolatedExecutor(backend isolation.Backend) *IsolatedExecutor {
	return &IsolatedExecutor{
		backend: backend,
	}
}

// NewIsolatedExecutorWithResolver creates an isolated executor with a backend resolver for per-function host targeting.
func NewIsolatedExecutorWithResolver(backend isolation.Backend, resolver isolation.BackendResolver) *IsolatedExecutor {
	return &IsolatedExecutor{
		backend:         backend,
		backendResolver: resolver,
	}
}

// SetBackendResolver sets the backend resolver for per-function Docker host targeting.
func (e *IsolatedExecutor) SetBackendResolver(resolver isolation.BackendResolver) {
	e.backendResolver = resolver
}

// Mode returns the execution mode.
func (e *IsolatedExecutor) Mode() ExecutionMode {
	return ModeIsolated
}

// Execute runs the function code using the isolation backend.
func (e *IsolatedExecutor) Execute(ctx context.Context, execCtx *ExecutionContext, config *FunctionConfig, args map[string]interface{}) (*ToolResult, error) {
	// Validate configuration
	if config.Code == "" {
		return &ToolResult{
			Success: false,
			Error:   "function code is not configured",
		}, fmt.Errorf("function code is not configured")
	}

	if config.Runtime == "" {
		return &ToolResult{
			Success: false,
			Error:   "runtime is not configured",
		}, fmt.Errorf("runtime is not configured")
	}

	// Map runtime string to isolation.Runtime
	runtime := mapRuntime(config.Runtime)
	if runtime == "" {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("unsupported runtime: %s", config.Runtime),
		}, fmt.Errorf("unsupported runtime: %s", config.Runtime)
	}

	// Build execution request
	requestID := execCtx.RequestID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	request := isolation.ExecutionRequest{
		RequestID:     requestID,
		FunctionID:    config.ID,
		TenantID:      execCtx.TenantID,
		CorrelationID: execCtx.CorrelationID,
		TraceID:       execCtx.TraceID,
		FunctionName:  config.Name,
		Runtime:       runtime,
		Code:          config.Code,
		Packages:      config.Packages,
		Arguments:     args,
		TimeoutMS:     int(config.TimeoutMs),
		MemoryMB:      config.MemoryMB,
		VCPUs:         config.VCPUs,
		NetworkMode:   mapNetworkMode(config.NetworkMode),
		AllowedHosts:  config.AllowedHosts,
	}

	// Resolve backend: use per-function host if configured, otherwise use default
	execBackend := e.backend
	if config.DockerHost != "" && e.backendResolver != nil {
		resolved, err := e.backendResolver.GetBackend(ctx, config.DockerHost)
		if err != nil {
			logger.WithFields(
				"function_id", config.ID,
				"docker_host", config.DockerHost,
				"error", err.Error(),
			).Error("failed to resolve Docker backend for function host")
			return &ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to resolve Docker backend for host %s: %v", config.DockerHost, err),
			}, err
		}
		execBackend = resolved
	}

	// Check if backend supports this runtime
	if !execBackend.SupportsRuntime(runtime) {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("runtime %s not supported by %s backend", config.Runtime, execBackend.Name()),
		}, fmt.Errorf("runtime not supported")
	}

	logger.WithFields(
		"function_id", config.ID,
		"function_name", config.Name,
		"runtime", config.Runtime,
		"backend", execBackend.Name(),
		"timeout_ms", config.TimeoutMs,
		"memory_mb", config.MemoryMB,
		"network_mode", config.NetworkMode,
		"correlation_id", execCtx.CorrelationID,
	).Info("executing isolated function")

	// Execute via backend
	result, err := execBackend.Execute(ctx, request)
	if err != nil {
		logger.WithFields(
			"function_id", config.ID,
			"error", err.Error(),
			"correlation_id", execCtx.CorrelationID,
		).Error("isolated execution failed")

		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("execution failed: %v", err),
		}, err
	}

	// Log execution result
	logger.WithFields(
		"function_id", config.ID,
		"function_name", config.Name,
		"success", result.Success,
		"duration_ms", result.DurationMS,
		"correlation_id", execCtx.CorrelationID,
	).Info("isolated execution completed")

	// Convert to tool result
	if !result.Success {
		return &ToolResult{
			Success:    false,
			Error:      result.Error,
			DurationMs: result.DurationMS,
		}, nil
	}

	return &ToolResult{
		Success:    true,
		Content:    result.Result,
		DurationMs: result.DurationMS,
	}, nil
}

// mapRuntime maps a runtime string to isolation.Runtime.
func mapRuntime(runtime string) isolation.Runtime {
	switch runtime {
	case "nodejs20", "node20", "node":
		return isolation.RuntimeNodeJS20
	case "deno":
		return isolation.RuntimeDeno
	case "python3", "python":
		return isolation.RuntimePython3
	default:
		return ""
	}
}

// mapNetworkMode maps a network mode string to isolation.NetworkMode.
func mapNetworkMode(mode string) isolation.NetworkMode {
	switch mode {
	case "allow":
		return isolation.NetworkAllow
	case "whitelist":
		return isolation.NetworkWhitelist
	case "deny":
		fallthrough
	default:
		return isolation.NetworkDeny
	}
}

// Start starts the backend.
func (e *IsolatedExecutor) Start(ctx context.Context) error {
	if e.backend != nil {
		return e.backend.Start(ctx)
	}
	return nil
}

// Stop stops the backend.
func (e *IsolatedExecutor) Stop(ctx context.Context) error {
	if e.backend != nil {
		return e.backend.Stop(ctx)
	}
	return nil
}

// Stats returns backend statistics.
func (e *IsolatedExecutor) Stats() isolation.BackendStats {
	if e.backend != nil {
		return e.backend.Stats()
	}
	return isolation.BackendStats{}
}

// BackendName returns the name of the isolation backend.
func (e *IsolatedExecutor) BackendName() string {
	if e.backend != nil {
		return e.backend.Name()
	}
	return "none"
}
