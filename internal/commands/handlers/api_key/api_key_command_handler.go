package api_key

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway/chat"
	"github.com/everstacklabs/everstack/internal/database"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/utils"
)

// ApiKeyCommandHandler handles API key create/revoke commands.
type ApiKeyCommandHandler struct{}

func NewApiKeyCommandHandler() *ApiKeyCommandHandler { return &ApiKeyCommandHandler{} }

func (h *ApiKeyCommandHandler) CommandType() string { return "CreateApiKey|RevokeApiKey" }

func (h *ApiKeyCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateApiKeyCommand:
		return h.handleCreate(ctx, c)
	case *RevokeApiKeyCommand:
		return h.handleRevoke(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *ApiKeyCommandHandler) handleCreate(ctx context.Context, cmd *CreateApiKeyCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"type", cmd.Type,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing api key create command")

	// compute hash
	hashVal := chat.HashAPIKey(cmd.Plaintext)
	if hv, ok := apikeylib.HashFromContext(ctx, cmd.Plaintext); ok {
		hashVal = hv
	}
	
	// create masked version for display
	maskedKey := utils.MaskApiKey(cmd.Plaintext)
	
	payload := map[string]interface{}{
		"id":             uuid.New().String(),
		"name":           cmd.Name,
		"hash":           hashVal,
		"type":           cmd.Type,
		"sensitive_id":   maskedKey,
		"user_id":        cmd.UserID,
		"org_id":         cmd.OrgID,
		"instance_id":    cmd.InstanceID,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "api.key.created",
		Stream:    "api-keys",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *ApiKeyCommandHandler) handleRevoke(ctx context.Context, cmd *RevokeApiKeyCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", cmd.ID,
		"key_id", cmd.KeyID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing api key revoke command")

	payload := map[string]interface{}{
		"id":             cmd.KeyID,
		"org_id":         cmd.OrgID,
		"revoked":        true,
		"revoked_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "api.key.revoked",
		Stream:    "api-keys",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
