package functions

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

// FunctionsCommandHandler handles function create/update/delete commands.
type FunctionsCommandHandler struct{}

func NewFunctionsCommandHandler() *FunctionsCommandHandler { return &FunctionsCommandHandler{} }

func (h *FunctionsCommandHandler) CommandType() string {
	return "CreateFunction|UpdateFunction|DeleteFunction"
}

func (h *FunctionsCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateFunctionCommand:
		return h.handleCreate(ctx, c)
	case *UpdateFunctionCommand:
		return h.handleUpdate(ctx, c)
	case *DeleteFunctionCommand:
		return h.handleDelete(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *FunctionsCommandHandler) handleCreate(ctx context.Context, cmd *CreateFunctionCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	// Use "default" tenant for self-hosted mode
	// Use a fixed UUID for self-hosted mode (single tenant)
	// This UUID is deterministic: uuid.NewSHA1(uuid.NameSpaceDNS, []byte("self-hosted"))
	tenantID := cmd.TenantID
	if tenantID == "" {
		tenantID = "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"mode", cmd.Mode,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing function create command")

	payload := map[string]interface{}{
		"id":             uuid.New().String(),
		"tenant_id":      tenantID,
		"name":           cmd.Name,
		"description":    cmd.Description,
		"mode":           cmd.Mode,
		"parameters":     cmd.Parameters,
		"timeout_ms":     cmd.TimeoutMs,
		"memory_mb":      cmd.MemoryMB,
		"max_retries":    cmd.MaxRetries,
		"enabled":        true,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	// Add mode-specific config
	if cmd.Webhook != nil {
		payload["webhook"] = cmd.Webhook
	}
	if cmd.Proxy != nil {
		payload["proxy"] = cmd.Proxy
	}
	if cmd.Isolated != nil {
		payload["isolated"] = cmd.Isolated
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "function.created",
		Stream:    "functions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *FunctionsCommandHandler) handleUpdate(ctx context.Context, cmd *UpdateFunctionCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	// Use "default" tenant for self-hosted mode
	// Use a fixed UUID for self-hosted mode (single tenant)
	// This UUID is deterministic: uuid.NewSHA1(uuid.NameSpaceDNS, []byte("self-hosted"))
	tenantID := cmd.TenantID
	if tenantID == "" {
		tenantID = "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"function_id", cmd.FunctionID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing function update command")

	payload := map[string]interface{}{
		"id":             cmd.FunctionID,
		"tenant_id":      tenantID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	// Add optional fields if provided
	if cmd.Name != nil {
		payload["name"] = *cmd.Name
	}
	if cmd.Description != nil {
		payload["description"] = *cmd.Description
	}
	if cmd.Mode != nil {
		payload["mode"] = *cmd.Mode
	}
	if cmd.Parameters != nil {
		payload["parameters"] = cmd.Parameters
	}
	if cmd.Webhook != nil {
		payload["webhook"] = cmd.Webhook
	}
	if cmd.Proxy != nil {
		payload["proxy"] = cmd.Proxy
	}
	if cmd.Isolated != nil {
		payload["isolated"] = cmd.Isolated
	}
	if cmd.TimeoutMs != nil {
		payload["timeout_ms"] = *cmd.TimeoutMs
	}
	if cmd.MemoryMB != nil {
		payload["memory_mb"] = *cmd.MemoryMB
	}
	if cmd.MaxRetries != nil {
		payload["max_retries"] = *cmd.MaxRetries
	}
	if cmd.Enabled != nil {
		payload["enabled"] = *cmd.Enabled
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "function.updated",
		Stream:    "functions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *FunctionsCommandHandler) handleDelete(ctx context.Context, cmd *DeleteFunctionCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	// Use "default" tenant for self-hosted mode
	// Use a fixed UUID for self-hosted mode (single tenant)
	// This UUID is deterministic: uuid.NewSHA1(uuid.NameSpaceDNS, []byte("self-hosted"))
	tenantID := cmd.TenantID
	if tenantID == "" {
		tenantID = "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"function_id", cmd.FunctionID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing function delete command")

	payload := map[string]interface{}{
		"id":             cmd.FunctionID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "function.deleted",
		Stream:    "functions",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
