package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ModelConfigCommandHandler handles model configuration commands.
type ModelConfigCommandHandler struct{}

// NewModelConfigCommandHandler creates a new model config command handler.
func NewModelConfigCommandHandler() *ModelConfigCommandHandler {
	return &ModelConfigCommandHandler{}
}

// CommandType returns the command type this handler processes.
func (h *ModelConfigCommandHandler) CommandType() string {
	return "ConfigureModel"
}

// Handle processes a ConfigureModelCommand and produces events.
func (h *ModelConfigCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	configCmd, ok := cmd.(*gateway.ConfigureModelCommand)
	if !ok {
		return nil, fmt.Errorf("invalid command type, expected ConfigureModelCommand")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", configCmd.ID,
		"provider", configCmd.Provider,
		"model_id", configCmd.ModelID,
		"alias", configCmd.Alias,
		"enabled", configCmd.Enabled,
		"user_id", configCmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing model configuration command")

	// Create events based on the command
	events := []database.Event{}

	// 1. Model configuration changed event
	configChangedPayload := map[string]interface{}{
		"config_id":      configCmd.ID,
		"provider":       configCmd.Provider,
		"model_id":       configCmd.ModelID,
		"alias":          configCmd.Alias,
		"config":         configCmd.Config,
		"enabled":        configCmd.Enabled,
		"user_id":        configCmd.UserID,
		"correlation_id": correlationID,
		"changed_at":     now.Format(time.RFC3339),
		"version":        1, // Could be incremented for updates
	}

	configChangedData, _ := json.Marshal(configChangedPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "model.config.changed",
		Stream:    "model-configs",
		Payload:   configChangedData,
		CreatedAt: now.Unix(),
	})

	// 2. Model availability event (if enabled/disabled)
	availabilityPayload := map[string]interface{}{
		"provider":       configCmd.Provider,
		"model_id":       configCmd.ModelID,
		"alias":          configCmd.Alias,
		"available":      configCmd.Enabled,
		"correlation_id": correlationID,
		"timestamp":      now.Format(time.RFC3339),
	}

	availabilityData, _ := json.Marshal(availabilityPayload)
	eventType := "model.enabled"
	if !configCmd.Enabled {
		eventType = "model.disabled"
	}

	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      eventType,
		Stream:    "model-availability",
		Payload:   availabilityData,
		CreatedAt: now.Unix(),
	})

	// 3. Configuration audit event
	auditPayload := map[string]interface{}{
		"action":        "configure_model",
		"resource_type": "model",
		"resource_id":   fmt.Sprintf("%s:%s", configCmd.Provider, configCmd.ModelID),
		"user_id":       configCmd.UserID,
		"changes": map[string]interface{}{
			"provider": configCmd.Provider,
			"model_id": configCmd.ModelID,
			"alias":    configCmd.Alias,
			"enabled":  configCmd.Enabled,
			"config":   configCmd.Config,
		},
		"correlation_id": correlationID,
		"timestamp":      now.Format(time.RFC3339),
	}

	auditData, _ := json.Marshal(auditPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "audit.configuration.changed",
		Stream:    "audit-logs",
		Payload:   auditData,
		CreatedAt: now.Unix(),
	})

	logger.WithFields(
		"command_id", configCmd.ID,
		"event_count", len(events),
		"correlation_id", correlationID,
	).Info("model configuration command processed, events generated")

	return events, nil
}
