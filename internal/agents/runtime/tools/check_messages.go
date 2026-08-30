package tools

import (
	"context"
	"fmt"
	"strings"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// CheckMessagesHandler implements the check_messages synthetic tool.
type CheckMessagesHandler struct {
	MessageBus *agentrt.AgentMessageBus
	AgentID    string
	TenantID   string
}

func (h *CheckMessagesHandler) Name() string { return "check_messages" }

func (h *CheckMessagesHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "check_messages",
			Description: "Check your inbox for messages from other agents. Returns pending messages and marks them as read.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"thread_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional thread ID to filter messages by thread.",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Message status to filter by (default: 'pending'). Options: pending, delivered, read.",
						"enum":        []string{"pending", "delivered", "read"},
					},
				},
			},
		},
	}
}

func (h *CheckMessagesHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	threadID, _ := args["thread_id"].(string)
	status, _ := args["status"].(string)

	messages, err := h.MessageBus.CheckMessages(ctx, h.AgentID, h.TenantID, threadID, status)
	if err != nil {
		return fmt.Sprintf("Failed to check messages: %s", err.Error()), nil
	}

	if len(messages) == 0 {
		return "No messages in inbox.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d message(s):\n\n", len(messages))
	for i, msg := range messages {
		senderLabel := msg.SenderName
		if senderLabel == "" {
			senderLabel = msg.SenderAgentID
		}
		fmt.Fprintf(&sb, "--- Message %d ---\n", i+1)
		fmt.Fprintf(&sb, "From: @%s\n", senderLabel)
		fmt.Fprintf(&sb, "Type: %s\n", msg.MessageType)
		if msg.ThreadID != "" {
			fmt.Fprintf(&sb, "Thread: %s\n", msg.ThreadID)
		}
		fmt.Fprintf(&sb, "Sent: %s\n", msg.SentAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(&sb, "Content: %s\n\n", msg.Content)
	}

	return sb.String(), nil
}
