package executors

import (
	"context"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// attachSyntheticTools gives a workflow agent node the same executable
// browser/sandbox surface as a normal agent session. The node still owns only
// an allowlist; lifecycle and browser allocation stay hidden behind this seam.
func (e *AgentExecutor) attachSyntheticTools(
	ctx context.Context,
	agentDef *agentConfig,
	loopInput *agentrt.LoopInput,
	sessionID, tenantID string,
	emitter *agentrt.Emitter,
	startedAt time.Time,
) *agenttools.BrowserSessionContext {
	if e == nil || loopInput == nil || agentDef == nil {
		return nil
	}

	runtimeConfig := agentDef.RuntimeConfig
	sandboxConfig := sandbox.ParseSandboxConfig(runtimeConfig)
	browserConfig := sandbox.ParseBrowserConfig(runtimeConfig)
	if !sandboxConfig.Enabled && !browserConfig.Enabled {
		return nil
	}

	interceptor := agenttools.NewToolInterceptor(e.ToolLoop)
	var sandboxCtx *agenttools.SandboxSessionContext

	if sandboxConfig.Enabled && e.SandboxManager != nil {
		sandboxConfig = e.SandboxManager.ClampToGlobalLimitsForTenant(sandboxConfig, tenantID)
		// A standalone browser pod does not need a duplicate Chromium sidecar.
		if browserConfig.Enabled && e.BrowserPool == nil {
			sandboxConfig.BrowserSidecar = browserConfig.ToSidecarConfig()
		}
		sandboxCtx = &agenttools.SandboxSessionContext{
			Manager:          e.SandboxManager,
			SessionID:        sessionID,
			TenantID:         tenantID,
			Config:           sandboxConfig,
			SessionStartedAt: startedAt,
			ExecutionMode:    "workflow",
			PersistenceMode:  "ephemeral",
			AgentID:          agentDef.ID,
			Emitter:          emitter,
		}
		for _, handler := range agenttools.NewSandboxHandlers(sandboxCtx) {
			if !workflowRuntimeToolEnabled(runtimeConfig, handler.Name()) {
				continue
			}
			interceptor.RegisterHandler(handler)
			loopInput.Tools = appendWorkflowTool(loopInput.Tools, handler.Name())
		}
	}

	if browserConfig.Enabled {
		if !browserConfig.Headless {
			// Headed browser access is a licensed capability. If no manager can
			// resolve the tenant entitlement, fail safe to headless mode.
			if e.SandboxManager == nil || !e.SandboxManager.IsBrowserHeadedEnabled(tenantID) {
				browserConfig.Headless = true
			}
		}

		// The standalone pool is intentionally independent of sandbox compute.
		// Keep a lightweight SandboxSessionContext for shared session identity;
		// the sidecar fallback still requires a real SandboxManager.
		if sandboxCtx == nil && e.BrowserPool != nil {
			sandboxCtx = &agenttools.SandboxSessionContext{
				Manager:          e.SandboxManager,
				SessionID:        sessionID,
				TenantID:         tenantID,
				Config:           sandbox.DefaultSandboxConfig(),
				SessionStartedAt: startedAt,
				ExecutionMode:    "workflow",
				PersistenceMode:  "ephemeral",
				AgentID:          agentDef.ID,
				Emitter:          emitter,
			}
		}

		if sandboxCtx == nil {
			logger.WithFields(
				"agent_id", agentDef.ID,
				"session_id", sessionID,
			).Warn("workflow agent: browser enabled but no browser pool or sandbox manager is configured")
		} else {
			browserCtx := &agenttools.BrowserSessionContext{
				SandboxCtx:       sandboxCtx,
				Config:           browserConfig,
				Emitter:          emitter,
				Pool:             e.BrowserPool,
				PersistSnapshots: true,
			}
			for _, handler := range agenttools.NewBrowserHandlers(browserCtx) {
				if !workflowRuntimeToolEnabled(runtimeConfig, handler.Name()) {
					continue
				}
				interceptor.RegisterHandler(handler)
				loopInput.Tools = appendWorkflowTool(loopInput.Tools, handler.Name())
			}
			loopInput.Interceptor = interceptor
			agenttools.WireSandboxEmitter(interceptor, emitter)
			agenttools.WireBrowserEmitter(interceptor, emitter)
			return browserCtx
		}
	}

	if len(interceptor.Handlers) > 0 {
		loopInput.Interceptor = interceptor
		agenttools.WireSandboxEmitter(interceptor, emitter)
	}
	return nil
}

func workflowRuntimeToolEnabled(config map[string]interface{}, toolName string) bool {
	raw, ok := config["disabled_runtime_tools"]
	if !ok {
		return true
	}
	switch values := raw.(type) {
	case []interface{}:
		for _, value := range values {
			if name, ok := value.(string); ok && strings.EqualFold(strings.TrimSpace(name), toolName) {
				return false
			}
		}
	case []string:
		for _, name := range values {
			if strings.EqualFold(strings.TrimSpace(name), toolName) {
				return false
			}
		}
	}
	return true
}

func appendWorkflowTool(tools []string, name string) []string {
	for _, existing := range tools {
		if existing == name {
			return tools
		}
	}
	return append(tools, name)
}
