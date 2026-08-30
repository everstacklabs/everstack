package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/projectruntime"
	"github.com/everstacklabs/everstack/internal/agents/revision"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/google/uuid"
)

// ProjectFunctionHandler exposes one export from a session-pinned revision.
type ProjectFunctionHandler struct {
	Runner     projectruntime.Runner
	Revision   *revision.Revision
	Function   revision.Function
	SandboxCtx *SandboxSessionContext
}

// NewProjectFunctionHandlers creates one synthetic handler per revision-local
// function. The handlers share the agent session's sandbox.
func NewProjectFunctionHandlers(
	runner projectruntime.Runner,
	rev *revision.Revision,
	sandboxCtx *SandboxSessionContext,
) []SyntheticToolHandler {
	if runner == nil || rev == nil || sandboxCtx == nil {
		return nil
	}
	handlers := make([]SyntheticToolHandler, 0, len(rev.Manifest.Functions))
	for _, function := range rev.Manifest.Functions {
		handlers = append(handlers, &ProjectFunctionHandler{
			Runner: runner, Revision: rev, Function: function, SandboxCtx: sandboxCtx,
		})
	}
	return handlers
}

func (h *ProjectFunctionHandler) Name() string { return h.Function.Name }

func (h *ProjectFunctionHandler) Definition() gw.ToolDefinition {
	description := h.Function.Description
	if description == "" {
		description = fmt.Sprintf("Run the %s project function from this agent revision.", h.Function.Name)
	}
	parameters := h.Function.Parameters
	if len(parameters) == 0 {
		parameters = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name: h.Function.Name, Description: description, Parameters: parameters,
		},
	}
}

func (h *ProjectFunctionHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if h.Runner == nil || h.Revision == nil || h.SandboxCtx == nil || h.SandboxCtx.Manager == nil {
		return "", fmt.Errorf("project function %q is not attached to a sandbox", h.Function.Name)
	}
	timeoutMS := h.SandboxCtx.Config.TimeoutSeconds * 1000
	if timeoutMS <= 0 {
		timeoutMS = 300_000
	}
	result := h.Runner.Run(ctx, &sandboxProjectGuest{ctx: h.SandboxCtx}, projectruntime.RunRequest{
		RequestID:    uuid.NewString(),
		TenantID:     h.SandboxCtx.TenantID,
		Revision:     h.Revision,
		Function:     h.Function.Name,
		Arguments:    args,
		TimeoutMS:    timeoutMS,
		MemoryMB:     int(h.SandboxCtx.Config.MemoryMB),
		VCPUs:        int(math.Ceil(h.SandboxCtx.Config.CPULimit)),
		NetworkMode:  projectNetworkMode(h.SandboxCtx.Config.NetworkMode),
		AllowedHosts: append([]string(nil), h.SandboxCtx.Config.AllowedHosts...),
	})
	if result == nil {
		return "", fmt.Errorf("project function %q returned no execution result", h.Function.Name)
	}
	if !result.Success {
		message := result.Error
		if message == "" {
			message = "execution failed"
		}
		return "", fmt.Errorf("project function %q: %s", h.Function.Name, message)
	}
	payload := map[string]interface{}{
		"result":      result.Result,
		"revision_id": h.Revision.ID,
		"function":    h.Function.Name,
		"duration_ms": result.DurationMS,
	}
	if result.Stdout != "" {
		payload["stdout"] = result.Stdout
	}
	if result.Stderr != "" {
		payload["stderr"] = result.Stderr
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode project function result: %w", err)
	}
	return string(encoded), nil
}

type sandboxProjectGuest struct {
	ctx        *SandboxSessionContext
	ensureOnce sync.Once
	ensureErr  error
}

func (g *sandboxProjectGuest) ensure(ctx context.Context) error {
	g.ensureOnce.Do(func() {
		_, g.ensureErr = ensureSandbox(ctx, g.ctx)
	})
	if g.ensureErr != nil {
		return fmt.Errorf("prepare project sandbox: %w", g.ensureErr)
	}
	return nil
}

func (g *sandboxProjectGuest) Exec(ctx context.Context, call fnexec.ExecCall) (*fnexec.ExecOutcome, error) {
	if err := g.ensure(ctx); err != nil {
		return nil, err
	}
	timeout := time.Duration(call.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	result, err := g.ctx.ExecWithRecovery(ctx, sandbox.ExecRequest{
		Command: call.Command, WorkDir: call.WorkDir, Env: call.Env, Timeout: timeout,
	})
	if err != nil {
		return nil, err
	}
	return &fnexec.ExecOutcome{
		Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode, TimedOut: result.TimedOut,
	}, nil
}

func (g *sandboxProjectGuest) WriteFile(ctx context.Context, filePath string, content []byte) error {
	if err := g.ensure(ctx); err != nil {
		return err
	}
	return g.ctx.WriteFileWithRecovery(ctx, filePath, content)
}

func projectNetworkMode(mode string) isolation.NetworkMode {
	switch mode {
	case "allow":
		return isolation.NetworkAllow
	case "whitelist":
		return isolation.NetworkWhitelist
	default:
		return isolation.NetworkDeny
	}
}
