package tools

import (
	"context"
	"encoding/json"
	"fmt"

	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// PlatformDeleteAgentHandler handles the platform_delete_agent tool.
type PlatformDeleteAgentHandler struct {
	Ctx *PlatformToolContext
}

func (h *PlatformDeleteAgentHandler) Name() string { return "platform_delete_agent" }

func (h *PlatformDeleteAgentHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "platform_delete_agent",
			Description: "Delete an agent from the platform. This is irreversible. Use this when the user explicitly asks to delete or remove an agent.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent_id": map[string]interface{}{
						"type":        "string",
						"description": "The agent's UUID to delete.",
					},
				},
				"required": []string{"agent_id"},
			},
		},
	}
}

func (h *PlatformDeleteAgentHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}

	cmd := agentscmd.NewDeleteAgentCommand(agentID, h.Ctx.TenantID, h.Ctx.UserID, "")
	if err := h.Ctx.CommandBus.Dispatch(ctx, cmd); err != nil {
		return "", fmt.Errorf("failed to delete agent: %w", err)
	}

	// Emit system block
	h.Ctx.emitSystemBlock("agent_card", map[string]interface{}{
		"id":     agentID,
		"action": "deleted",
	})

	result := map[string]interface{}{
		"agent_id": agentID,
		"status":   "deleted",
	}
	b, _ := json.Marshal(result)
	return fmt.Sprintf("Agent deleted successfully.\n%s", string(b)), nil
}
