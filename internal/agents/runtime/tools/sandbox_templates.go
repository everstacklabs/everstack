package tools

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// ============================================================================
// sandbox_list_templates — lists available sandbox environment templates
// ============================================================================

type sandboxListTemplatesHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxListTemplatesHandler) Name() string { return "sandbox_list_templates" }

func (h *sandboxListTemplatesHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxListTemplatesHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_list_templates",
			Description: "List available sandbox environment templates (e.g., Node.js, Python, Go, Rust). Use this to see what environments are available, then select the best one for the task using sandbox_set_template. Choose the environment that best matches the user's project or request — do not ask the user to pick unless the task is genuinely ambiguous between multiple options.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (h *sandboxListTemplatesHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	templates := sandbox.ListTemplates()
	if len(templates) == 0 {
		return "No templates available", nil
	}

	// Return text summary for the LLM. The agent should pick the right
	// template autonomously based on the user's request — no HITL needed.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d environment(s) available:\n\n", len(templates)))
	for _, t := range templates {
		network := t.NetworkMode
		if network == "whitelist" && len(t.AllowedHosts) > 0 {
			network = fmt.Sprintf("whitelist (%s)", strings.Join(t.AllowedHosts, ", "))
		}
		sb.WriteString(fmt.Sprintf("- **%s** (id: `%s`)\n", t.Name, t.Slug))
		sb.WriteString(fmt.Sprintf("  %s | Image: %s | Network: %s\n", t.Description, t.Image, network))
		sb.WriteString(fmt.Sprintf("  CPU: %.0f core, Memory: %dMB, Disk: %dMB\n\n", t.CPULimit, t.MemoryMB, t.DiskMB))
	}
	sb.WriteString("Select the most appropriate environment for the task using sandbox_set_template. If the task clearly implies a language or framework, choose it directly without asking the user.")
	return sb.String(), nil
}

// ============================================================================
// sandbox_set_template — selects a template before sandbox provisioning
// ============================================================================

type sandboxSetTemplateHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxSetTemplateHandler) Name() string { return "sandbox_set_template" }

func (h *sandboxSetTemplateHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxSetTemplateHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_set_template",
			Description: "Set or change the sandbox environment template. If a sandbox is already running, it will be destroyed and a new one created with the new template on the next command. Use sandbox_list_templates first to see available options, then select the best match for the task.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"template": map[string]interface{}{
						"type":        "string",
						"description": "Template ID or slug (e.g., 'node-dev', 'python-dev', 'go-dev', 'rust-dev').",
					},
				},
				"required": []string{"template"},
			},
		},
	}
}

func (h *sandboxSetTemplateHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	templateID, _ := args["template"].(string)
	if templateID == "" {
		return "", fmt.Errorf("template is required")
	}

	tpl := sandbox.GetTemplate(templateID)
	if tpl == nil {
		return fmt.Sprintf("Template %q not found. Use sandbox_list_templates to see available options.", templateID), nil
	}

	targetCfg := sandbox.TemplateToSandboxConfig(tpl)
	mergedCfg := applyTemplatePreservingSessionFields(targetCfg, h.ctx.Config)

	// If a sandbox already exists and already matches the requested template,
	// keep it. This avoids unnecessary state loss when the model re-selects the
	// same template in later turns.
	if inst, exists := h.ctx.Manager.GetInstance(h.ctx.SessionID); exists {
		if instanceMatchesTemplate(inst, targetCfg) {
			h.ctx.Config = mergedCfg
			return fmt.Sprintf("Environment already set to **%s** (%s). Reusing existing sandbox.", tpl.Name, tpl.Description), nil
		}
		// Safety for persistent troopers: do not destroy/recreate a live trooper
		// sandbox during normal chat turns when real workspace data exists.
		// Allow reset only when /workspace is effectively empty (identity stubs only),
		// so bootstrapping Node/Python tooling still works on fresh troopers.
		if h.ctx.Config.Persistent && strings.TrimSpace(h.ctx.AgentID) != "" {
			files, err := h.ctx.Manager.ListFiles(ctx, h.ctx.SessionID, "/workspace")
			if err == nil && trooperWorkspaceLooksEmpty(files) {
				// Workspace is empty, safe to reset template.
			} else {
				return fmt.Sprintf(
					"Skipping environment reset to **%s** for this persistent trooper to preserve existing /workspace state. Continue with current environment.",
					tpl.Name,
				), nil
			}
		}
	}

	// If a sandbox already exists and the template actually changes, destroy it
	// so the new template takes effect.
	replaced := false
	if _, exists := h.ctx.Manager.GetInstance(h.ctx.SessionID); exists {
		if err := h.ctx.Manager.Destroy(ctx, h.ctx.SessionID); err != nil {
			return fmt.Sprintf("Failed to destroy existing sandbox: %s", err.Error()), nil
		}
		if h.ctx.Emitter != nil {
			h.ctx.Emitter.Emit(agentrt.Event{
				Type:      agentrt.EventSandboxDestroy,
				SessionID: h.ctx.SessionID,
				Timestamp: time.Now(),
			})
		}
		replaced = true
	}

	// Apply template to the session config
	h.ctx.Config = mergedCfg

	if replaced {
		return fmt.Sprintf("Previous sandbox destroyed. Environment set to **%s** (%s). Network: %s. A new sandbox will be created with this configuration on the next command.", tpl.Name, tpl.Description, tpl.NetworkMode), nil
	}
	return fmt.Sprintf("Environment set to **%s** (%s). Network: %s. The sandbox will be created with this configuration on the next command.", tpl.Name, tpl.Description, tpl.NetworkMode), nil
}

func instanceMatchesTemplate(inst *sandbox.Instance, cfg sandbox.SandboxConfig) bool {
	if inst == nil {
		return false
	}
	instCfg := inst.Config

	// Only compare template-owned fields (image, network, hosts, env).
	// Resource limits (CPU, memory, disk) are user-configured and not
	// part of the template identity.
	if instCfg.Image != cfg.Image ||
		instCfg.NetworkMode != sandbox.NetworkMode(cfg.NetworkMode) {
		return false
	}

	if !slices.Equal(normalizeStrings(instCfg.AllowedHosts), normalizeStrings(cfg.AllowedHosts)) {
		return false
	}
	return maps.Equal(instCfg.EnvVars, cfg.EnvVars)
}

func normalizeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	slices.Sort(out)
	return out
}

func trooperWorkspaceLooksEmpty(files []sandbox.FileInfo) bool {
	if len(files) == 0 {
		return true
	}
	allowed := map[string]struct{}{
		"IDENTITY.md": {},
		"USER.md":     {},
		"ROLE.md":     {},
		"SOUL.md":     {},
		".tmp":        {},
	}
	for _, f := range files {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		// Skip directories — we only care about user-created content files.
		if f.IsDir {
			continue
		}
		// Skip hidden/dotfiles (e.g. .bashrc, .profile from base image)
		// except explicitly allowed ones like .tmp.
		if strings.HasPrefix(name, ".") {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		return false
	}
	return true
}

// applyTemplatePreservingSessionFields applies template runtime settings while
// preserving per-session metadata configured outside template selection.
// Templates only change the image, env vars, allowed hosts, and network mode.
// User-configured resource limits (CPU, memory, disk, timeout) are always preserved.
func applyTemplatePreservingSessionFields(templateCfg, existing sandbox.SandboxConfig) sandbox.SandboxConfig {
	cfg := templateCfg

	// Preserve user-configured resource limits — templates are not authoritative
	// for these; the user sets them via the agent definition's sandbox config.
	if existing.CPULimit > 0 {
		cfg.CPULimit = existing.CPULimit
	}
	if existing.MemoryMB > 0 {
		cfg.MemoryMB = existing.MemoryMB
	}
	if existing.DiskMB > 0 {
		cfg.DiskMB = existing.DiskMB
	}
	if existing.TimeoutSeconds > 0 {
		cfg.TimeoutSeconds = existing.TimeoutSeconds
	}

	// Preserve trooper identity/persistence routing fields.
	cfg.Persistent = existing.Persistent
	cfg.AgentID = existing.AgentID

	// Preserve git integration settings configured by the user/agent definition.
	cfg.GitRepoURL = existing.GitRepoURL
	cfg.GitBranch = existing.GitBranch
	cfg.GitInstallationID = existing.GitInstallationID

	// Preserve session-level lifecycle/access settings that templates don't own.
	cfg.IdleRetentionSeconds = existing.IdleRetentionSeconds
	cfg.Name = existing.Name
	cfg.SSHEnabled = existing.SSHEnabled

	// Preserve explicit tool allowlist if configured.
	if len(existing.Tools) > 0 {
		cfg.Tools = append([]string(nil), existing.Tools...)
	}

	return cfg
}
