package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/jmoiron/sqlx"
)

// SendMessageHandler implements the send_message synthetic tool for cross-agent communication.
type SendMessageHandler struct {
	MessageBus     *agentrt.AgentMessageBus
	SenderAgentID  string
	SenderName     string
	TenantID       string
	DB             *sqlx.DB
	ServerCtx      context.Context // fallback for CQRS
}

func (h *SendMessageHandler) Name() string { return "send_message" }

func (h *SendMessageHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "send_message",
			Description: "Send a message to another agent. The target agent must be linked to this agent. Messages are delivered in real-time if the target is active, or queued for later delivery.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target_agent": map[string]interface{}{
						"type":        "string",
						"description": "Name or ID of the target agent.",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The message content to send.",
					},
					"thread_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional thread ID for threaded conversations. If omitted, a new thread is started.",
					},
				},
				"required": []string{"target_agent", "content"},
			},
		},
	}
}

func (h *SendMessageHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	targetAgent, _ := args["target_agent"].(string)
	content, _ := args["content"].(string)
	threadID, _ := args["thread_id"].(string)

	if targetAgent == "" || content == "" {
		return "", fmt.Errorf("target_agent and content are required")
	}

	// Resolve the target agent
	agent, err := h.resolveAgent(ctx, targetAgent)
	if err != nil {
		return fmt.Sprintf("Failed to find target agent: %s", err.Error()), nil
	}

	// Check that sender has an agent_link to the target
	if err := h.checkPeerAuthorization(ctx, agent.ID); err != nil {
		return fmt.Sprintf("Not authorized: %s", err.Error()), nil
	}

	if threadID == "" {
		threadID = uuid.New().String()
	}

	msg := agentrt.PeerMessage{
		ID:            uuid.New().String(),
		SenderAgentID: h.SenderAgentID,
		SenderName:    h.SenderName,
		ThreadID:      threadID,
		Content:       content,
		MessageType:   "message",
		SentAt:        time.Now(),
	}

	if err := h.MessageBus.SendMessage(ctx, msg, agent.ID, h.TenantID); err != nil {
		return fmt.Sprintf("Failed to send message: %s", err.Error()), nil
	}

	return fmt.Sprintf("Message sent to @%s (thread: %s)", agent.Name, threadID), nil
}

func (h *SendMessageHandler) resolveAgent(ctx context.Context, agentRef string) (*agentsquery.AgentDefinitionReadModel, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && h.ServerCtx != nil {
		sys, err = cqrs.GetSystemFromContext(h.ServerCtx)
	}
	if err != nil {
		return nil, fmt.Errorf("CQRS system not available: %w", err)
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		tenantID = h.TenantID
	}

	var res interface{}
	if _, parseErr := uuid.Parse(agentRef); parseErr == nil {
		q := agentsquery.NewGetAgentByIDQuery(agentRef, tenantID)
		res, err = sys.QueryBus.Execute(ctx, q)
	} else {
		q := agentsquery.NewGetAgentByNameQuery(agentRef, tenantID)
		res, err = sys.QueryBus.Execute(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("agent %q not found", agentRef)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	if data == nil {
		return nil, fmt.Errorf("agent %q not found", agentRef)
	}

	agent, ok := data.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		return nil, fmt.Errorf("unexpected data type: %T", data)
	}
	return agent, nil
}

func (h *SendMessageHandler) checkPeerAuthorization(ctx context.Context, targetAgentID string) error {
	if h.DB == nil {
		// No DB — allow (development/testing mode)
		return nil
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		tenantID = h.TenantID
	}

	var count int
	err := h.DB.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM agent_links
		WHERE source_agent_id = $1 AND target_id = $2 AND tenant_id = $3
	`, h.SenderAgentID, targetAgentID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to check agent link: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("no link exists from this agent to %s; create a link first", targetAgentID)
	}
	return nil
}
