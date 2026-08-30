package tools

import (
	"context"
	"encoding/json"
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

// DelegateJobHandler implements the delegate_job synthetic tool.
// It sends a structured job delegation message to another agent.
type DelegateJobHandler struct {
	MessageBus    *agentrt.AgentMessageBus
	SenderAgentID string
	SenderName    string
	TenantID      string
	DB            *sqlx.DB
	ServerCtx     context.Context
}

func (h *DelegateJobHandler) Name() string { return "delegate_job" }

func (h *DelegateJobHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "delegate_job",
			Description: "Delegate a job to another agent. The target agent will receive the job as a delegation message and can respond with results on the callback thread.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target_agent": map[string]interface{}{
						"type":        "string",
						"description": "Name or ID of the target agent to delegate to.",
					},
					"job": map[string]interface{}{
						"type":        "string",
						"description": "Description of what the target agent should do.",
					},
					"priority": map[string]interface{}{
						"type":        "integer",
						"description": "Job priority (1=urgent, 2=high, 3=normal, 4=low). Default: 3.",
						"enum":        []int{1, 2, 3, 4},
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum time for the delegated job in seconds (default: 300).",
					},
				},
				"required": []string{"target_agent", "job"},
			},
		},
	}
}

func (h *DelegateJobHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	targetAgent, _ := args["target_agent"].(string)
	job, _ := args["job"].(string)
	priority := 3
	if p, ok := args["priority"].(float64); ok && p >= 1 && p <= 4 {
		priority = int(p)
	}
	timeoutSeconds := 300
	if t, ok := args["timeout_seconds"].(float64); ok && t > 0 && t <= 3600 {
		timeoutSeconds = int(t)
	}

	if targetAgent == "" || job == "" {
		return "", fmt.Errorf("target_agent and job are required")
	}

	// Resolve the target agent
	agent, err := h.resolveAgent(ctx, targetAgent)
	if err != nil {
		return fmt.Sprintf("Failed to find target agent: %s", err.Error()), nil
	}

	// Check authorization
	if err := h.checkDelegationAuth(ctx, agent.ID); err != nil {
		return fmt.Sprintf("Not authorized to delegate: %s", err.Error()), nil
	}

	callbackThreadID := uuid.New().String()

	// Build delegation payload
	payload := map[string]interface{}{
		"job":                job,
		"priority":           priority,
		"timeout_seconds":    timeoutSeconds,
		"callback_thread_id": callbackThreadID,
		"delegator_agent_id": h.SenderAgentID,
		"delegator_name":     h.SenderName,
	}
	payloadJSON, _ := json.Marshal(payload)

	msg := agentrt.PeerMessage{
		ID:            uuid.New().String(),
		SenderAgentID: h.SenderAgentID,
		SenderName:    h.SenderName,
		ThreadID:      callbackThreadID,
		Content:       fmt.Sprintf("Job delegation (priority %d, timeout %ds): %s", priority, timeoutSeconds, job),
		MessageType:   "delegation",
		SentAt:        time.Now(),
	}

	// Store payload as part of the message in DB
	_ = payloadJSON // payload is embedded in message content for now

	if err := h.MessageBus.SendMessage(ctx, msg, agent.ID, h.TenantID); err != nil {
		return fmt.Sprintf("Failed to delegate job: %s", err.Error()), nil
	}

	return fmt.Sprintf(
		"Job delegated to @%s\nCallback thread: %s\nPriority: %d\nTimeout: %ds\n\nUse check_messages with thread_id=%q to check for results.",
		agent.Name, callbackThreadID, priority, timeoutSeconds, callbackThreadID,
	), nil
}

func (h *DelegateJobHandler) resolveAgent(ctx context.Context, agentRef string) (*agentsquery.AgentDefinitionReadModel, error) {
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

func (h *DelegateJobHandler) checkDelegationAuth(ctx context.Context, targetAgentID string) error {
	if h.DB == nil {
		return nil
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		tenantID = h.TenantID
	}

	// Check for any link type — PEER can delegate too, but SUPERVISOR is preferred
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
