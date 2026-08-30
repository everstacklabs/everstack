package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// PlatformUpdateAgentHandler handles the platform_update_agent tool.
type PlatformUpdateAgentHandler struct {
	Ctx *PlatformToolContext
}

func (h *PlatformUpdateAgentHandler) Name() string { return "platform_update_agent" }

func (h *PlatformUpdateAgentHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "platform_update_agent",
			Description: "Update an existing agent's configuration. You can change the name, model, system prompt, tools, or other settings. Use this when the user wants to modify an agent.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent_id": map[string]interface{}{
						"type":        "string",
						"description": "The agent's UUID to update.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "New name for the agent.",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "New description.",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "New LLM model.",
					},
					"system_prompt": map[string]interface{}{
						"type":        "string",
						"description": "New system prompt.",
					},
					"tools": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Updated list of enabled tools. This replaces the existing tools list.",
					},
				},
				"required": []string{"agent_id"},
			},
		},
	}
}

func (h *PlatformUpdateAgentHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}

	cmd := &agentscmd.UpdateAgentCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    h.Ctx.UserID,
		},
		AgentID:  agentID,
		TenantID: h.Ctx.TenantID,
	}

	if v, ok := args["name"].(string); ok && v != "" {
		cmd.Name = &v
	}
	if v, ok := args["description"].(string); ok {
		cmd.Description = &v
	}
	if v, ok := args["model"].(string); ok && v != "" {
		cmd.Model = &v
	}
	if v, ok := args["system_prompt"].(string); ok {
		cmd.SystemPrompt = &v
	}
	if toolsRaw, ok := args["tools"].([]interface{}); ok {
		var tools []string
		for _, t := range toolsRaw {
			if s, ok := t.(string); ok {
				tools = append(tools, s)
			}
		}
		cmd.Tools = tools
	}

	if err := h.Ctx.CommandBus.Dispatch(ctx, cmd); err != nil {
		return "", fmt.Errorf("failed to update agent: %w", err)
	}

	// Wait briefly for event projection
	time.Sleep(200 * time.Millisecond)

	// Emit system block
	h.Ctx.emitSystemBlock("agent_card", map[string]interface{}{
		"id":     agentID,
		"name":   stringOrDefault(args["name"], ""),
		"model":  stringOrDefault(args["model"], ""),
		"action": "updated",
	})

	result := map[string]interface{}{
		"agent_id": agentID,
		"status":   "updated",
	}
	b, _ := json.Marshal(result)
	return fmt.Sprintf("Agent updated successfully.\n%s", string(b)), nil
}

func stringOrDefault(v interface{}, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
