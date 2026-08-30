package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/channels"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// ChannelHistoryHandler exposes channel/thread message history to the agent
// as a callable tool. The agent decides when and how many messages to read.
type ChannelHistoryHandler struct {
	Fetcher    channels.HistoryFetcher
	ChannelRef string // The current channel ID
	ThreadRef  string // The current thread timestamp (may be empty)
}

func (h *ChannelHistoryHandler) Name() string { return "read_channel_history" }

func (h *ChannelHistoryHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "read_channel_history",
			Description: "Read recent messages from the current channel or thread. Use this to understand what has been discussed before responding. You can control how many messages to fetch.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Number of recent messages to fetch (1-100). Start small (10-20) and fetch more if needed.",
						"default":     20,
					},
				},
			},
		},
	}
}

func (h *ChannelHistoryHandler) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	msgs, err := h.Fetcher.FetchHistory(h.ChannelRef, h.ThreadRef, limit)
	if err != nil {
		return "", fmt.Errorf("fetch history: %w", err)
	}

	if len(msgs) == 0 {
		return "No messages found in the channel/thread history.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d messages:\n\n", len(msgs)))
	for _, m := range msgs {
		role := m.UserName
		if m.IsBot {
			role = role + " (you)"
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", role, m.Text))
	}

	return sb.String(), nil
}
