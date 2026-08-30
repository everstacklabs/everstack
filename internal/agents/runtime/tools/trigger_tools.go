package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/agents/trigger"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// ---------------------------------------------------------------------------
// create_trigger
// ---------------------------------------------------------------------------

// TriggerCreateHandler handles the create_trigger synthetic tool.
type TriggerCreateHandler struct {
	Store    trigger.Store
	TenantID string
	AgentID  string
}

func (h *TriggerCreateHandler) Name() string { return "create_trigger" }

func (h *TriggerCreateHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "create_trigger",
			Description: "Create a trigger (cron schedule, webhook, or event-driven) for this agent. The trigger will start a new session with the rendered input when fired.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Human-readable name for the trigger.",
					},
					"trigger_type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"cron", "webhook", "event"},
						"description": "Type of trigger: cron (scheduled), webhook (HTTP callback), or event (fired by another agent).",
					},
					"cron_expression": map[string]interface{}{
						"type":        "string",
						"description": "Cron expression (e.g. '*/30 * * * *'). Required for cron triggers.",
					},
					"cron_timezone": map[string]interface{}{
						"type":        "string",
						"description": "IANA timezone for the cron schedule. Defaults to UTC.",
					},
					"event_source_agent_id": map[string]interface{}{
						"type":        "string",
						"description": "Agent ID whose events should fire this trigger. Required for event triggers.",
					},
					"event_type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"session.end", "session.error"},
						"description": "Event type to listen for. Required for event triggers.",
					},
					"input_template": map[string]interface{}{
						"type":        "string",
						"description": "Go text/template string used to render the session input when the trigger fires. Available data varies by trigger type.",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum execution time in seconds for triggered sessions. Defaults to 300.",
					},
					"max_retries": map[string]interface{}{
						"type":        "integer",
						"description": "Number of retry attempts on failure. Defaults to 0.",
					},
					"workflow_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional workflow ID. If set, the trigger executes this workflow instead of starting an agent session.",
					},
				},
				"required": []string{"name", "trigger_type"},
			},
		},
	}
}

func (h *TriggerCreateHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	name, _ := args["name"].(string)
	triggerType, _ := args["trigger_type"].(string)

	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	tt := trigger.TriggerType(triggerType)
	switch tt {
	case trigger.TriggerCron, trigger.TriggerWebhook, trigger.TriggerEvent:
	default:
		return "", fmt.Errorf("invalid trigger_type %q: must be cron, webhook, or event", triggerType)
	}

	t := &trigger.Trigger{
		TenantID: h.TenantID,
		AgentID:  h.AgentID,
		Name:     name,
		Type:     tt,
		Enabled:  true,
	}

	// Cron fields
	if tt == trigger.TriggerCron {
		expr, _ := args["cron_expression"].(string)
		if expr == "" {
			return "", fmt.Errorf("cron_expression is required for cron triggers")
		}
		t.CronExpression = expr
		tz, _ := args["cron_timezone"].(string)
		if tz == "" {
			tz = "UTC"
		}
		t.CronTimezone = tz
	}

	// Webhook fields — auto-generate path + secret
	var rawSecret string
	if tt == trigger.TriggerWebhook {
		var secretHash string
		rawSecret, secretHash = trigger.GenerateWebhookSecret()
		t.WebhookSecretHash = secretHash
		t.WebhookPath = fmt.Sprintf("%s-%s", truncateID(h.AgentID, 8), triggerRandomHex(8))
	}

	// Event fields
	if tt == trigger.TriggerEvent {
		sourceAgent, _ := args["event_source_agent_id"].(string)
		eventType, _ := args["event_type"].(string)
		if sourceAgent == "" || eventType == "" {
			return "", fmt.Errorf("event_source_agent_id and event_type are required for event triggers")
		}
		t.EventSourceAgentID = sourceAgent
		t.EventType = eventType
	}

	// Optional workflow targeting
	if wfID, ok := args["workflow_id"].(string); ok && wfID != "" {
		t.WorkflowID = wfID
	}

	// Optional shared fields
	if tpl, ok := args["input_template"].(string); ok {
		t.InputTemplate = tpl
	}
	if ts, ok := args["timeout_seconds"].(float64); ok && ts > 0 {
		t.TimeoutSeconds = int(ts)
	} else {
		t.TimeoutSeconds = 300
	}
	if mr, ok := args["max_retries"].(float64); ok && mr >= 0 {
		t.MaxRetries = int(mr)
	}

	if err := h.Store.CreateTrigger(ctx, t); err != nil {
		return "", fmt.Errorf("failed to create trigger: %w", err)
	}

	// Build response
	var sb strings.Builder
	fmt.Fprintf(&sb, "Trigger created successfully.\n")
	fmt.Fprintf(&sb, "  ID:   %s\n", t.ID)
	fmt.Fprintf(&sb, "  Name: %s\n", t.Name)
	fmt.Fprintf(&sb, "  Type: %s\n", t.Type)

	switch tt {
	case trigger.TriggerCron:
		fmt.Fprintf(&sb, "  Cron: %s (%s)\n", t.CronExpression, t.CronTimezone)
	case trigger.TriggerWebhook:
		fmt.Fprintf(&sb, "  Webhook URL:    /v1/triggers/webhook/%s\n", t.WebhookPath)
		fmt.Fprintf(&sb, "  Webhook Secret: %s\n", rawSecret)
		fmt.Fprintf(&sb, "  (Store the secret securely — it will not be shown again.)\n")
	case trigger.TriggerEvent:
		fmt.Fprintf(&sb, "  Source Agent: %s\n", t.EventSourceAgentID)
		fmt.Fprintf(&sb, "  Event Type:  %s\n", t.EventType)
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// list_triggers
// ---------------------------------------------------------------------------

// TriggerListHandler handles the list_triggers synthetic tool.
type TriggerListHandler struct {
	Store    trigger.Store
	TenantID string
	AgentID  string
}

func (h *TriggerListHandler) Name() string { return "list_triggers" }

func (h *TriggerListHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "list_triggers",
			Description: "List all triggers configured for this agent.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (h *TriggerListHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	triggers, err := h.Store.ListTriggers(ctx, h.AgentID, h.TenantID)
	if err != nil {
		return "", fmt.Errorf("failed to list triggers: %w", err)
	}

	if len(triggers) == 0 {
		return "No triggers configured for this agent.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d trigger(s):\n\n", len(triggers))
	for i, t := range triggers {
		fmt.Fprintf(&sb, "%d. %s (ID: %s)\n", i+1, t.Name, t.ID)
		fmt.Fprintf(&sb, "   Type:    %s\n", t.Type)
		fmt.Fprintf(&sb, "   Enabled: %t\n", t.Enabled)
		switch t.Type {
		case trigger.TriggerCron:
			fmt.Fprintf(&sb, "   Cron:    %s (%s)\n", t.CronExpression, t.CronTimezone)
		case trigger.TriggerWebhook:
			fmt.Fprintf(&sb, "   Webhook: /v1/triggers/webhook/%s\n", t.WebhookPath)
		case trigger.TriggerEvent:
			fmt.Fprintf(&sb, "   Source:  %s / %s\n", t.EventSourceAgentID, t.EventType)
		}
		if t.InputTemplate != "" {
			fmt.Fprintf(&sb, "   Input Template: %s\n", t.InputTemplate)
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// delete_trigger
// ---------------------------------------------------------------------------

// TriggerDeleteHandler handles the delete_trigger synthetic tool.
type TriggerDeleteHandler struct {
	Store    trigger.Store
	TenantID string
	AgentID  string
}

func (h *TriggerDeleteHandler) Name() string { return "delete_trigger" }

func (h *TriggerDeleteHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "delete_trigger",
			Description: "Delete a trigger belonging to this agent.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"trigger_id": map[string]interface{}{
						"type":        "string",
						"description": "ID of the trigger to delete.",
					},
				},
				"required": []string{"trigger_id"},
			},
		},
	}
}

func (h *TriggerDeleteHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	triggerID, _ := args["trigger_id"].(string)
	if triggerID == "" {
		return "", fmt.Errorf("trigger_id is required")
	}

	// Ownership check: verify the trigger belongs to this agent + tenant
	existing, err := h.Store.GetTrigger(ctx, triggerID, h.TenantID)
	if err != nil {
		return "", fmt.Errorf("trigger not found: %w", err)
	}
	if existing.AgentID != h.AgentID {
		return "", fmt.Errorf("trigger %s does not belong to this agent", triggerID)
	}

	if err := h.Store.DeleteTrigger(ctx, triggerID, h.TenantID); err != nil {
		return "", fmt.Errorf("failed to delete trigger: %w", err)
	}

	return fmt.Sprintf("Trigger %q (ID: %s) deleted successfully.", existing.Name, triggerID), nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// truncateID returns up to n characters of s.
func truncateID(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// triggerRandomHex generates n random hex characters.
func triggerRandomHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
