package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// PlatformCreateAgentHandler handles the platform_create_agent tool.
type PlatformCreateAgentHandler struct {
	Ctx *PlatformToolContext
}

func (h *PlatformCreateAgentHandler) Name() string { return "platform_create_agent" }

func (h *PlatformCreateAgentHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "platform_create_agent",
			Description: "Create a new agent on the platform. Returns the created agent details. Use this when the user wants to create, build, or set up a new agent.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the agent. Must be unique.",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Brief description of what the agent does.",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "LLM model to use (e.g. 'claude-sonnet-4-20250514', 'gpt-4o'). Defaults to 'claude-sonnet-4-20250514' if not specified.",
					},
					"system_prompt": map[string]interface{}{
						"type":        "string",
						"description": "System prompt/instructions for the agent.",
					},
					"tools": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "List of tool names to enable (e.g. 'sandbox_shell', 'web_search', 'memory_store'). Leave empty for a chat-only agent.",
					},
					"max_turns": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum conversation turns. 0 for unlimited. Defaults to 0.",
					},
				},
				"required": []string{"name"},
			},
		},
	}
}

func (h *PlatformCreateAgentHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	description, _ := args["description"].(string)
	model, _ := args["model"].(string)
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	systemPrompt, _ := args["system_prompt"].(string)

	var tools []string
	if toolsRaw, ok := args["tools"].([]interface{}); ok {
		for _, t := range toolsRaw {
			if s, ok := t.(string); ok {
				tools = append(tools, s)
			}
		}
	}

	var maxTurns int32
	if mt, ok := args["max_turns"].(float64); ok {
		maxTurns = int32(mt)
	}

	cmd := agentscmd.NewCreateAgentCommand(
		h.Ctx.TenantID,
		name,
		description,
		model,
		systemPrompt,
		tools,
		nil,   // config
		maxTurns,
		0,     // maxToolCallsPerTurn
		"primary",
		nil,   // maxSteps
		"ask", // taskPermissionMode
		false, // hidden
		nil,   // color
		nil,   // workingDirectory
		nil,   // mentionAlias
		"",    // lifecycleMode
		nil,   // icon
		"", "", "", "", // soulMD, identityMD, userMD, roleMD
		"", "",         // sandboxImage, sandboxNetworkMode
		0,              // sandboxCPULimit
		0, 0,           // sandboxMemoryMB, sandboxDiskMB
		0,              // sandboxTimeoutSeconds
		nil,            // sandboxAllowedHosts
		nil,            // sandboxEnvVars
		false,          // sandboxSSHEnabled
		"", "",         // sandboxGitRepoURL, sandboxGitBranch
		"", "", "",     // dbSqlitePath, dbLanceDBPath, dbRedbPath
		0,              // maxConcurrentWorkers
		nil,            // workerPoolConfig
		false,          // autoProvision
		h.Ctx.UserID,
		"", // traceID
	)

	if err := h.Ctx.CommandBus.Dispatch(ctx, cmd); err != nil {
		return "", fmt.Errorf("failed to create agent: %w", err)
	}

	// Wait briefly for event projection
	time.Sleep(200 * time.Millisecond)

	// Emit system block for the UI
	h.Ctx.emitSystemBlock("agent_card", map[string]interface{}{
		"id":          cmd.ID,
		"name":        name,
		"description": description,
		"model":       model,
		"tools":       tools,
		"status":      "created",
		"action":      "created",
	})

	result := map[string]interface{}{
		"id":          cmd.ID,
		"name":        name,
		"description": description,
		"model":       model,
		"tools":       tools,
		"status":      "created",
	}
	b, _ := json.Marshal(result)
	return fmt.Sprintf("Agent created successfully.\n%s", string(b)), nil
}
