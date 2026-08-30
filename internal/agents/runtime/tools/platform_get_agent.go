package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
)

// PlatformGetAgentHandler handles the platform_get_agent tool.
type PlatformGetAgentHandler struct {
	Ctx *PlatformToolContext
}

func (h *PlatformGetAgentHandler) Name() string { return "platform_get_agent" }

func (h *PlatformGetAgentHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "platform_get_agent",
			Description: "Get detailed information about a specific agent by name or ID. Use this when the user asks about a particular agent's configuration, status, or details.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent_id": map[string]interface{}{
						"type":        "string",
						"description": "The agent's UUID. Provide this or name.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The agent's name. Provide this or agent_id.",
					},
				},
			},
		},
	}
}

func (h *PlatformGetAgentHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	name, _ := args["name"].(string)

	if agentID == "" && name == "" {
		return "", fmt.Errorf("either agent_id or name is required")
	}

	// List agents and find by ID or name
	enabled := true
	q := agentsquery.NewListAgentsQuery(h.Ctx.TenantID, &enabled, true, nil, nil, 200, 0)
	result, err := h.Ctx.QueryBus.Execute(ctx, q)
	if err != nil {
		return "", fmt.Errorf("failed to query agents: %w", err)
	}

	var data interface{} = result
	if resp, ok := result.(*query.Response); ok {
		data = resp.Data
	}

	agents, ok := data.([]agentsquery.AgentDefinitionReadModel)
	if !ok {
		return "", fmt.Errorf("unexpected query result type: %T", data)
	}

	var found *agentsquery.AgentDefinitionReadModel
	for i := range agents {
		if agentID != "" && agents[i].ID == agentID {
			found = &agents[i]
			break
		}
		if name != "" && agents[i].Name == name {
			found = &agents[i]
			break
		}
	}

	if found == nil {
		return "Agent not found.", nil
	}

	nullStr := func(ns sql.NullString) string {
		if ns.Valid {
			return ns.String
		}
		return ""
	}

	detail := map[string]interface{}{
		"id":               found.ID,
		"name":             found.Name,
		"description":      nullStr(found.Description),
		"model":            found.Model,
		"system_prompt":    nullStr(found.SystemPrompt),
		"tools":            found.Tools,
		"tool_count":       len(found.Tools),
		"max_turns":        found.MaxTurns,
		"mode":             found.Mode,
		"lifecycle_mode":   found.LifecycleMode,
		"lifecycle_status": found.LifecycleStatus,
		"hidden":           found.Hidden,
		"enabled":          found.Enabled,
		"created_at":       found.CreatedAt,
		"updated_at":       found.UpdatedAt,
	}

	// Emit system block
	h.Ctx.emitSystemBlock("agent_card", map[string]interface{}{
		"id":               found.ID,
		"name":             found.Name,
		"description":      nullStr(found.Description),
		"model":            found.Model,
		"tools":            found.Tools,
		"status":           found.LifecycleStatus,
		"lifecycle_mode":   found.LifecycleMode,
		"lifecycle_status": found.LifecycleStatus,
		"action":           "viewed",
	})

	b, _ := json.Marshal(detail)
	return string(b), nil
}
