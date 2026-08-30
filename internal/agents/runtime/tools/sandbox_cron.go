package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/robfig/cron/v3"
)

// SandboxCronHandler exposes cron scheduling to agents. Agents can create,
// list, and delete scheduled jobs that run commands in their sandbox.
type SandboxCronHandler struct {
	Ctx *SandboxSessionContext

	// Channel notification context — when set, crons created by this handler
	// will send notifications back to the originating channel.
	ChannelConfigID string
	ChannelRef      string
	ThreadRef       string
}

func (h *SandboxCronHandler) Name() string { return "schedule_cron" }

func (h *SandboxCronHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name: "schedule_cron",
			Description: `Create, list, or delete scheduled cron jobs. When called from a channel (Slack/Discord), notifications are automatically sent back to the channel when the cron fires.

Actions:
- "create": Schedule a new recurring job. Requires name, schedule (cron expression), and command.
- "list": List all cron jobs for this session.
- "delete": Delete a cron job by its ID.

Cron expression format: "minute hour day-of-month month day-of-week"
Examples: "0 9 * * *" (daily at 9am), "*/5 * * * *" (every 5 minutes), "0 9 * * 1" (every Monday at 9am)

Use notify_message to set a custom notification message. If not set, the command output is used.`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"create", "list", "delete"},
						"description": "The action to perform.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name/description for the cron job (required for create).",
					},
					"schedule": map[string]interface{}{
						"type":        "string",
						"description": "Cron expression (required for create). Format: 'minute hour day-of-month month day-of-week'.",
					},
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute (required for create).",
					},
					"notify_message": map[string]interface{}{
						"type":        "string",
						"description": "Custom notification message sent to the channel when the cron fires. If omitted, the command's stdout output is sent instead.",
					},
					"cron_id": map[string]interface{}{
						"type":        "integer",
						"description": "Cron job ID (required for delete).",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

func (h *SandboxCronHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)

	switch action {
	case "create":
		return h.createCron(ctx, args)
	case "list":
		return h.listCrons(ctx)
	case "delete":
		return h.deleteCron(ctx, args)
	default:
		return "", fmt.Errorf("unknown action: %s (use create, list, or delete)", action)
	}
}

func (h *SandboxCronHandler) createCron(ctx context.Context, args map[string]interface{}) (string, error) {
	name, _ := args["name"].(string)
	schedule, _ := args["schedule"].(string)
	command, _ := args["command"].(string)
	notifyMessage, _ := args["notify_message"].(string)

	if name == "" || schedule == "" || command == "" {
		return "", fmt.Errorf("name, schedule, and command are all required for create")
	}

	// Validate cron expression
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	cronSchedule, err := parser.Parse(schedule)
	if err != nil {
		return "", fmt.Errorf("invalid cron expression %q: %w", schedule, err)
	}

	db := h.Ctx.Manager.DB()
	if db == nil {
		return "", fmt.Errorf("database not available")
	}

	// Get sandbox ID for this session
	sandboxID, err := h.resolveSandboxID(ctx)
	if err != nil {
		return "", err
	}

	nextRun := cronSchedule.Next(time.Now())

	// Use channel context from the handler (populated for channel sessions)
	var channelConfigID, channelRef, threadRef, notifyMsg *string
	if h.ChannelConfigID != "" {
		channelConfigID = &h.ChannelConfigID
		channelRef = &h.ChannelRef
		threadRef = &h.ThreadRef
	}
	if notifyMessage != "" {
		notifyMsg = &notifyMessage
	}

	const q = `
		INSERT INTO sandbox_crons
			(tenant_id, sandbox_id, session_id, name, schedule, command, work_dir, timeout_seconds,
			 auto_recreate, sandbox_config, next_run_at, channel_config_id, channel_ref, thread_ref, notify_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`

	var id int64
	err = db.GetContext(ctx, &id, q,
		h.Ctx.TenantID, sandboxID, h.Ctx.SessionID,
		name, schedule, command,
		"/workspace", 300, true, []byte("{}"), nextRun,
		channelConfigID, channelRef, threadRef, notifyMsg,
	)
	if err != nil {
		return "", fmt.Errorf("create cron: %w", err)
	}

	notifyStatus := "disabled"
	if channelConfigID != nil {
		notifyStatus = "enabled (messages will be sent to this channel)"
	}

	return fmt.Sprintf("Cron job created successfully.\nID: %d\nName: %s\nSchedule: %s\nCommand: %s\nNext run: %s\nChannel notification: %s",
		id, name, schedule, command, nextRun.Format(time.RFC3339), notifyStatus), nil
}

func (h *SandboxCronHandler) listCrons(ctx context.Context) (string, error) {
	db := h.Ctx.Manager.DB()
	if db == nil {
		return "", fmt.Errorf("database not available")
	}

	var crons []sandbox.SandboxCron
	err := db.SelectContext(ctx, &crons,
		`SELECT id, name, schedule, command, enabled, last_run_at, next_run_at, run_count, error_count
		 FROM sandbox_crons
		 WHERE session_id = $1
		 ORDER BY created_at DESC`,
		h.Ctx.SessionID,
	)
	if err != nil {
		return "", fmt.Errorf("list crons: %w", err)
	}

	if len(crons) == 0 {
		return "No cron jobs found for this session.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d cron job(s):\n\n", len(crons)))
	for _, c := range crons {
		status := "enabled"
		if !c.Enabled {
			status = "disabled"
		}
		nextRun := "not scheduled"
		if c.NextRunAt != nil {
			nextRun = c.NextRunAt.Format(time.RFC3339)
		}
		sb.WriteString(fmt.Sprintf("ID: %d | %s | %s\n  Schedule: %s\n  Command: %s\n  Runs: %d | Errors: %d | Next: %s\n\n",
			c.ID, c.Name, status, c.Schedule, c.Command, c.RunCount, c.ErrorCount, nextRun))
	}

	return sb.String(), nil
}

func (h *SandboxCronHandler) deleteCron(ctx context.Context, args map[string]interface{}) (string, error) {
	cronID, ok := args["cron_id"].(float64)
	if !ok || cronID <= 0 {
		return "", fmt.Errorf("cron_id is required for delete")
	}

	db := h.Ctx.Manager.DB()
	if db == nil {
		return "", fmt.Errorf("database not available")
	}

	result, err := db.ExecContext(ctx,
		`DELETE FROM sandbox_crons WHERE id = $1 AND session_id = $2`,
		int64(cronID), h.Ctx.SessionID,
	)
	if err != nil {
		return "", fmt.Errorf("delete cron: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return "No cron job found with that ID in this session.", nil
	}

	return fmt.Sprintf("Cron job %d deleted successfully.", int64(cronID)), nil
}

func (h *SandboxCronHandler) resolveSandboxID(ctx context.Context) (string, error) {
	// Try to get sandbox ID from the manager's active instances
	instances := h.Ctx.Manager.ListInstances()
	for _, inst := range instances {
		if inst.Config.SessionID == h.Ctx.SessionID {
			return inst.ID, nil
		}
	}

	// No sandbox running — create one so the cron has a valid sandbox_id
	inst, err := h.Ctx.Manager.GetOrCreate(ctx, h.Ctx.SessionID, h.Ctx.TenantID, h.Ctx.Config)
	if err != nil {
		return "", fmt.Errorf("ensure sandbox for cron: %w", err)
	}
	return inst.ID, nil
}
