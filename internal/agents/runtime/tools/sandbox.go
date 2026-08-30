package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// SandboxSessionContext provides shared state for all sandbox tool handlers
// within a single agent session.
type SandboxSessionContext struct {
	Manager                 *sandbox.SandboxManager
	SessionID               string
	TenantID                string
	Config                  sandbox.SandboxConfig
	SessionStartedAt        time.Time
	ExecutionMode           string
	PersistenceMode         string
	AllowedWorkingDirectory string
	Emitter                 *agentrt.Emitter
	PortExposureBaseDomain  string // e.g., "sandbox.run"
	PortExposureTLSEnabled  bool
	PortExposureListenPort  string // e.g., "8443"; empty = default 80/443

	// AgentID is set for persistent trooper routing — when Config.Persistent
	// is true, the sandbox is keyed by agent ID instead of session ID.
	AgentID string

	// LinkedSessionID, when non-empty, links this agent to an existing sandbox
	// instead of creating a new one. The sandbox is identified by its session ID.
	LinkedSessionID string

	// Installed skills to be written to the sandbox filesystem.
	// Populated from agent config at session build time.
	SkillEntries []agentrt.SkillEntry

	// Channel context — set when the session originates from a messaging
	// platform. Used by the cron handler to configure channel notifications.
	ChannelConfigID string
	ChannelRef      string
	ThreadRef       string

	// portURLs tracks URLs from explicitly exposed sandbox ports.
	// Written by sandbox_expose_port and sandbox_shell auto-expose;
	// read by the loop to announce "Available service URL(s)".
	portURLsMu sync.Mutex
	portURLs   map[string]struct{}

	// skillsWritten tracks whether skills have been written to the sandbox.
	skillsWrittenMu sync.Mutex
	skillsWritten   bool

	readyMetricMu      sync.Mutex
	readyMetricEmitted bool
}

// CloneForPersistentChild returns a fresh SandboxSessionContext for a
// spawned persistent child agent so its sandbox tools route to the
// child's OWN trooper sandbox (keyed by childAgentID at session id
// "trp-<childAgentID>") instead of inheriting the parent's. Manager,
// TenantID, port-exposure config, emitter etc. carry over so the
// child's sandbox sees the same backend + URLs as the parent's.
//
// Per-spawn state (skillsWritten, readyMetric, portURLs) is reset so
// the child writes its own skills, emits its own ready metric, and
// tracks its own port URLs.
func (sctx *SandboxSessionContext) CloneForPersistentChild(childAgentID string) *SandboxSessionContext {
	if sctx == nil || childAgentID == "" {
		return nil
	}
	cloned := &SandboxSessionContext{
		Manager:                 sctx.Manager,
		SessionID:               "trp-" + childAgentID,
		TenantID:                sctx.TenantID,
		Config:                  sctx.Config,
		SessionStartedAt:        sctx.SessionStartedAt,
		ExecutionMode:           sctx.ExecutionMode,
		PersistenceMode:         sctx.PersistenceMode,
		AllowedWorkingDirectory: sctx.AllowedWorkingDirectory,
		Emitter:                 sctx.Emitter,
		PortExposureBaseDomain:  sctx.PortExposureBaseDomain,
		PortExposureTLSEnabled:  sctx.PortExposureTLSEnabled,
		PortExposureListenPort:  sctx.PortExposureListenPort,
		AgentID:                 childAgentID,
		SkillEntries:            sctx.SkillEntries,
		ChannelConfigID:         sctx.ChannelConfigID,
		ChannelRef:              sctx.ChannelRef,
		ThreadRef:               sctx.ThreadRef,
	}
	cloned.Config.Persistent = true
	cloned.Config.AgentID = childAgentID
	return cloned
}

// deadGuestSignatures are substrings that mean "the sandbox VM is gone /
// the guest agent is unreachable" — the recoverable condition every
// sandbox tool should respond to by reviving the sandbox and retrying,
// rather than surfacing a raw failure. Mirrors the backend's
// isDeadGuestSnapshotError (manager_executor.go) so the tool layer reacts
// to the same signal the lifecycle layer does. The typed
// sandbox.ErrSandboxNotRunning is handled separately by callers via
// errors.Is; this covers the wrapped/stringified cases (e.g. the browser
// sidecar probe failing because vsock is gone).
var deadGuestSignatures = []string{
	"vsock.sock",
	"no such file",
	"failed to connect to guest agent",
	"connection refused",
	"use of closed network connection",
	"sidecar not ready",
	"not running",
	"VM not found",
}

// isDeadGuestToolError reports whether err indicates the sandbox VM died
// or its guest agent is unreachable — the recoverable condition. True for
// the typed ErrSandboxNotRunning and for any error whose message carries a
// dead-guest signature. Shared by all sandbox tools so browser, exec,
// file, port, and LSP handlers react to a dead sandbox uniformly.
func isDeadGuestToolError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sandbox.ErrSandboxNotRunning) {
		return true
	}
	msg := err.Error()
	for _, sig := range deadGuestSignatures {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

// recoverSandbox attempts to recover a sandbox that has died.
// For persistent troopers, it revives the sandbox from its snapshot to
// preserve /workspace state. For ephemeral sandboxes, it creates a fresh
// container via ensureSandbox.
func recoverSandbox(ctx context.Context, sctx *SandboxSessionContext) (*sandbox.Instance, error) {
	logger.WithFields(
		"session_id", sctx.SessionID,
		"agent_id", sctx.AgentID,
		"persistent", sctx.Config.Persistent,
	).Warn("sandbox_tools: sandbox died mid-session, attempting automatic recovery")

	if sctx.Emitter != nil {
		sctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxError,
			SessionID: sctx.SessionID,
			Timestamp: time.Now(),
			Error:     "sandbox not responding — recovering",
		})
	}

	// For persistent troopers, use GetOrCreateTrooper which will attempt
	// revive from snapshot first, preserving /workspace workspace state.
	// We never do a bare "recreate from scratch" for persistent agents.
	inst, err := ensureSandbox(ctx, sctx)
	if err != nil {
		return nil, fmt.Errorf("sandbox recovery failed: %w", err)
	}

	logger.WithFields(
		"session_id", sctx.SessionID,
		"sandbox_id", inst.ID,
		"persistent", sctx.Config.Persistent,
	).Info("sandbox_tools: sandbox recovered successfully")

	if sctx.Emitter != nil {
		sctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxReady,
			SessionID: sctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"recovered": true,
			},
		})
	}
	return inst, nil
}

// ExecWithRecovery runs Manager.Exec and, if it fails because the sandbox
// VM died (dead-guest signature), revives the sandbox once and retries.
// Tool handlers should prefer this over calling Manager.Exec directly so a
// sandbox that died mid-session auto-recovers instead of failing raw —
// the same behaviour exec/file/port handlers already had inline, now
// shared so every tool reacts to a dead guest uniformly.
func (c *SandboxSessionContext) ExecWithRecovery(ctx context.Context, req sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	res, err := c.Manager.Exec(ctx, c.SessionID, req)
	if err == nil || !isDeadGuestToolError(err) {
		return res, err
	}
	if _, recErr := recoverSandbox(ctx, c); recErr != nil {
		return nil, fmt.Errorf("%w (sandbox recovery also failed: %v)", err, recErr)
	}
	return c.Manager.Exec(ctx, c.SessionID, req)
}

// ReadFileWithRecovery is ReadFile with the same dead-guest revive-and-retry.
func (c *SandboxSessionContext) ReadFileWithRecovery(ctx context.Context, path string) ([]byte, error) {
	data, err := c.Manager.ReadFile(ctx, c.SessionID, path)
	if err == nil || !isDeadGuestToolError(err) {
		return data, err
	}
	if _, recErr := recoverSandbox(ctx, c); recErr != nil {
		return nil, fmt.Errorf("%w (sandbox recovery also failed: %v)", err, recErr)
	}
	return c.Manager.ReadFile(ctx, c.SessionID, path)
}

// WriteFileWithRecovery is WriteFile with the same dead-guest revive-and-retry.
func (c *SandboxSessionContext) WriteFileWithRecovery(ctx context.Context, path string, content []byte) error {
	err := c.Manager.WriteFile(ctx, c.SessionID, path, content)
	if err == nil || !isDeadGuestToolError(err) {
		return err
	}
	if _, recErr := recoverSandbox(ctx, c); recErr != nil {
		return fmt.Errorf("%w (sandbox recovery also failed: %v)", err, recErr)
	}
	return c.Manager.WriteFile(ctx, c.SessionID, path, content)
}

// AddPortURL records a sandbox port exposure URL.
func (c *SandboxSessionContext) AddPortURL(url string) {
	c.portURLsMu.Lock()
	defer c.portURLsMu.Unlock()
	if c.portURLs == nil {
		c.portURLs = make(map[string]struct{})
	}
	c.portURLs[url] = struct{}{}
}

// GetPortURLs returns all recorded port URLs in sorted order.
func (c *SandboxSessionContext) GetPortURLs() []string {
	c.portURLsMu.Lock()
	defer c.portURLsMu.Unlock()
	if len(c.portURLs) == 0 {
		return nil
	}
	urls := make([]string, 0, len(c.portURLs))
	for u := range c.portURLs {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls
}

// WireSandboxEmitter sets the emitter on the SandboxSessionContext via the interceptor.
// Called after PrepareSession returns the emitter.
func WireSandboxEmitter(interceptor *ToolInterceptor, emitter *agentrt.Emitter) {
	for _, handler := range interceptor.Handlers {
		if se, ok := handler.(sandboxEmitterWirer); ok {
			se.wireEmitter(emitter)
			return // All sandbox handlers share the same context, only need to set once
		}
	}
}

// sandboxEmitterWirer is an internal interface for wiring the emitter.
type sandboxEmitterWirer interface {
	wireEmitter(emitter *agentrt.Emitter)
}

// NewSandboxHandlers creates the sandbox synthetic tool handlers.
func NewSandboxHandlers(ctx *SandboxSessionContext) []SyntheticToolHandler {
	handlers := []SyntheticToolHandler{
		&sandboxExecuteHandler{ctx: ctx},
		&sandboxShellHandler{ctx: ctx},
		&sandboxWriteFileHandler{ctx: ctx},
		&sandboxReadFileHandler{ctx: ctx},
		&sandboxListFilesHandler{ctx: ctx},
		&sandboxExposePortHandler{ctx: ctx},
		&sandboxUnexposePortHandler{ctx: ctx},
		&sandboxListPortsHandler{ctx: ctx},
		// File tools (edit, grep, glob, patch)
		&sandboxEditHandler{ctx: ctx},
		&sandboxGrepHandler{ctx: ctx},
		&sandboxGlobHandler{ctx: ctx},
		&sandboxPatchHandler{ctx: ctx},
	}

	// Template tools are only available for ephemeral sessions. Persistent
	// agents always use their provisioned sandbox — the LLM should not have
	// the power to destroy/recreate the sandbox environment.
	if !ctx.Config.Persistent {
		handlers = append(handlers, &sandboxListTemplatesHandler{ctx: ctx})
		handlers = append(handlers, &sandboxSetTemplateHandler{ctx: ctx})
	}

	// If a repo is already configured on the session (from agent config / GitHub app
	// installation), do not expose sandbox_git_clone. This keeps the configured repo
	// as the single source of truth and prevents model-side overrides.
	if strings.TrimSpace(ctx.Config.GitRepoURL) == "" {
		handlers = append(handlers, &sandboxGitCloneHandler{ctx: ctx})
	}

	// Cron scheduling — always available when sandbox is enabled.
	// Channel info is passed through so crons can notify the originating channel.
	handlers = append(handlers, &SandboxCronHandler{
		Ctx:             ctx,
		ChannelConfigID: ctx.ChannelConfigID,
		ChannelRef:      ctx.ChannelRef,
		ThreadRef:       ctx.ThreadRef,
	})

	// Filter by configured tools if specified
	if len(ctx.Config.Tools) > 0 {
		allowed := make(map[string]bool, len(ctx.Config.Tools))
		for _, t := range ctx.Config.Tools {
			allowed[t] = true
		}
		var filtered []SyntheticToolHandler
		for _, h := range handlers {
			if allowed[h.Name()] {
				filtered = append(filtered, h)
			}
		}
		return filtered
	}

	return handlers
}

// ensureSandbox lazily provisions the sandbox on first tool call.
// For persistent troopers (Config.Persistent && AgentID != ""), routes to
// GetOrCreateTrooper which shares a single sandbox across sessions.
func ensureSandbox(ctx context.Context, sctx *SandboxSessionContext) (*sandbox.Instance, error) {
	var inst *sandbox.Instance
	var err error

	if sctx.LinkedSessionID != "" {
		// Link to an existing sandbox instead of creating a new one
		inst, err = sctx.Manager.GetLinked(ctx, sctx.LinkedSessionID, sctx.SessionID, sctx.TenantID)
	} else if sctx.Config.Persistent && sctx.AgentID != "" {
		// Only gate on the feature flag when creating a NEW trooper.
		// If the trooper already exists (was provisioned earlier), allow usage
		// even if the license state is temporarily unavailable.
		if !sctx.Manager.TrooperExists(sctx.AgentID) && !sctx.Manager.IsTrooperFeatureEnabled(sctx.TenantID) {
			return nil, fmt.Errorf("persistent troopers are a cloud-only feature; upgrade at https://everstack.dev/pricing")
		}
		inst, err = sctx.Manager.GetOrCreateTrooper(ctx, sctx.AgentID, sctx.SessionID, sctx.TenantID, sctx.Config)
	} else {
		inst, err = sctx.Manager.GetOrCreate(ctx, sctx.SessionID, sctx.TenantID, sctx.Config)
	}
	if err != nil {
		if sctx.Emitter != nil {
			sctx.Emitter.Emit(agentrt.Event{
				Type:      agentrt.EventSandboxError,
				SessionID: sctx.SessionID,
				Timestamp: time.Now(),
				Error:     err.Error(),
			})
		}
		return nil, err
	}

	if sctx.Emitter != nil {
		data := map[string]interface{}{
			"image":   inst.Config.Image,
			"backend": inst.Backend,
		}
		if sctx.ExecutionMode != "" {
			data["execution_mode"] = sctx.ExecutionMode
		}
		if sctx.PersistenceMode != "" {
			data["persistence_mode"] = sctx.PersistenceMode
		}
		if !sctx.SessionStartedAt.IsZero() {
			sctx.readyMetricMu.Lock()
			if !sctx.readyMetricEmitted {
				data["session_ready_ms"] = time.Since(sctx.SessionStartedAt).Milliseconds()
				sctx.readyMetricEmitted = true
			}
			sctx.readyMetricMu.Unlock()
		}
		sctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxReady,
			SessionID: sctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data:      data,
		})
	}

	// Write skills to sandbox filesystem on first provision
	sctx.skillsWrittenMu.Lock()
	needsSkillWrite := !sctx.skillsWritten && len(sctx.SkillEntries) > 0
	if needsSkillWrite {
		sctx.skillsWritten = true
	}
	sctx.skillsWrittenMu.Unlock()
	if needsSkillWrite {
		WriteSkillsToSandbox(ctx, sctx)
	}

	return inst, nil
}

// WriteSkillsToSandbox writes all installed skills to /skills/{name}/SKILL.md
// inside the sandbox. Called once after sandbox creation/provisioning.
// Errors are logged but not fatal — the use_skill handler falls back to
// in-memory content if the file read fails.
func WriteSkillsToSandbox(ctx context.Context, sctx *SandboxSessionContext) {
	if len(sctx.SkillEntries) == 0 || sctx.Manager == nil {
		return
	}

	for _, skill := range sctx.SkillEntries {
		if skill.Content == "" {
			continue
		}
		skillPath := fmt.Sprintf("/skills/%s/SKILL.md", skill.Name)
		if err := sctx.Manager.WriteFile(ctx, sctx.SessionID, skillPath, []byte(skill.Content)); err != nil {
			logger.WithFields(
				"skill_name", skill.Name,
				"path", skillPath,
				"session_id", sctx.SessionID,
				"error", err.Error(),
			).Warn("sandbox_tools: failed to write skill to sandbox")
		}
	}
}

func ensureRepoReadyIfNeeded(ctx context.Context, sctx *SandboxSessionContext, inst *sandbox.Instance, needed bool) (*sandbox.Instance, error) {
	if !needed {
		return inst, nil
	}
	if sctx.Manager == nil {
		return inst, fmt.Errorf("sandbox manager not available")
	}
	repoReadyInst, cloneResult, err := sctx.Manager.EnsureRepoReady(ctx, sctx.SessionID)
	if err != nil {
		if sctx.Emitter != nil {
			sctx.Emitter.Emit(agentrt.Event{
				Type:      agentrt.EventSandboxError,
				SessionID: sctx.SessionID,
				Timestamp: time.Now(),
				Error:     err.Error(),
			})
		}
		return inst, err
	}
	if cloneResult != nil && cloneResult.Cloned && sctx.Emitter != nil {
		sctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxGitClone,
			SessionID: sctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: repoReadyInst.ID,
			Data: map[string]interface{}{
				"clone_duration_ms": cloneResult.DurationMs,
				"clone_bytes_total": cloneResult.SizeBytes,
				"clone_strategy":    cloneResult.Strategy,
			},
		})
	}
	return repoReadyInst, nil
}

// ============================================================================
// sandbox_execute — runs code in a specific language
// ============================================================================

type sandboxExecuteHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxExecuteHandler) Name() string { return "sandbox_execute" }

func (h *sandboxExecuteHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxShellHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxWriteFileHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxReadFileHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxListFilesHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxExecuteHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_execute",
			Description: "Execute code in a sandboxed environment. The code runs in an isolated container with no access to the host system. The sandbox persists across calls within this session, so files written in one call are available in the next. For long-running bash server commands (e.g., `node index.js`, `npm run dev`), prefer `sandbox_shell` or ensure the command runs detached so the tool call does not block until timeout.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"language": map[string]interface{}{
						"type":        "string",
						"description": "Programming language to execute. Supported: python, javascript, typescript, ruby, go, bash, rust.",
						"enum":        []string{"python", "javascript", "typescript", "ruby", "go", "bash", "rust"},
					},
					"code": map[string]interface{}{
						"type":        "string",
						"description": "The code to execute.",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum execution time in seconds (default: 30, max: 300).",
					},
				},
				"required": []string{"language", "code"},
			},
		},
	}
}

func (h *sandboxExecuteHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	language, _ := args["language"].(string)
	code, _ := args["code"].(string)
	if language == "" || code == "" {
		return "", fmt.Errorf("language and code are required")
	}
	if isManualGitCloneBlocked(h.ctx.Config, language, code) {
		repo := strings.TrimSpace(h.ctx.Config.GitRepoURL)
		branch := strings.TrimSpace(h.ctx.Config.GitBranch)
		return fmt.Sprintf("Repository is already provisioned for this session at /repo: %s (branch: %s). Do not run `git clone`; use /repo directly.", repo, branchOrDefault(branch)), nil
	}
	originalCode := code

	timeout := 30
	if t, ok := args["timeout_seconds"].(float64); ok && t > 0 && t <= 300 {
		timeout = int(t)
	}

	autoDetached := false
	logPath := ""
	lang := strings.ToLower(strings.TrimSpace(language))
	if (lang == "bash" || lang == "sh") && shouldAutoDetachDevServer(code) {
		autoDetached = true
		logPath = "/tmp/sandbox_exec_server.log"
		code = fmt.Sprintf(
			"nohup sh -lc '%s' >%s 2>&1 & echo $!",
			escapeForSingleQuotedShell(code),
			logPath,
		)
		if timeout > 20 {
			timeout = 20
		}
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	inst, err = ensureRepoReadyIfNeeded(ctx, h.ctx, inst, shouldEnsureRepoForScript(language, originalCode))
	if err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	// Write code to temp file
	ext, cmd := languageCommand(language)
	if cmd == nil {
		return fmt.Sprintf("Unsupported language: %s", language), nil
	}

	filePath := "/workspace/.sandbox_exec" + ext
	if err := h.ctx.Manager.WriteFile(ctx, h.ctx.SessionID, filePath, []byte(code)); err != nil {
		// Sandbox died — recover and retry the write
		if errors.Is(err, sandbox.ErrSandboxNotRunning) {
			inst, err = recoverSandbox(ctx, h.ctx)
			if err != nil {
				return fmt.Sprintf("Sandbox crashed and recovery failed: %s", err.Error()), nil
			}
			if err := h.ctx.Manager.WriteFile(ctx, h.ctx.SessionID, filePath, []byte(code)); err != nil {
				return fmt.Sprintf("Failed to write code file after recovery: %s", err.Error()), nil
			}
		} else {
			return fmt.Sprintf("Failed to write code file: %s", err.Error()), nil
		}
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxExec,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"language": language,
				"tool":     "sandbox_execute",
			},
		})
	}

	result, err := h.ctx.Manager.Exec(ctx, h.ctx.SessionID, sandbox.ExecRequest{
		Command: cmd,
		Timeout: time.Duration(timeout) * time.Second,
	})
	if err != nil {
		// Sandbox died during exec — recover and retry
		if errors.Is(err, sandbox.ErrSandboxNotRunning) {
			inst, err = recoverSandbox(ctx, h.ctx)
			if err != nil {
				return "", fmt.Errorf("sandbox crashed and recovery failed: %w", err)
			}
			// Re-write the code file and retry exec on the new sandbox
			if err := h.ctx.Manager.WriteFile(ctx, h.ctx.SessionID, filePath, []byte(code)); err != nil {
				return "", fmt.Errorf("failed to write code file after recovery: %w", err)
			}
			result, err = h.ctx.Manager.Exec(ctx, h.ctx.SessionID, sandbox.ExecRequest{
				Command: cmd,
				Timeout: time.Duration(timeout) * time.Second,
			})
			if err != nil {
				return "", fmt.Errorf("execution failed after recovery: %w", err)
			}
		} else {
			return "", fmt.Errorf("execution failed: %w", err)
		}
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:              agentrt.EventSandboxResult,
			SessionID:         h.ctx.SessionID,
			Timestamp:         time.Now(),
			SandboxID:         inst.ID,
			SandboxExitCode:   result.ExitCode,
			SandboxDurationMs: result.DurationMs,
		})
	}

	if result.TimedOut || result.ExitCode != 0 {
		return "", fmt.Errorf("%s", formatExecResult(result))
	}

	if autoDetached {
		pid := strings.TrimSpace(result.Stdout)
		if pid == "" {
			pid = "unknown"
		}
		sh := &sandboxShellHandler{ctx: h.ctx}
		autoExposeSummary := sh.tryAutoExposePort(ctx, inst, originalCode)
		autoExposeBlock := ""
		if autoExposeSummary != "" {
			autoExposeBlock = "\n" + autoExposeSummary + "\n"
		}
		return fmt.Sprintf(
			"Started server script in background.\nPID: %s\nLog: %s%s\n%s",
			pid,
			logPath,
			autoExposeBlock,
			formatExecResult(result),
		), nil
	}

	return formatExecResult(result), nil
}

// ============================================================================
// sandbox_shell — runs a shell command directly
// ============================================================================

type sandboxShellHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxShellHandler) Name() string { return "sandbox_shell" }

func (h *sandboxShellHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_shell",
			Description: "Execute a shell command in the sandbox environment. Use this for running CLI tools, installing packages, or file operations. For frontend apps, always use Vite (never create-react-app). When starting Vite with npm, pass args after `--` (e.g., `npm run dev -- --host 0.0.0.0 --port 5173 --strictPort`). Long-lived server commands (`npm run dev`, `npm start`, `node server.js`, etc.) are treated as background workloads and should be detached (`nohup ... &`) so tool calls do not timeout. Avoid killing all Vite processes globally; in multi-app sandboxes, track app-local PID/port files and only restart that app. For Node backends in Vite projects (`package.json` has `type: module`), use ESM `import` syntax or a `.cjs` file instead of `require` in `.js`.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to execute.",
					},
					"working_directory": map[string]interface{}{
						"type":        "string",
						"description": "Working directory for the command (default: /workspace).",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum execution time in seconds (default: 30, max: 300).",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (h *sandboxShellHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	if isManualGitCloneBlocked(h.ctx.Config, "bash", command) {
		repo := strings.TrimSpace(h.ctx.Config.GitRepoURL)
		branch := strings.TrimSpace(h.ctx.Config.GitBranch)
		return fmt.Sprintf("Repository is already provisioned for this session at /repo: %s (branch: %s). Do not run `git clone`; use /repo directly.", repo, branchOrDefault(branch)), nil
	}
	originalCommand := command
	serverLikeCommand := isLongRunningServerCommand(originalCommand)
	userDetachedCommand := isDetachedShellCommand(originalCommand)

	workDir, _ := args["working_directory"].(string)
	if workDir == "" {
		workDir = defaultWorkingDirectory(h.ctx.AllowedWorkingDirectory)
	}
	if errMsg := validateReadablePathWithConstraint(workDir, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}

	timeout := 30
	if t, ok := args["timeout_seconds"].(float64); ok && t > 0 && t <= 300 {
		timeout = int(t)
	}

	// When the working directory is a subdirectory of /workspace, inline mkdir -p
	// and cd into the shell command. Docker exec's WorkingDir requires the directory
	// to already exist, but the trooper tmpfs starts empty so nested paths like
	// /workspace/myapp/src won't exist yet.
	// Check auto-detach on the original command BEFORE prepending mkdir/cd,
	// otherwise directory names like "test-vite-app" trigger false positives.
	autoDetached := false
	logPath := ""
	shouldDetach := shouldAutoDetachDevServer(command)

	if workDir != "/workspace" && strings.HasPrefix(workDir, "/workspace/") {
		command = fmt.Sprintf("mkdir -p '%s' && cd '%s' && %s", workDir, workDir, command)
		workDir = "/workspace"
	}

	if shouldDetach {
		autoDetached = true
		logPath = "/tmp/sandbox_dev_server.log"
		command = fmt.Sprintf(
			"nohup sh -lc '%s' >%s 2>&1 & echo $!",
			escapeForSingleQuotedShell(command),
			logPath,
		)
		// Detached command returns immediately and should not run for full tool timeout.
		if timeout > 20 {
			timeout = 20
		}
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	inst, err = ensureRepoReadyIfNeeded(ctx, h.ctx, inst, shouldEnsureRepoForShellCommand(originalCommand, workDir))
	if err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxExec,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"command": truncateStr(command, 200),
				"tool":    "sandbox_shell",
			},
		})
	}

	execReq := sandbox.ExecRequest{
		Command: []string{"sh", "-c", command},
		WorkDir: workDir,
		Timeout: time.Duration(timeout) * time.Second,
	}
	result, err := h.ctx.Manager.Exec(ctx, h.ctx.SessionID, execReq)
	if err != nil {
		// Sandbox died — recover and retry
		if errors.Is(err, sandbox.ErrSandboxNotRunning) {
			inst, err = recoverSandbox(ctx, h.ctx)
			if err != nil {
				return "", fmt.Errorf("sandbox crashed and recovery failed: %w", err)
			}
			result, err = h.ctx.Manager.Exec(ctx, h.ctx.SessionID, execReq)
			if err != nil {
				return "", fmt.Errorf("shell execution failed after recovery: %w", err)
			}
		} else {
			return "", fmt.Errorf("shell execution failed: %w", err)
		}
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:              agentrt.EventSandboxResult,
			SessionID:         h.ctx.SessionID,
			Timestamp:         time.Now(),
			SandboxID:         inst.ID,
			SandboxExitCode:   result.ExitCode,
			SandboxDurationMs: result.DurationMs,
		})
	}

	if autoDetached || (serverLikeCommand && userDetachedCommand && result.ExitCode == 0) {
		pid := strings.TrimSpace(result.Stdout)
		if pid == "" {
			pid = "unknown"
		}
		if logPath == "" {
			logPath = extractLogPathFromCommand(originalCommand)
		}
		if logPath == "" {
			logPath = "(not detected)"
		}
		autoExposeSummary := h.tryAutoExposePort(ctx, inst, originalCommand)
		autoExposeBlock := ""
		if autoExposeSummary != "" {
			autoExposeBlock = "\n" + autoExposeSummary + "\n"
		}
		statusLine := "Started dev server in background."
		if !autoDetached && userDetachedCommand {
			statusLine = "Detected already-detached server command."
		}
		return fmt.Sprintf(
			"%s\nPID: %s\nLog: %s\nOriginal command: %s%s\n%s",
			statusLine,
			pid,
			logPath,
			originalCommand,
			autoExposeBlock,
			formatShellExecResult(command, result),
		), nil
	}

	if result.TimedOut || result.ExitCode != 0 {
		return "", fmt.Errorf("%s", formatShellExecResult(originalCommand, result))
	}

	return formatShellExecResult(originalCommand, result), nil
}

// ============================================================================
// sandbox_write_file — writes a file to the sandbox
// ============================================================================

type sandboxWriteFileHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxWriteFileHandler) Name() string { return "sandbox_write_file" }

func (h *sandboxWriteFileHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_write_file",
			Description: "Write content to a file in the sandbox environment. The file will be created or overwritten.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to write to (e.g., /workspace/script.py).",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Content to write to the file.",
					},
					"encoding": map[string]interface{}{
						"type":        "string",
						"description": "Content encoding: 'utf-8' (default) or 'base64'.",
						"enum":        []string{"utf-8", "base64"},
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

func (h *sandboxWriteFileHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" || content == "" {
		return "", fmt.Errorf("path and content are required")
	}

	if errMsg := validateWritablePathWithConstraint(path, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}

	encoding, _ := args["encoding"].(string)
	var data []byte
	if encoding == "base64" {
		var err error
		data, err = base64.StdEncoding.DecodeString(content)
		if err != nil {
			return fmt.Sprintf("Invalid base64 content: %s", err.Error()), nil
		}
	} else {
		data = []byte(content)
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	if _, err := ensureRepoReadyIfNeeded(ctx, h.ctx, inst, pathRequiresRepo(path)); err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	if err := h.ctx.Manager.WriteFile(ctx, h.ctx.SessionID, path, data); err != nil {
		if errors.Is(err, sandbox.ErrSandboxNotRunning) {
			if _, recErr := recoverSandbox(ctx, h.ctx); recErr != nil {
				return fmt.Sprintf("Sandbox crashed and recovery failed: %s", recErr.Error()), nil
			}
			if err := h.ctx.Manager.WriteFile(ctx, h.ctx.SessionID, path, data); err != nil {
				return fmt.Sprintf("Failed to write file after recovery: %s", err.Error()), nil
			}
		} else {
			return fmt.Sprintf("Failed to write file: %s", err.Error()), nil
		}
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(data), path), nil
}

// ============================================================================
// sandbox_read_file — reads a file from the sandbox
// ============================================================================

type sandboxReadFileHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxReadFileHandler) Name() string { return "sandbox_read_file" }

func (h *sandboxReadFileHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_read_file",
			Description: "Read the contents of a file from the sandbox environment.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to read from (e.g., /workspace/output.txt).",
					},
					"encoding": map[string]interface{}{
						"type":        "string",
						"description": "Response encoding: 'utf-8' (default) or 'base64'.",
						"enum":        []string{"utf-8", "base64"},
					},
					"max_size_bytes": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum bytes to read (default: 1048576 / 1MB).",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (h *sandboxReadFileHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if errMsg := validateReadablePathWithConstraint(path, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	if _, err := ensureRepoReadyIfNeeded(ctx, h.ctx, inst, pathRequiresRepo(path)); err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	data, err := h.ctx.Manager.ReadFile(ctx, h.ctx.SessionID, path)
	if err != nil {
		if errors.Is(err, sandbox.ErrSandboxNotRunning) {
			if _, recErr := recoverSandbox(ctx, h.ctx); recErr != nil {
				return fmt.Sprintf("Sandbox crashed and recovery failed: %s", recErr.Error()), nil
			}
			data, err = h.ctx.Manager.ReadFile(ctx, h.ctx.SessionID, path)
			if err != nil {
				return fmt.Sprintf("Failed to read file after recovery: %s", err.Error()), nil
			}
		} else {
			return fmt.Sprintf("Failed to read file: %s", err.Error()), nil
		}
	}

	encoding, _ := args["encoding"].(string)
	if encoding == "base64" {
		return base64.StdEncoding.EncodeToString(data), nil
	}

	return string(data), nil
}

// ============================================================================
// sandbox_list_files — lists directory contents in the sandbox
// ============================================================================

type sandboxListFilesHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxListFilesHandler) Name() string { return "sandbox_list_files" }

func (h *sandboxListFilesHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_list_files",
			Description: "List files and directories in the sandbox environment.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory path to list (default: /workspace).",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "List recursively (default: false).",
					},
				},
			},
		},
	}
}

func (h *sandboxListFilesHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = defaultWorkingDirectory(h.ctx.AllowedWorkingDirectory)
	}
	if errMsg := validateReadablePathWithConstraint(path, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}
	recursive, _ := args["recursive"].(bool)

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	if _, err := ensureRepoReadyIfNeeded(ctx, h.ctx, inst, pathRequiresRepo(path)); err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	var files []sandbox.FileInfo
	if recursive {
		files, err = h.ctx.Manager.SearchFiles(ctx, h.ctx.SessionID, path, "", 100)
	} else {
		files, err = h.ctx.Manager.ListFiles(ctx, h.ctx.SessionID, path)
	}
	if err != nil {
		if errors.Is(err, sandbox.ErrSandboxNotRunning) {
			if _, recErr := recoverSandbox(ctx, h.ctx); recErr != nil {
				return fmt.Sprintf("Sandbox crashed and recovery failed: %s", recErr.Error()), nil
			}
			if recursive {
				files, err = h.ctx.Manager.SearchFiles(ctx, h.ctx.SessionID, path, "", 100)
			} else {
				files, err = h.ctx.Manager.ListFiles(ctx, h.ctx.SessionID, path)
			}
			if err != nil {
				return fmt.Sprintf("Failed to list files after recovery: %s", err.Error()), nil
			}
		} else {
			return fmt.Sprintf("Failed to list files: %s", err.Error()), nil
		}
	}

	if len(files) == 0 {
		return "Directory is empty", nil
	}

	var sb strings.Builder
	for _, f := range files {
		typeStr := "file"
		if f.IsDir {
			typeStr = "dir"
		}
		// Strip /workspace prefix from displayed paths since it's the default sandbox directory.
		displayPath := strings.TrimPrefix(f.Path, "/workspace")
		if displayPath == "" {
			displayPath = "/"
		}
		fmt.Fprintf(&sb, "[%s] %s (%d bytes)\n", typeStr, displayPath, f.Size)
	}
	return sb.String(), nil
}

// ============================================================================
// Helpers
// ============================================================================

// languageCommand maps a language name to the file extension and interpreter command.
func languageCommand(language string) (string, []string) {
	switch strings.ToLower(language) {
	case "python":
		return ".py", []string{"python3", "/workspace/.sandbox_exec.py"}
	case "javascript", "node":
		return ".js", []string{"node", "/workspace/.sandbox_exec.js"}
	case "typescript":
		return ".ts", []string{"npx", "tsx", "/workspace/.sandbox_exec.ts"}
	case "ruby":
		return ".rb", []string{"ruby", "/workspace/.sandbox_exec.rb"}
	case "go":
		return ".go", []string{"go", "run", "/workspace/.sandbox_exec.go"}
	case "bash", "sh":
		return ".sh", []string{"bash", "/workspace/.sandbox_exec.sh"}
	case "rust":
		return ".rs", []string{"sh", "-c", "rustc /workspace/.sandbox_exec.rs -o /tmp/sandbox_exec && /tmp/sandbox_exec"}
	default:
		return "", nil
	}
}

func formatExecResult(result *sandbox.ExecResult) string {
	var sb strings.Builder

	if result.TimedOut {
		sb.WriteString("TIMED OUT\n\n")
	}

	sb.WriteString(fmt.Sprintf("Exit code: %d\n", result.ExitCode))
	sb.WriteString(fmt.Sprintf("Duration: %dms\n", result.DurationMs))

	if result.Stdout != "" {
		sb.WriteString("\n--- stdout ---\n")
		sb.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		sb.WriteString("\n--- stderr ---\n")
		sb.WriteString(result.Stderr)
	}

	return sb.String()
}

func formatShellExecResult(command string, result *sandbox.ExecResult) string {
	out := formatExecResult(result)
	hints := shellRemediationHints(command, result)
	if len(hints) == 0 {
		return out
	}

	var sb strings.Builder
	sb.WriteString(out)
	sb.WriteString("\n\n--- next fixes ---\n")
	for _, h := range hints {
		sb.WriteString("- ")
		sb.WriteString(h)
		sb.WriteString("\n")
	}
	return sb.String()
}

func shellRemediationHints(command string, result *sandbox.ExecResult) []string {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	cmd := strings.ToLower(command)
	var hints []string

	has := func(substr string) bool { return strings.Contains(text, substr) }
	add := func(h string) {
		for _, existing := range hints {
			if existing == h {
				return
			}
		}
		hints = append(hints, h)
	}

	if result.TimedOut &&
		(strings.Contains(cmd, "npm run dev") || strings.Contains(cmd, "vite") || strings.Contains(cmd, "next dev")) {
		add("Long-running dev servers should be started detached: `nohup npm run dev -- --host 0.0.0.0 --port <port> --strictPort >/tmp/dev.log 2>&1 &`.")
	}

	if has("network: use --host to expose") ||
		has("unknown cli config \"--host\"") ||
		has("is being parsed as a normal command line argument") {
		add("For npm scripts, pass args after `--` (example: `npm run dev -- --host 0.0.0.0 --port 5173 --strictPort`).")
	}

	if has("require is not defined in es module scope") || has("this file is being treated as an es module") {
		add("This project uses ESM (`type: module`). Use `import ...` syntax in `.js` files, or rename backend files using `require(...)` to `.cjs`.")
	}

	if has("err_module_not_found") && has("cannot find package") {
		re := regexp.MustCompile(`Cannot find package '([^']+)'`)
		if m := re.FindStringSubmatch(result.Stderr); len(m) == 2 {
			add(fmt.Sprintf("Missing dependency `%s`. Install it in the app directory (example: `cd /repo/<app> && npm i %s` or `cd /workspace/<app> && npm i %s`).", m[1], m[1], m[1]))
		} else {
			add("A dependency is missing. Install the missing package in the app directory with `npm i <package>`.")
		}
	}

	if has("missing script: \"test\"") {
		add("This project has no `test` script. Run `npm run` to inspect available scripts, or execute lint/build (`npm run lint`, `npm run build`) instead.")
	}

	if has("node-gyp") && has("find python") {
		add("Native module build is blocked because Python is unavailable in this sandbox image. Prefer a pure-JS alternative package, use a prebuilt-compatible Node version/package, or switch to a sandbox template/image with Python installed.")
	}

	if strings.Contains(cmd, "pkill -f vite") {
		add("Avoid `pkill -f vite` in multi-app sandboxes. Track per-app PID/port files and stop only that app.")
	}

	return hints
}

func shouldAutoDetachDevServer(command string) bool {
	return isLongRunningServerCommand(command) && !isDetachedShellCommand(command)
}

var gitCloneCommandPattern = regexp.MustCompile(`(?i)\bgit\s+clone\b`)
var gitCommandPattern = regexp.MustCompile(`(?i)\bgit\s+`)
var gitRepoCommandPattern = regexp.MustCompile(`(?i)\bgit\s+(status|add|commit|checkout|switch|branch|log|diff|merge|rebase|cherry-pick|reset|restore|stash|pull|push|fetch|rev-parse|show|tag|remote|submodule|worktree|clean|blame|bisect|describe)\b`)
var gitNoRepoCommandPattern = regexp.MustCompile(`(?i)\bgit\s+(--version|version|help)\b`)
var gitGlobalConfigPattern = regexp.MustCompile(`(?i)\bgit\s+config\s+--(global|system)\b`)

func isManualGitCloneBlocked(cfg sandbox.SandboxConfig, language, command string) bool {
	if strings.TrimSpace(cfg.GitRepoURL) == "" {
		return false
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang != "bash" && lang != "sh" {
		return false
	}
	return gitCloneCommandPattern.MatchString(command)
}

func pathRequiresRepo(path string) bool {
	clean := filepath.Clean(strings.TrimSpace(path))
	return clean == "/repo" || strings.HasPrefix(clean, "/repo/")
}

func shouldEnsureRepoForScript(language, code string) bool {
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang == "bash" || lang == "sh" || lang == "shell" {
		return shouldEnsureRepoForShellCommand(code, "")
	}
	return strings.Contains(code, "/repo")
}

func shouldEnsureRepoForShellCommand(command, workDir string) bool {
	if pathRequiresRepo(workDir) {
		return true
	}
	c := strings.TrimSpace(command)
	if c == "" {
		return false
	}
	if strings.Contains(c, "/repo") {
		return true
	}
	if !gitCommandPattern.MatchString(c) {
		return false
	}
	if gitRepoCommandPattern.MatchString(c) {
		return true
	}
	// Avoid cloning for git commands that are explicitly non-repository scoped.
	if gitNoRepoCommandPattern.MatchString(c) || gitGlobalConfigPattern.MatchString(c) {
		return false
	}
	// Conservative fallback: unknown git commands are treated as repo-scoped.
	return true
}

func escapeForSingleQuotedShell(s string) string {
	// Close quote, emit escaped single quote, reopen quote.
	return strings.ReplaceAll(s, "'", `'"'"'`)
}

func (h *sandboxShellHandler) tryAutoExposePort(ctx context.Context, inst *sandbox.Instance, command string) string {
	if inst.Config.NetworkMode == sandbox.NetworkDeny {
		return "Auto-expose skipped: sandbox network mode is 'deny'."
	}

	detected, detectErr := h.detectListeningPortsWithRetry(ctx)
	if detectErr != nil {
		return fmt.Sprintf("Auto-expose skipped: failed to detect listening ports: %v", detectErr)
	}
	if len(detected) == 0 {
		return "Auto-expose skipped: no listening ports detected yet."
	}

	mappings, listErr := h.ctx.Manager.ListExposedPorts(ctx, h.ctx.SessionID)
	if listErr != nil {
		return fmt.Sprintf("Auto-expose skipped: failed to list existing exposed ports: %v", listErr)
	}

	exposed := make(map[int]bool, len(mappings))
	for _, m := range mappings {
		exposed[m.Port] = true
	}

	candidate, ok := pickAutoExposeCandidate(command, detected, exposed)
	if !ok {
		if len(mappings) > 0 {
			return "Auto-expose skipped: detected ports are already exposed."
		}
		return "Auto-expose skipped: no suitable listening port found."
	}

	mapping, err := h.ctx.Manager.ExposePort(ctx, h.ctx.SessionID, candidate.Port, "tcp")
	if err != nil {
		return fmt.Sprintf("Auto-expose failed for port %d: %v", candidate.Port, err)
	}

	url := buildPortURL(h.ctx.PortExposureBaseDomain, h.ctx.PortExposureTLSEnabled, h.ctx.PortExposureListenPort, mapping.Subdomain, mapping.SandboxID, candidate.Port)
	h.ctx.AddPortURL(url)
	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxPortExpose,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"port":      candidate.Port,
				"host_port": mapping.HostPort,
				"protocol":  "tcp",
				"url":       url,
				"subdomain": mapping.Subdomain,
				"auto":      true,
			},
		})
	}

	process := candidate.Process
	if process == "" {
		process = "unknown-process"
	}
	return fmt.Sprintf("Auto-exposed port %d (%s) at %s", candidate.Port, process, url)
}

func (h *sandboxShellHandler) detectListeningPortsWithRetry(ctx context.Context) ([]sandbox.ListeningPort, error) {
	var lastErr error
	for i := 0; i < 8; i++ {
		ports, err := h.ctx.Manager.DetectListeningPorts(ctx, h.ctx.SessionID)
		if err == nil && len(ports) > 0 {
			return ports, nil
		}
		if err != nil {
			lastErr = err
		}
		if i < 7 {
			time.Sleep(1 * time.Second)
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func pickAutoExposeCandidate(command string, detected []sandbox.ListeningPort, exposed map[int]bool) (sandbox.ListeningPort, bool) {
	commandPort := extractPortFromCommand(command)
	var best sandbox.ListeningPort
	bestScore := -1

	for _, lp := range detected {
		if lp.Protocol != "" && !strings.EqualFold(lp.Protocol, "tcp") {
			continue
		}
		if exposed[lp.Port] {
			continue
		}
		score := scoreListeningPort(command, commandPort, lp)
		if score > bestScore {
			bestScore = score
			best = lp
		}
	}

	if bestScore < 0 {
		return sandbox.ListeningPort{}, false
	}
	return best, true
}

func scoreListeningPort(command string, commandPort int, lp sandbox.ListeningPort) int {
	score := 0
	c := strings.ToLower(command)
	p := strings.ToLower(lp.Process)

	if commandPort > 0 && lp.Port == commandPort {
		score += 10000
	}

	if strings.Contains(c, fmt.Sprintf(":%d", lp.Port)) {
		score += 2500
	}

	switch lp.Port {
	case 5173, 5174, 4173:
		score += 1800
	case 3000, 3001:
		score += 1600
	case 4000:
		score += 1400
	case 8080, 8081:
		score += 1200
	default:
		if lp.Port >= 1024 && lp.Port <= 65535 {
			score += 200
		}
	}

	if strings.Contains(p, "vite") ||
		strings.Contains(p, "node") ||
		strings.Contains(p, "next") ||
		strings.Contains(p, "nuxt") ||
		strings.Contains(p, "react") ||
		strings.Contains(p, "python") ||
		strings.Contains(p, "uvicorn") ||
		strings.Contains(p, "gunicorn") ||
		strings.Contains(p, "flask") ||
		strings.Contains(p, "django") ||
		strings.Contains(p, "rails") ||
		strings.Contains(p, "go") ||
		strings.Contains(p, "http") ||
		strings.Contains(p, "server") {
		score += 900
	}

	if lp.Address == "127.0.0.1" || lp.Address == "::1" {
		score += 300
	}

	return score
}

func extractPortFromCommand(command string) int {
	re := regexp.MustCompile(`(?i)(?:--port(?:=|\s+)|-p\s+)(\d{2,5})`)
	m := re.FindStringSubmatch(command)
	if len(m) != 2 {
		return 0
	}
	p, err := strconv.Atoi(m[1])
	if err != nil || p <= 0 || p > 65535 {
		return 0
	}
	return p
}

func extractLogPathFromCommand(command string) string {
	re := regexp.MustCompile(`>\s*(/tmp/[^\s]+)`)
	m := re.FindStringSubmatch(command)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

func isDetachedShellCommand(command string) bool {
	c := strings.ToLower(strings.TrimSpace(command))
	if c == "" {
		return false
	}
	return strings.Contains(c, "nohup ") ||
		strings.HasSuffix(c, "&") ||
		(strings.Contains(c, ">/tmp/") && strings.Contains(c, " 2>&1 &"))
}

func isLongRunningServerCommand(command string) bool {
	c := strings.ToLower(strings.TrimSpace(command))
	if c == "" {
		return false
	}

	// Prefer explicit dev/start server intents first so compound commands like
	// `npm install && npm run dev` are still treated as long-running servers.
	if strings.Contains(c, "npm run dev") ||
		strings.Contains(c, "pnpm dev") ||
		strings.Contains(c, "yarn dev") ||
		strings.Contains(c, "npm start") ||
		strings.Contains(c, "npm run start") ||
		strings.Contains(c, "pnpm start") ||
		strings.Contains(c, "yarn start") ||
		strings.Contains(c, "next dev") ||
		strings.Contains(c, "nuxt dev") ||
		strings.Contains(c, "uvicorn ") ||
		strings.Contains(c, "flask run") ||
		strings.Contains(c, "rails server") ||
		strings.Contains(c, "go run .") ||
		strings.Contains(c, "node server.js") ||
		strings.Contains(c, "node index.js") ||
		strings.Contains(c, "node app.js") {
		return true
	}

	// Scaffolding/build/install commands may include "vite" in package names
	// but are one-shot commands and must not be treated as long-running servers.
	if strings.Contains(c, "npm create vite") ||
		strings.Contains(c, "create-vite") ||
		strings.Contains(c, "vite@latest") ||
		strings.Contains(c, "vite build") ||
		strings.Contains(c, "npm run build") ||
		strings.Contains(c, "pnpm build") ||
		strings.Contains(c, "yarn build") ||
		strings.Contains(c, "npm test") ||
		strings.Contains(c, "pnpm test") ||
		strings.Contains(c, "yarn test") ||
		strings.Contains(c, "npm install") ||
		strings.Contains(c, "npm i ") ||
		strings.Contains(c, "pnpm install") ||
		strings.Contains(c, "pnpm i ") ||
		strings.Contains(c, "pnpm add") ||
		strings.Contains(c, "yarn install") ||
		strings.Contains(c, "yarn add") {
		return false
	}

	// Generic vite command (e.g. `vite --host 0.0.0.0`) should still be treated
	// as a long-running server once scaffolding/build cases are ruled out above.
	return strings.Contains(c, "vite")
}
