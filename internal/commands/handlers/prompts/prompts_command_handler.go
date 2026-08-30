package prompts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// errMissingTenant mirrors the datasets handler: fail closed when a command
// arrives without tenant context instead of inventing a fallback tenant (the
// anti-pattern from the 2026-05-06 tenant-isolation P0).
var errMissingTenant = fmt.Errorf("tenant id is required (handler invoked without tenant context)")

func resolveTenantID(tenantID string) (string, error) {
	if tenantID == "" {
		return "", errMissingTenant
	}
	return tenantID, nil
}

// PromptsCommandHandler handles prompt and prompt version commands.
type PromptsCommandHandler struct{}

func NewPromptsCommandHandler() *PromptsCommandHandler { return &PromptsCommandHandler{} }

func (h *PromptsCommandHandler) CommandType() string {
	return "CreatePrompt|UpdatePrompt|DeletePrompt|CreatePromptVersion|SetPromptLabels"
}

func (h *PromptsCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreatePromptCommand:
		return h.handleCreatePrompt(ctx, c)
	case *UpdatePromptCommand:
		return h.handleUpdatePrompt(ctx, c)
	case *DeletePromptCommand:
		return h.handleDeletePrompt(ctx, c)
	case *CreatePromptVersionCommand:
		return h.handleCreatePromptVersion(ctx, c)
	case *SetPromptLabelsCommand:
		return h.handleSetPromptLabels(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *PromptsCommandHandler) handleCreatePrompt(ctx context.Context, cmd *CreatePromptCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing prompt create command")

	tags := cmd.Tags
	if tags == nil {
		tags = []string{}
	}

	payload := map[string]interface{}{
		"id":             cmd.ID,
		"tenant_id":      tenantID,
		"name":           cmd.Name,
		"description":    cmd.Description,
		"tags":           tags,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	data, _ := json.Marshal(payload)
	events := []database.Event{{
		ID:        uuid.New().String(),
		Type:      "prompt.created",
		Stream:    "prompts",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}

	if len(cmd.Messages) > 0 {
		versionPayload := map[string]interface{}{
			"id":             cmd.VersionID,
			"prompt_id":      cmd.ID,
			"tenant_id":      tenantID,
			"version":        1,
			"messages":       cmd.Messages,
			"config":         cmd.Config,
			"labels":         []string{},
			"commit_message": cmd.CommitMessage,
			"created_by":     cmd.UserID,
			"created_at":     now.Format(time.RFC3339),
			"correlation_id": correlationID,
		}
		versionData, _ := json.Marshal(versionPayload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "prompt_version.created",
			Stream:    "prompts",
			Payload:   versionData,
			CreatedAt: now.Unix(),
		})
	}

	return events, nil
}

func (h *PromptsCommandHandler) handleUpdatePrompt(ctx context.Context, cmd *UpdatePromptCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"prompt_id", cmd.PromptID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing prompt update command")

	payload := map[string]interface{}{
		"id":             cmd.PromptID,
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
	if cmd.Tags != nil {
		payload["tags"] = *cmd.Tags
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "prompt.updated",
		Stream:    "prompts",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *PromptsCommandHandler) handleDeletePrompt(ctx context.Context, cmd *DeletePromptCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"prompt_id", cmd.PromptID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing prompt delete command")

	payload := map[string]interface{}{
		"id":             cmd.PromptID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "prompt.deleted",
		Stream:    "prompts",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *PromptsCommandHandler) handleCreatePromptVersion(ctx context.Context, cmd *CreatePromptVersionCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"prompt_id", cmd.PromptID,
		"version", cmd.Version,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing prompt version create command")

	labels := cmd.Labels
	if labels == nil {
		labels = []string{}
	}

	payload := map[string]interface{}{
		"id":             cmd.ID,
		"prompt_id":      cmd.PromptID,
		"tenant_id":      tenantID,
		"version":        cmd.Version,
		"messages":       cmd.Messages,
		"config":         cmd.Config,
		"labels":         labels,
		"commit_message": cmd.CommitMessage,
		"created_by":     cmd.UserID,
		"created_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	events := []database.Event{{
		ID:        uuid.New().String(),
		Type:      "prompt_version.created",
		Stream:    "prompts",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}

	// Moving labels onto a brand-new version means stripping them from
	// siblings; reuse the labels_set projection for that.
	if len(labels) > 0 {
		labelPayload := map[string]interface{}{
			"prompt_id":      cmd.PromptID,
			"tenant_id":      tenantID,
			"version":        cmd.Version,
			"labels":         labels,
			"updated_at":     now.Format(time.RFC3339),
			"correlation_id": correlationID,
		}
		labelData, _ := json.Marshal(labelPayload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "prompt_version.labels_set",
			Stream:    "prompts",
			Payload:   labelData,
			CreatedAt: now.Unix(),
		})
	}

	return events, nil
}

func (h *PromptsCommandHandler) handleSetPromptLabels(ctx context.Context, cmd *SetPromptLabelsCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"prompt_id", cmd.PromptID,
		"version", cmd.Version,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing prompt labels set command")

	labels := cmd.Labels
	if labels == nil {
		labels = []string{}
	}

	payload := map[string]interface{}{
		"prompt_id":      cmd.PromptID,
		"tenant_id":      tenantID,
		"version":        cmd.Version,
		"labels":         labels,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "prompt_version.labels_set",
		Stream:    "prompts",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
