package executors

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// StartExecutor handles the start node -- applies system prompt and input template.
type StartExecutor struct{}

func (e *StartExecutor) NodeType() string { return "start" }

func (e *StartExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	// Snapshot original messages before any modification
	ec.SnapshotOriginalMessages()

	systemPrompt := node.GetConfigString("systemPrompt")
	inputTemplate := node.GetConfigString("inputTemplate")

	// Prepend system prompt if configured
	if systemPrompt != "" {
		textContent := systemPrompt
		systemMsg := gateway.Message{
			Role: gateway.RoleSystem,
			Content: []gateway.ContentPart{
				{Type: "text", Text: &textContent},
			},
		}
		// Insert system message at the beginning
		ec.Messages = append([]gateway.Message{systemMsg}, ec.Messages...)
	}

	// Apply input template to the last user message if configured
	if inputTemplate != "" && len(ec.Messages) > 0 {
		for i := len(ec.Messages) - 1; i >= 0; i-- {
			if ec.Messages[i].Role == gateway.RoleUser && len(ec.Messages[i].Content) > 0 {
				if ec.Messages[i].Content[0].Text != nil {
					original := *ec.Messages[i].Content[0].Text
					templated := strings.ReplaceAll(inputTemplate, "{{input}}", original)
					ec.Messages[i].Content[0].Text = &templated
				}
				break
			}
		}
	}

	// Populate per-node execution data
	if systemPrompt != "" {
		preview := systemPrompt
		if len(preview) > 200 {
			preview = preview[:200]
		}
		ec.SetNodeData("system_prompt", preview)
	}
	ec.SetNodeData("message_count", fmt.Sprintf("%d", len(ec.Messages)))

	output := map[string]interface{}{
		"message_count":      len(ec.Messages),
		"has_input_template": inputTemplate != "",
	}
	if systemPrompt != "" {
		preview := systemPrompt
		if len(preview) > 200 {
			preview = preview[:200]
		}
		output["system_prompt_preview"] = preview
	}

	return engine.NodeResult{NextHandle: "out", Output: output}
}
