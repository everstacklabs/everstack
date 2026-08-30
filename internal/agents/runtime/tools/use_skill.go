package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// UseSkillHandler is a synthetic tool that lets the agent dynamically load a
// skill's instructions from the sandbox filesystem. Skills are written to
// /skills/{name}/SKILL.md at sandbox provision time. The handler reads the
// file content and returns it so the LLM can follow the skill's instructions.
type UseSkillHandler struct {
	SandboxCtx *SandboxSessionContext
	Emitter    *agentrt.Emitter
	SessionID  string

	// AvailableSkills is the set of skill names installed for this agent.
	// Used for validation and for the enum in the tool definition.
	AvailableSkills []agentrt.SkillEntry
}

func (h *UseSkillHandler) Name() string { return "use_skill" }

func (h *UseSkillHandler) Definition() gw.ToolDefinition {
	// Build enum of available skill names for the tool parameter
	skillNames := make([]interface{}, 0, len(h.AvailableSkills))
	for _, s := range h.AvailableSkills {
		skillNames = append(skillNames, s.Name)
	}

	// Build description listing available skills
	var sb strings.Builder
	sb.WriteString("Load the full instructions for a matching installed skill. Available skills:\n")
	for _, s := range h.AvailableSkills {
		desc := s.Description
		if desc == "" {
			desc = "No description"
		}
		sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, desc))
	}
	sb.WriteString("Call this when a request clearly matches one of these skills.")

	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill_name": map[string]interface{}{
				"type":        "string",
				"description": "The name of the skill to load.",
				"enum":        skillNames,
			},
		},
		"required": []string{"skill_name"},
	}

	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "use_skill",
			Description: sb.String(),
			Parameters:  params,
		},
	}
}

func (h *UseSkillHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	skillName, _ := args["skill_name"].(string)
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return "", fmt.Errorf("skill_name is required")
	}

	// Validate that the skill exists in the installed set
	var found *agentrt.SkillEntry
	for i := range h.AvailableSkills {
		if h.AvailableSkills[i].Name == skillName {
			found = &h.AvailableSkills[i]
			break
		}
	}
	if found == nil {
		available := make([]string, 0, len(h.AvailableSkills))
		for _, s := range h.AvailableSkills {
			available = append(available, s.Name)
		}
		return "", fmt.Errorf("skill %q not found. Available skills: %s", skillName, strings.Join(available, ", "))
	}

	// Emit skill.start event
	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSkillStart,
			SessionID: h.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"skill_name":        skillName,
				"skill_description": found.Description,
			},
		})
	}

	start := time.Now()

	// Read the skill file from the sandbox filesystem
	skillPath := fmt.Sprintf("/skills/%s/SKILL.md", skillName)
	content, err := h.readSkillFromSandbox(ctx, skillPath)
	if err != nil {
		// Fallback: use the in-memory content from the skill entry
		logger.WithFields(
			"skill_name", skillName,
			"path", skillPath,
			"error", err.Error(),
		).Warn("use_skill: failed to read skill from sandbox, using in-memory fallback")
		content = found.Content
	}

	durationMs := time.Since(start).Milliseconds()

	if content == "" {
		if h.Emitter != nil {
			h.Emitter.Emit(agentrt.Event{
				Type:      agentrt.EventSkillError,
				SessionID: h.SessionID,
				Timestamp: time.Now(),
				Error:     fmt.Sprintf("skill %q has empty content", skillName),
				Data: map[string]interface{}{
					"skill_name": skillName,
				},
			})
		}
		return "", fmt.Errorf("skill %q has empty content", skillName)
	}

	// Emit skill.end event
	if h.Emitter != nil {
		h.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSkillEnd,
			SessionID: h.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"skill_name":  skillName,
				"duration_ms": durationMs,
				"content_len": len(content),
			},
		})
	}

	return fmt.Sprintf("## Skill: %s\n\n%s", skillName, content), nil
}

// readSkillFromSandbox reads a skill file from the sandbox filesystem.
func (h *UseSkillHandler) readSkillFromSandbox(ctx context.Context, path string) (string, error) {
	if h.SandboxCtx == nil || h.SandboxCtx.Manager == nil {
		return "", fmt.Errorf("sandbox not available")
	}

	// Ensure the sandbox is running (lazy provision on first tool call)
	if _, err := ensureSandbox(ctx, h.SandboxCtx); err != nil {
		return "", fmt.Errorf("failed to ensure sandbox: %w", err)
	}

	data, err := h.SandboxCtx.Manager.ReadFile(ctx, h.SandboxCtx.SessionID, path)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}

	return string(data), nil
}
