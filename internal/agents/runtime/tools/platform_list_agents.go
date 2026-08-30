package tools

import (
	"context"
	"encoding/json"
	"fmt"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
)

// PlatformListAgentsHandler handles the platform_list_agents tool.
type PlatformListAgentsHandler struct {
	Ctx *PlatformToolContext
}

func (h *PlatformListAgentsHandler) Name() string { return "platform_list_agents" }

func (h *PlatformListAgentsHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "platform_list_agents",
			Description: "List all agents on the platform. Returns agent names, models, status, and tool counts. Use this when the user asks to see their agents.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"include_hidden": map[string]interface{}{
						"type":        "boolean",
						"description": "Include hidden/subagent agents. Defaults to false.",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of agents to return. Defaults to 50.",
					},
				},
			},
		},
	}
}

func (h *PlatformListAgentsHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	includeHidden := false
	if ih, ok := args["include_hidden"].(bool); ok {
		includeHidden = ih
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	enabled := true
	q := agentsquery.NewListAgentsQuery(h.Ctx.TenantID, &enabled, includeHidden, nil, nil, limit, 0)
	result, err := h.Ctx.QueryBus.Execute(ctx, q)
	if err != nil {
		return "", fmt.Errorf("failed to list agents: %w", err)
	}

	// Unwrap query.Response wrapper if present
	var data interface{} = result
	if resp, ok := result.(*query.Response); ok {
		data = resp.Data
	}

	agents, ok := data.([]agentsquery.AgentDefinitionReadModel)
	if !ok {
		return "", fmt.Errorf("unexpected query result type: %T", data)
	}

	// Build summary for each agent
	type agentSummary struct {
		ID              string   `json:"id"`
		Name            string   `json:"name"`
		Description     string   `json:"description,omitempty"`
		Model           string   `json:"model"`
		Tools           []string `json:"tools"`
		ToolCount       int      `json:"tool_count"`
		Status          string   `json:"status"`
		LifecycleMode   string   `json:"lifecycle_mode,omitempty"`
		LifecycleStatus string   `json:"lifecycle_status,omitempty"`
		Hidden          bool     `json:"hidden,omitempty"`
		CreatedAt       string   `json:"created_at"`
	}

	summaries := make([]agentSummary, 0, len(agents))
	blockPayloads := make([]map[string]interface{}, 0, len(agents))

	for _, a := range agents {
		desc := ""
		if a.Description.Valid {
			desc = a.Description.String
		}

		s := agentSummary{
			ID:              a.ID,
			Name:            a.Name,
			Description:     desc,
			Model:           a.Model,
			Tools:           a.Tools,
			ToolCount:       len(a.Tools),
			Status:          a.LifecycleStatus,
			LifecycleMode:   a.LifecycleMode,
			LifecycleStatus: a.LifecycleStatus,
			Hidden:          a.Hidden,
			CreatedAt:       a.CreatedAt,
		}
		summaries = append(summaries, s)

		blockPayloads = append(blockPayloads, map[string]interface{}{
			"id":               a.ID,
			"name":             a.Name,
			"description":      desc,
			"model":            a.Model,
			"tools":            a.Tools,
			"status":           a.LifecycleStatus,
			"lifecycle_mode":   a.LifecycleMode,
			"lifecycle_status": a.LifecycleStatus,
		})
	}

	// Emit system block with agent list
	h.Ctx.emitSystemBlock("agent_list", map[string]interface{}{
		"agents": blockPayloads,
		"total":  len(agents),
	})

	b, _ := json.Marshal(summaries)
	return fmt.Sprintf("Found %d agent(s).\n%s", len(agents), string(b)), nil
}
