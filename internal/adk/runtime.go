// Package adk runs Google ADK (Agent Development Kit) agents inside Everstack
// sandboxes. It is the "author in ADK, run on Everstack" path: a developer
// brings ADK agent code, and Everstack provisions an isolated sandbox, installs
// google-adk, runs the agent against an input, captures the output, and tears
// the sandbox down - wrapping the run in Everstack's isolation and (in future)
// observability/billing.
//
// The orchestration here is backend-agnostic and unit-tested against a fake
// Sandbox. The production Sandbox (sandbox_adapter.go) drives the real
// Everstack SandboxManager.
//
// NOTE: the generated Python harness targets ADK's InMemoryRunner API and is
// best-effort; the exact ADK runner surface should be validated against the
// installed google-adk version during a live run. The Everstack-side
// orchestration (provision → write → install → run → teardown, with guaranteed
// cleanup) is the tested, load-bearing part.
package adk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
)

// Sandbox is the minimal sandbox capability the ADK runtime needs. It is an
// interface so the orchestration is testable without a real sandbox backend.
type Sandbox interface {
	// Create provisions a sandbox for the tenant and returns an opaque handle.
	Create(ctx context.Context, tenantID string) (handle string, err error)
	// WriteFile writes content to path inside the sandbox.
	WriteFile(ctx context.Context, handle, path, content string) error
	// Exec runs a command and returns stdout, stderr, exit code.
	Exec(ctx context.Context, handle string, cmd []string) (stdout, stderr string, exitCode int, err error)
	// Destroy tears the sandbox down. Must be idempotent.
	Destroy(ctx context.Context, handle string) error
}

// Runtime runs ADK agents in Everstack sandboxes.
type Runtime struct {
	sb Sandbox
}

// New constructs a Runtime over the given sandbox backend.
func New(sb Sandbox) *Runtime { return &Runtime{sb: sb} }

const (
	workDir       = "/workspace"
	agentPath     = workDir + "/agent.py"
	harnessPath   = workDir + "/_everstack_adk_runner.py"
	inputPath     = workDir + "/_input.txt"
	defaultModule = "agent"
)

// RunRequest describes one ADK agent run.
type RunRequest struct {
	TenantID  string
	AgentCode string   // Python defining `root_agent` (an ADK agent)
	Input     string   // user message to send the agent
	Packages  []string // extra pip packages (google-adk is always installed)
}

// RunResult is the outcome of a run.
type RunResult struct {
	Output   string // the agent's final text (stdout of the harness)
	ExitCode int
	Logs     string // stderr (install + run diagnostics)
}

// Run provisions a sandbox, installs ADK, runs the agent against the input, and
// returns its output. The sandbox is always destroyed before returning, even on
// error - an ADK run must never leak a sandbox.
func (rt *Runtime) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if rt == nil || rt.sb == nil {
		return nil, fmt.Errorf("adk runtime not configured")
	}
	if req.TenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	if strings.TrimSpace(req.AgentCode) == "" {
		return nil, fmt.Errorf("agent code is required")
	}

	ctx, span := telemetry.StartHarnessRunSpan(ctx, req.TenantID)
	defer span.End()
	span.SetAttributes(attribute.Int(attrs.HarnessPackages, len(req.Packages)))

	handle, err := rt.sb.Create(ctx, req.TenantID)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, fmt.Errorf("adk: provision sandbox: %w", err)
	}
	// Guaranteed teardown. Use a fresh context so cleanup runs even if ctx is
	// already cancelled/timed-out.
	defer func() { _ = rt.sb.Destroy(context.Background(), handle) }()

	for path, content := range map[string]string{
		agentPath:   req.AgentCode,
		harnessPath: harnessScript,
		inputPath:   req.Input,
	} {
		if err := rt.sb.WriteFile(ctx, handle, path, content); err != nil {
			telemetry.RecordError(span, err)
			return nil, fmt.Errorf("adk: write %s: %w", path, err)
		}
	}

	// Install google-adk plus any extra packages.
	pkgs := append([]string{"google-adk"}, req.Packages...)
	installCmd := append([]string{"pip", "install", "--quiet", "--disable-pip-version-check"}, pkgs...)
	if _, stderr, code, err := rt.sb.Exec(ctx, handle, installCmd); err != nil || code != 0 {
		instErr := fmt.Errorf("adk: install failed (exit %d): %s", code, firstLines(stderr, 20))
		telemetry.RecordError(span, instErr)
		return nil, instErr
	}

	stdout, stderr, code, err := rt.sb.Exec(ctx, handle, []string{"python", harnessPath})
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, fmt.Errorf("adk: run failed: %w", err)
	}
	res := &RunResult{Output: strings.TrimRight(stdout, "\n"), ExitCode: code, Logs: stderr}
	span.SetAttributes(attribute.Int(attrs.SandboxExitCode, code))
	if code != 0 {
		exitErr := fmt.Errorf("adk: agent exited %d: %s", code, firstLines(stderr, 20))
		telemetry.RecordError(span, exitErr)
		return res, exitErr
	}
	return res, nil
}

// harnessScript imports the user's root_agent and runs it against the input via
// ADK's InMemoryRunner, printing the final text response.
const harnessScript = `import asyncio, sys

try:
    from agent import root_agent
except Exception as e:  # noqa
    sys.stderr.write("failed to import root_agent from agent.py: %r\n" % (e,))
    sys.exit(2)

from google.adk.runners import InMemoryRunner
from google.genai import types

async def main():
    with open("` + inputPath + `", "r") as f:
        text = f.read()
    runner = InMemoryRunner(agent=root_agent, app_name="everstack")
    session = await runner.session_service.create_session(app_name="everstack", user_id="everstack")
    content = types.Content(role="user", parts=[types.Part(text=text)])
    final = ""
    async for event in runner.run_async(user_id="everstack", session_id=session.id, new_message=content):
        if getattr(event, "content", None) and event.content.parts:
            for p in event.content.parts:
                if getattr(p, "text", None):
                    final = p.text
    sys.stdout.write(final)

asyncio.run(main())
`

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func randHandle() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "adk-000000000000"
	}
	return "adk-" + hex.EncodeToString(b)
}
