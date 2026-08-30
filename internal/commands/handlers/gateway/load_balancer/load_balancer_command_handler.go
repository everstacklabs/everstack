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

// LoadBalancerCommandHandler handles load balancer configuration commands.
type LoadBalancerCommandHandler struct{}

// NewLoadBalancerCommandHandler creates a new load balancer command handler.
func NewLoadBalancerCommandHandler() *LoadBalancerCommandHandler {
	return &LoadBalancerCommandHandler{}
}

// CommandType returns the command type this handler processes.
func (h *LoadBalancerCommandHandler) CommandType() string {
	return "UpdateLoadBalancer"
}

// Handle processes an UpdateLoadBalancerCommand and produces events.
func (h *LoadBalancerCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	lbCmd, ok := cmd.(*gateway.UpdateLoadBalancerCommand)
	if !ok {
		return nil, fmt.Errorf("invalid command type, expected UpdateLoadBalancerCommand")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()

	logger.WithFields(
		"command_id", lbCmd.ID,
		"strategy", lbCmd.Strategy,
		"key_source", lbCmd.KeySource,
		"enabled", lbCmd.Enabled,
		"user_id", lbCmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing load balancer configuration command")

	// Create events based on the command
	events := []database.Event{}

	// 1. Load balancer configuration changed event
	configChangedPayload := map[string]interface{}{
		"config_id":      lbCmd.ID,
		"strategy":       lbCmd.Strategy,
		"key_source":     lbCmd.KeySource,
		"weights":        lbCmd.Weights,
		"enabled":        lbCmd.Enabled,
		"user_id":        lbCmd.UserID,
		"correlation_id": correlationID,
		"changed_at":     now.Format(time.RFC3339),
		"version":        1, // Could be incremented for updates
	}

	configChangedData, _ := json.Marshal(configChangedPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "load_balancer.config.changed",
		Stream:    "load-balancer-configs",
		Payload:   configChangedData,
		CreatedAt: now.Unix(),
	})

	// 2. Strategy change event (if applicable)
	strategyPayload := map[string]interface{}{
		"old_strategy":   "", // Would need to be passed in or looked up
		"new_strategy":   lbCmd.Strategy,
		"key_source":     lbCmd.KeySource,
		"correlation_id": correlationID,
		"timestamp":      now.Format(time.RFC3339),
	}

	strategyData, _ := json.Marshal(strategyPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "load_balancer.strategy.changed",
		Stream:    "load-balancer-events",
		Payload:   strategyData,
		CreatedAt: now.Unix(),
	})

	// 3. Weights update event (if weights provided)
	if len(lbCmd.Weights) > 0 {
		weightsPayload := map[string]interface{}{
			"weights":        lbCmd.Weights,
			"strategy":       lbCmd.Strategy,
			"correlation_id": correlationID,
			"timestamp":      now.Format(time.RFC3339),
		}

		weightsData, _ := json.Marshal(weightsPayload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "load_balancer.weights.updated",
			Stream:    "load-balancer-events",
			Payload:   weightsData,
			CreatedAt: now.Unix(),
		})
	}

	// 4. Load balancer status event
	statusPayload := map[string]interface{}{
		"enabled":        lbCmd.Enabled,
		"strategy":       lbCmd.Strategy,
		"key_source":     lbCmd.KeySource,
		"correlation_id": correlationID,
		"timestamp":      now.Format(time.RFC3339),
	}

	statusData, _ := json.Marshal(statusPayload)
	eventType := "load_balancer.enabled"
	if !lbCmd.Enabled {
		eventType = "load_balancer.disabled"
	}

	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      eventType,
		Stream:    "load-balancer-status",
		Payload:   statusData,
		CreatedAt: now.Unix(),
	})

	// 5. Configuration audit event
	auditPayload := map[string]interface{}{
		"action":        "update_load_balancer",
		"resource_type": "load_balancer",
		"resource_id":   "global",
		"user_id":       lbCmd.UserID,
		"changes": map[string]interface{}{
			"strategy":   lbCmd.Strategy,
			"key_source": lbCmd.KeySource,
			"weights":    lbCmd.Weights,
			"enabled":    lbCmd.Enabled,
		},
		"correlation_id": correlationID,
		"timestamp":      now.Format(time.RFC3339),
	}

	auditData, _ := json.Marshal(auditPayload)
	events = append(events, database.Event{
		ID:        uuid.New().String(),
		Type:      "audit.load_balancer.changed",
		Stream:    "audit-logs",
		Payload:   auditData,
		CreatedAt: now.Unix(),
	})

	logger.WithFields(
		"command_id", lbCmd.ID,
		"event_count", len(events),
		"correlation_id", correlationID,
	).Info("load balancer configuration command processed, events generated")

	return events, nil
}
