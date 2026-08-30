package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// WorkflowsCommandHandler handles workflow create/update/delete commands.
type WorkflowsCommandHandler struct{}

func NewWorkflowsCommandHandler() *WorkflowsCommandHandler { return &WorkflowsCommandHandler{} }

func (h *WorkflowsCommandHandler) CommandType() string {
	return "CreateWorkflow|UpdateWorkflow|DeleteWorkflow"
}

func (h *WorkflowsCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateWorkflowCommand:
		return h.handleCreate(ctx, c)
	case *UpdateWorkflowCommand:
		return h.handleUpdate(ctx, c)
	case *DeleteWorkflowCommand:
		return h.handleDelete(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *WorkflowsCommandHandler) handleCreate(ctx context.Context, cmd *CreateWorkflowCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing workflow create command")

	// Parse nodes/edges/viewport for the event payload
	var nodes interface{} = json.RawMessage("[]")
	if len(cmd.Nodes) > 0 {
		nodes = json.RawMessage(cmd.Nodes)
	}

	var edges interface{} = json.RawMessage("[]")
	if len(cmd.Edges) > 0 {
		edges = json.RawMessage(cmd.Edges)
	}

	var viewport interface{} = json.RawMessage(`{"x":0,"y":0,"zoom":1}`)
	if len(cmd.Viewport) > 0 {
		viewport = json.RawMessage(cmd.Viewport)
	}

	payload := map[string]interface{}{
		"id":             cmd.ID,
		"tenant_id":      tenantID,
		"name":           cmd.Name,
		"description":    cmd.Description,
		"nodes":          nodes,
		"edges":          edges,
		"viewport":       viewport,
		"enabled":        false,
		"version":        1,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "workflow.created",
		Stream:    "workflows",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *WorkflowsCommandHandler) handleUpdate(ctx context.Context, cmd *UpdateWorkflowCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	logger.WithFields(
		"command_id", cmd.ID,
		"workflow_id", cmd.WorkflowID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing workflow update command")

	payload := map[string]interface{}{
		"id":             cmd.WorkflowID,
		"tenant_id":      tenantID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	if cmd.Name != nil {
		payload["name"] = *cmd.Name
	}
	if cmd.Description != nil {
		payload["description"] = *cmd.Description
	}
	if len(cmd.Nodes) > 0 {
		payload["nodes"] = json.RawMessage(cmd.Nodes)
	}
	if len(cmd.Edges) > 0 {
		payload["edges"] = json.RawMessage(cmd.Edges)
	}
	if len(cmd.Viewport) > 0 {
		payload["viewport"] = json.RawMessage(cmd.Viewport)
	}
	if cmd.Enabled != nil {
		payload["enabled"] = *cmd.Enabled
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "workflow.updated",
		Stream:    "workflows",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *WorkflowsCommandHandler) handleDelete(ctx context.Context, cmd *DeleteWorkflowCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	tenantID := cmd.TenantID

	logger.WithFields(
		"command_id", cmd.ID,
		"workflow_id", cmd.WorkflowID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing workflow delete command")

	payload := map[string]interface{}{
		"id":             cmd.WorkflowID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "workflow.deleted",
		Stream:    "workflows",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
