// Package isolation provides isolated function execution backends.
// Currently supports Docker for self-hosted deployments.
// Firecracker support planned for cloud/multi-tenant deployments.
package isolation

import (
	"context"
	"time"
)

// Backend is the interface for isolated execution backends.
type Backend interface {
	// Name returns the backend name (e.g., "docker")
	Name() string

	// Start initializes the backend
	Start(ctx context.Context) error

	// Stop gracefully shuts down the backend
	Stop(ctx context.Context) error

	// Execute runs code in an isolated environment
	Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error)

	// SupportsRuntime checks if a runtime is supported
	SupportsRuntime(runtime Runtime) bool

	// Stats returns backend statistics
	Stats() BackendStats
}

// Runtime represents a supported execution runtime.
type Runtime string

const (
	RuntimeNodeJS20 Runtime = "nodejs20"
	RuntimeDeno     Runtime = "deno"
	RuntimePython3  Runtime = "python3"
)

// ValidRuntimes returns all valid runtime values.
func ValidRuntimes() []Runtime {
	return []Runtime{RuntimeNodeJS20, RuntimeDeno, RuntimePython3}
}

// ExecutionRequest contains everything needed to execute user code.
type ExecutionRequest struct {
	// Identifiers
	RequestID  string
	FunctionID string
	TenantID   string

	// Tracing context (for correlation with parent request)
	CorrelationID string
	TraceID       string
	FunctionName  string

	// Code to execute
	Runtime  Runtime
	Code     string
	Packages []string

	// Files, Entrypoint and Export describe a multi-file project invocation.
	// They are optional for backward compatibility with single-file Code.
	// When Files is non-empty, Entrypoint must name one of those files.
	Files      []SourceFile
	Entrypoint string
	Export     string

	// Function arguments
	Arguments map[string]interface{}

	// Resource limits
	TimeoutMS int
	MemoryMB  int
	VCPUs     int

	// Network policy
	NetworkMode  NetworkMode
	AllowedHosts []string
}

// SourceFile is one relative file materialized for a project invocation.
type SourceFile struct {
	Path    string
	Content []byte
	Mode    int32
}

// NetworkMode defines network access policy.
type NetworkMode string

const (
	NetworkDeny      NetworkMode = "deny"      // No network access (default)
	NetworkWhitelist NetworkMode = "whitelist" // Only allowed hosts
	NetworkAllow     NetworkMode = "allow"     // Full network access
)

// ExecutionResult is the result from code execution.
type ExecutionResult struct {
	// Success indicates if execution completed without errors
	Success bool

	// Result is the return value from the function
	Result interface{}

	// Error information
	Error     string
	ErrorType ErrorType

	// Output streams
	Stdout string
	Stderr string

	// Metrics
	DurationMS   int64
	MemoryUsedMB int
}

// ErrorType categorizes execution errors.
type ErrorType string

const (
	ErrorTypeNone    ErrorType = ""
	ErrorTypeTimeout ErrorType = "timeout"
	ErrorTypeOOM     ErrorType = "oom"
	ErrorTypeSyntax  ErrorType = "syntax"
	ErrorTypeRuntime ErrorType = "runtime"
	ErrorTypeNetwork ErrorType = "network"
)

// BackendStats contains metrics about the backend.
type BackendStats struct {
	Name            string
	ActiveRequests  int
	TotalExecutions int64
	RuntimeStats    map[Runtime]RuntimeStats

	// Execution metrics
	WarmHits        int64
	ColdStarts      int64
	TotalDurationMs int64
	TotalErrors     int64

	// Pool metrics (optional)
	PoolMetrics *PoolMetrics
}

// PoolMetrics contains container pool health metrics.
type PoolMetrics struct {
	TotalRecycled         int64
	TotalEvictedIdle      int64
	TotalEvictedUnhealthy int64
}

// RuntimeStats contains metrics for a specific runtime.
type RuntimeStats struct {
	Ready     int
	Executing int
	Total     int
}

// Config contains configuration for isolation backends.
type Config struct {
	// Default resource limits
	DefaultTimeoutMS int
	DefaultMemoryMB  int
	DefaultVCPUs     int

	// Network defaults
	DefaultNetworkMode NetworkMode

	// TenantDefaults, when set, returns per-tenant overrides for the
	// timeout/memory defaults. Wired at gateway startup from the
	// runtime_config service. Returning zeros means "no tenant
	// override, fall back to the deployment defaults below". Backend
	// type / image / docker host stay deployment-time.
	TenantDefaults func(tenantID string) (timeoutMS int, memoryMB int)
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		DefaultTimeoutMS:   30000,
		DefaultMemoryMB:    512,
		DefaultVCPUs:       1,
		DefaultNetworkMode: NetworkDeny,
	}
}

// ApplyDefaults fills in missing request fields with config defaults.
// Per-tenant overrides win over deployment defaults; both are upper-
// bounded by Config (no resolver promotion path here, just defaulting).
func (c Config) ApplyDefaults(req *ExecutionRequest) {
	timeoutDefault := c.DefaultTimeoutMS
	memoryDefault := c.DefaultMemoryMB
	if c.TenantDefaults != nil && req.TenantID != "" {
		if t, m := c.TenantDefaults(req.TenantID); t > 0 || m > 0 {
			if t > 0 {
				timeoutDefault = t
			}
			if m > 0 {
				memoryDefault = m
			}
		}
	}
	if req.TimeoutMS == 0 {
		req.TimeoutMS = timeoutDefault
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = memoryDefault
	}
	if req.VCPUs == 0 {
		req.VCPUs = c.DefaultVCPUs
	}
	if req.NetworkMode == "" {
		req.NetworkMode = c.DefaultNetworkMode
	}
}

// ExecutionTimeout returns the timeout as a time.Duration.
func (r *ExecutionRequest) ExecutionTimeout() time.Duration {
	if r.TimeoutMS <= 0 {
		return 30 * time.Second
	}
	return time.Duration(r.TimeoutMS) * time.Millisecond
}

// BackendResolver resolves the appropriate isolation backend for a given Docker host.
type BackendResolver interface {
	// GetBackend returns the backend for the given Docker host.
	// Empty string returns the global default backend.
	GetBackend(ctx context.Context, dockerHost string) (Backend, error)
	// GlobalHost returns the configured global Docker host.
	GlobalHost() string
}
