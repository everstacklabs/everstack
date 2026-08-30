package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway/chat"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// EmbeddingCommandHandler handles embedding generation commands.
type EmbeddingCommandHandler struct{}

// NewEmbeddingCommandHandler creates a new embedding command handler.
func NewEmbeddingCommandHandler() *EmbeddingCommandHandler {
	return &EmbeddingCommandHandler{}
}

// CommandType returns the command type this handler processes.
func (h *EmbeddingCommandHandler) CommandType() string {
	return "ProcessEmbedding"
}

// Handle processes a ProcessEmbeddingCommand and produces events.
func (h *EmbeddingCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	embeddingCmd, ok := cmd.(*gateway.ProcessEmbeddingCommand)
	if !ok {
		return nil, fmt.Errorf("invalid command type, expected ProcessEmbeddingCommand")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", embeddingCmd.ID,
		"model", embeddingCmd.Model,
		"input_count", len(embeddingCmd.Input),
		"user_id", embeddingCmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing embedding command")

	// Create events based on the command
	events := []database.Event{}

	// 1. Embedding request started event
	requestStartedPayload := map[string]interface{}{
		"request_id":      embeddingCmd.ID,
		"user_id":         embeddingCmd.UserID,
		"api_key":         chat.HashAPIKey(embeddingCmd.APIKey),
		"model":           embeddingCmd.Model,
		"input_count":     len(embeddingCmd.Input),
		"encoding_format": embeddingCmd.EncodingFormat,
		"dimensions":      embeddingCmd.Dimensions,
		"correlation_id":  correlationID,
		"metadata":        embeddingCmd.RequestMetadata,
		"started_at":      now.Format(time.RFC3339),
	}

	requestStartedData, _ := json.Marshal(requestStartedPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "embedding.request.started",
		Stream:    "embedding-requests",
		Payload:   requestStartedData,
		CreatedAt: now.Unix(),
	})

	// 2. Individual input processing events
	for i, input := range embeddingCmd.Input {
		inputPayload := map[string]interface{}{
			"request_id":     embeddingCmd.ID,
			"input_index":    i,
			"input_text":     input,
			"input_length":   len(input),
			"correlation_id": correlationID,
			"timestamp":      now.Format(time.RFC3339),
		}

		inputData, _ := json.Marshal(inputPayload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "embedding.input.processed",
			Stream:    "embedding-inputs",
			Payload:   inputData,
			CreatedAt: now.Unix(),
		})
	}

	// 3. Model selection event
	modelSelectionPayload := map[string]interface{}{
		"request_id":      embeddingCmd.ID,
		"requested_model": embeddingCmd.Model,
		"user_id":         embeddingCmd.UserID,
		"api_key_hash":    chat.HashAPIKey(embeddingCmd.APIKey),
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

	logger.WithFields(
		"command_id", embeddingCmd.ID,
		"event_count", len(events),
		"correlation_id", correlationID,
	).Info("embedding command processed, events generated")

	return events, nil
}
