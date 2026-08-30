package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/database"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ChatCommandHandler handles chat completion commands.
type ChatCommandHandler struct{}

// NewChatCommandHandler creates a new chat command handler.
func NewChatCommandHandler() *ChatCommandHandler {
	return &ChatCommandHandler{}
}

// CommandType returns the command type this handler processes.
func (h *ChatCommandHandler) CommandType() string {
	return "ChatCompletion"
}

// Handle processes a ChatCompletionCommand and produces events.
func (h *ChatCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	chatCmd, ok := cmd.(*gateway.ChatCompletionCommand)
	if !ok {
		return nil, fmt.Errorf("invalid command type, expected ChatCompletionCommand")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", chatCmd.ID,
		"model", chatCmd.Model,
		"stream", chatCmd.Stream,
		"message_count", len(chatCmd.Messages),
		"user_id", chatCmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing chat command")

	// Create events based on the command
	events := []database.Event{}

	// hash API key
	hashVal := HashAPIKey(chatCmd.APIKey)
	if hv, ok := apikeylib.HashFromContext(ctx, chatCmd.APIKey); ok {
		hashVal = hv
	}
	// 1. Chat session started event
	sessionStartedPayload := map[string]interface{}{
		"session_id":     chatCmd.ID,
		"user_id":        chatCmd.UserID,
		"api_key":        hashVal, // Hash for privacy
		"model":          chatCmd.Model,
		"provider":       chatCmd.Provider,
		"stream":         chatCmd.Stream,
		"message_count":  len(chatCmd.Messages),
		"temperature":    chatCmd.Temperature,
		"max_tokens":     chatCmd.MaxTokens,
		"correlation_id": correlationID,
		"metadata":       chatCmd.RequestMetadata,
		"started_at":     now.Format(time.RFC3339),
		// Completion fields will be filled when session completes
		"completed_at":  nil,
		"duration_ms":   nil,
		"tokens_used":   nil,
		"success":       nil,
		"error_code":    nil,
		"error_message": nil,
	}

	sessionStartedData, _ := json.Marshal(sessionStartedPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "chat.session.started",
		Stream:    "chat-sessions",
		Payload:   sessionStartedData,
		CreatedAt: now.Unix(),
	})

	// 2. Individual message events
	for i, msg := range chatCmd.Messages {
		messagePayload := map[string]interface{}{
			"session_id":     chatCmd.ID,
			"message_index":  i,
			"role":           msg.Role,
			"content":        msg.Content,
			"name":           msg.Name,
			"extra":          msg.Extra,
			"correlation_id": correlationID,
			"timestamp":      now.Format(time.RFC3339),
		}

		messageData, _ := json.Marshal(messagePayload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "chat.message.processed",
			Stream:    "chat-messages",
			Payload:   messageData,
			CreatedAt: now.Unix(),
		})
	}

	// 3. Model selection event (for load balancer analytics)
	modelSelectionPayload := map[string]interface{}{
		"session_id":      chatCmd.ID,
		"requested_model": chatCmd.Model,
		"user_id":         chatCmd.UserID,
		"api_key_hash":    hashVal,
		"correlation_id":  correlationID,
		"timestamp":       now.Format(time.RFC3339),
	}

	modelSelectionData, _ := json.Marshal(modelSelectionPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "model.selection.requested",
		Stream:    "model-selections",
		Payload:   modelSelectionData,
		CreatedAt: now.Unix(),
	})

	// Internal gateway log - not forwarded to OTEL (CategorySystem)
	logger.WithFields(
		"command_id", chatCmd.ID,
		"event_count", len(events),
		"correlation_id", correlationID,
	).WithCategory(logger.CategorySystem).Info("chat command processed, events generated")

	return events, nil
}

// EmitModelNotFoundEvent creates an event when a requested model is not found/activated.
// This should be called from the gateway processor when a model resolution fails.
func EmitModelNotFoundEvent(ctx context.Context, requestedModel, userID, apiKeyHash, correlationID string) database.Event {
	now := time.Now()
	payload := map[string]interface{}{
		"requested_model": requestedModel,
		"user_id":         userID,
		"api_key_hash":    apiKeyHash,
		"correlation_id":  correlationID,
		"timestamp":       now.Format(time.RFC3339),
		"error_type":      "model_not_found",
	}

	payloadData, _ := json.Marshal(payload)
	return database.Event{
		ID:        uuid.New().String(),
		Type:      "model.not_found",
		Stream:    "model-errors",
		Payload:   payloadData,
		CreatedAt: now.Unix(),
	}
}

// EmitFallbackTriggeredEvent creates an event when fallback routing is initiated.
func EmitFallbackTriggeredEvent(ctx context.Context, requestedModel, reason, userID, apiKeyHash, correlationID string) database.Event {
	now := time.Now()
	payload := map[string]interface{}{
		"requested_model": requestedModel,
		"fallback_reason": reason,
		"user_id":         userID,
		"api_key_hash":    apiKeyHash,
		"correlation_id":  correlationID,
		"timestamp":       now.Format(time.RFC3339),
	}

	payloadData, _ := json.Marshal(payload)
	return database.Event{
		ID:        uuid.New().String(),
		Type:      "fallback.triggered",
		Stream:    "fallback-routing",
		Payload:   payloadData,
		CreatedAt: now.Unix(),
	}
}

// EmitFallbackSucceededEvent creates an event when fallback routing succeeds.
func EmitFallbackSucceededEvent(ctx context.Context, requestedModel, actualModel, reason string, attempts int32, userID, apiKeyHash, correlationID string, durationMs int64) database.Event {
	now := time.Now()
	payload := map[string]interface{}{
		"requested_model":   requestedModel,
		"actual_model":      actualModel,
		"fallback_reason":   reason,
		"fallback_attempts": attempts,
		"duration_ms":       durationMs,
		"user_id":           userID,
		"api_key_hash":      apiKeyHash,
		"correlation_id":    correlationID,
		"timestamp":         now.Format(time.RFC3339),
		"success":           true,
	}

	payloadData, _ := json.Marshal(payload)
	return database.Event{
		ID:        uuid.New().String(),
		Type:      "fallback.succeeded",
		Stream:    "fallback-routing",
		Payload:   payloadData,
		CreatedAt: now.Unix(),
	}
}

// EmitFallbackFailedEvent creates an event when all fallback attempts are exhausted.
func EmitFallbackFailedEvent(ctx context.Context, requestedModel, reason string, attempts int32, userID, apiKeyHash, correlationID string, lastError string) database.Event {
	now := time.Now()
	payload := map[string]interface{}{
		"requested_model":   requestedModel,
		"fallback_reason":   reason,
		"fallback_attempts": attempts,
		"last_error":        lastError,
		"user_id":           userID,
		"api_key_hash":      apiKeyHash,
		"correlation_id":    correlationID,
		"timestamp":         now.Format(time.RFC3339),
		"success":           false,
	}

	payloadData, _ := json.Marshal(payload)
	return database.Event{
		ID:        uuid.New().String(),
		Type:      "fallback.failed",
		Stream:    "fallback-routing",
		Payload:   payloadData,
		CreatedAt: now.Unix(),
	}
}

// EmitSessionErrorEvent creates an event when a chat session fails before processing starts.
// This tracks early failures like model not found, validation errors, etc.
func EmitSessionErrorEvent(ctx context.Context, sessionID, requestedModel, errorType, errorMessage, userID, apiKeyHash, correlationID string) database.Event {
	now := time.Now()
	payload := map[string]interface{}{
		"session_id":      sessionID,
		"requested_model": requestedModel,
		"error_type":      errorType,
		"error_message":   errorMessage,
		"user_id":         userID,
		"api_key_hash":    apiKeyHash,
		"correlation_id":  correlationID,
		"timestamp":       now.Format(time.RFC3339),
		"failed_at":       now.Format(time.RFC3339),
	}

	payloadData, _ := json.Marshal(payload)
	return database.Event{
		ID:        uuid.New().String(),
		Type:      "chat.session.error",
		Stream:    "chat-sessions",
		Payload:   payloadData,
		CreatedAt: now.Unix(),
	}
}

// hashAPIKey creates a consistent hash of the API key for privacy.
func HashAPIKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	// Simple hash for demo - in production use proper cryptographic hash
	hash := 0
	for _, c := range apiKey {
		hash = hash*31 + int(c)
	}
	return fmt.Sprintf("hash_%x", hash)
}
