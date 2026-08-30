// Package projectruntime executes project-scoped functions from immutable
// agent revisions inside an already-running Everstack sandbox.
package projectruntime

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/revision"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
)

const defaultGuestWorkDir = "/workspace/.everstack/functions"

// Runner is the narrow execution boundary consumed by agent tool handlers.
type Runner interface {
	Run(context.Context, fnexec.Execer, RunRequest) *isolation.ExecutionResult
}

// Config controls invocation scratch space and cleanup.
type Config struct {
	GuestWorkDir  string
	CleanupOnExit bool
}

// RunRequest selects one project-scoped function from a pinned revision.
type RunRequest struct {
	RequestID     string
	TenantID      string
	Revision      *revision.Revision
	Function      string
	Arguments     map[string]interface{}
	TimeoutMS     int
	MemoryMB      int
	VCPUs         int
	NetworkMode   isolation.NetworkMode
	AllowedHosts  []string
	CorrelationID string
	TraceID       string
}

// Runtime dispatches project functions through the shared fnexec transport.
// It owns no sandbox lifecycle; the caller supplies an already-routed guest.
type Runtime struct {
	config Config
}

// New creates a project runtime.
func New(config Config) *Runtime {
	if config.GuestWorkDir == "" {
		config.GuestWorkDir = defaultGuestWorkDir
	}
	return &Runtime{config: config}
}

// Run executes one declared export from the exact files in the revision.
func (r *Runtime) Run(ctx context.Context, guest fnexec.Execer, req RunRequest) *isolation.ExecutionResult {
	started := time.Now()
	fail := func(format string, args ...interface{}) *isolation.ExecutionResult {
		return &isolation.ExecutionResult{
			Success:    false,
			Error:      fmt.Sprintf(format, args...),
			ErrorType:  isolation.ErrorTypeRuntime,
			DurationMS: time.Since(started).Milliseconds(),
		}
	}
	if guest == nil {
		return fail("project sandbox is not available")
	}
	if req.Revision == nil {
		return fail("project revision is required")
	}
	function, ok := req.Revision.Manifest.FunctionByName(req.Function)
	if !ok {
		return fail("project function %q is not declared in revision %s", req.Function, req.Revision.ID)
	}

	files := make([]isolation.SourceFile, len(req.Revision.Manifest.Files))
	for i, file := range req.Revision.Manifest.Files {
		files[i] = isolation.SourceFile{
			Path: file.Path, Content: append([]byte(nil), file.Content...), Mode: file.Mode,
		}
	}
	result := fnexec.Dispatch(ctx, guest, r.config.GuestWorkDir, r.config.CleanupOnExit, isolation.ExecutionRequest{
		RequestID:     req.RequestID,
		FunctionID:    req.Revision.ID + ":" + function.Name,
		TenantID:      req.TenantID,
		CorrelationID: req.CorrelationID,
		TraceID:       req.TraceID,
		FunctionName:  function.Name,
		Runtime:       function.Runtime,
		Files:         files,
		Entrypoint:    function.Path,
		Export:        function.Export,
		Arguments:     req.Arguments,
		TimeoutMS:     req.TimeoutMS,
		MemoryMB:      req.MemoryMB,
		VCPUs:         req.VCPUs,
		NetworkMode:   req.NetworkMode,
		AllowedHosts:  append([]string(nil), req.AllowedHosts...),
	})
	result.DurationMS = time.Since(started).Milliseconds()
	return result
}
