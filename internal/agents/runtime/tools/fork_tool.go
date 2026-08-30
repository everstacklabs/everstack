package tools

import (
	"context"
	"fmt"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// ForkHandler handles the fork synthetic tool call.
type ForkHandler struct {
	ForkManager    *agentrt.ForkManager
	ParentInput    *agentrt.LoopInput
	CurrentMessages func() []gw.Message // returns current conversation state
}

// Name returns the tool name.
func (h *ForkHandler) Name() string { return "fork" }

// Definition returns the tool definition.
func (h *ForkHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "fork",
			Description: "Fork the current conversation context to think about a specific question independently. The fork runs concurrently and its conclusion will be injected back into this conversation automatically. Use this when you need to reason deeply about something without losing the current train of thought.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"instruction": map[string]interface{}{
						"type":        "string",
						"description": "A clear instruction for what the fork should think about and conclude.",
					},
				},
				"required": []string{"instruction"},
			},
		},
	}
}

// Execute starts a fork and returns immediately.
func (h *ForkHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	instruction, _ := args["instruction"].(string)
	if instruction == "" {
		return "", fmt.Errorf("instruction is required")
	}

	if h.ForkManager == nil {
		return "Fork is not available.", nil
	}

	if h.ParentInput == nil {
		return "", fmt.Errorf("fork: parent input not set")
	}

	// Get current messages from the conversation
	var messages []gw.Message
	if h.CurrentMessages != nil {
		messages = h.CurrentMessages()
	}

	forkID, err := h.ForkManager.Fork(ctx, instruction, messages, h.ParentInput)
	if err != nil {
		return fmt.Sprintf("Cannot fork: %s", err.Error()), nil
	}

	return fmt.Sprintf("Fork %s started. Its conclusion will appear automatically in this conversation when ready.", forkID), nil
}
