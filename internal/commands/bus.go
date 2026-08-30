package commands

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// DefaultCommandBus implements CommandBus with event persistence.
type DefaultCommandBus struct {
	handlers map[string]CommandHandler
	writer   database.Writer
	bus      database.Bus // Optional event bus for publishing
	mu       sync.RWMutex
}

// NewCommandBus creates a new command bus with the given writer and optional event bus.
func NewCommandBus(writer database.Writer, bus database.Bus) *DefaultCommandBus {
	return &DefaultCommandBus{
		handlers: make(map[string]CommandHandler),
		writer:   writer,
		bus:      bus,
	}
}

// RegisterHandler registers a command handler for a specific command type.
func (cb *DefaultCommandBus) RegisterHandler(handler CommandHandler) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cmdType := handler.CommandType()
	// Support multi-type registration separated by '|'
	for _, t := range strings.Split(cmdType, "|") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		cb.handlers[t] = handler
		logger.Debugf("registered command handler for type: %s", t)
	}
}

// Dispatch routes a command to its handler and persists resulting events.
func (cb *DefaultCommandBus) Dispatch(ctx context.Context, cmd Command) error {
	start := time.Now()
	correlationID := correlation.GetCorrelationID(ctx)

	// Validate command
	if err := cmd.Validate(); err != nil {
		logger.WithFields(
			"command_type", cmd.CommandType(),
			"aggregate_id", cmd.AggregateID(),
			"correlation_id", correlationID,
			"error", err.Error(),
		).Warn("command validation failed")
		return fmt.Errorf("command validation failed: %w", err)
	}

	// Find handler
	cb.mu.RLock()
	handler, exists := cb.handlers[cmd.CommandType()]
	cb.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no handler registered for command type: %s", cmd.CommandType())
	}

	logger.WithFields(
		"command_type", cmd.CommandType(),
		"aggregate_id", cmd.AggregateID(),
		"correlation_id", correlationID,
	).Debug("dispatching command")

	// Handle command
	events, err := handler.Handle(ctx, cmd)
	if err != nil {
		logger.WithFields(
			"command_type", cmd.CommandType(),
			"aggregate_id", cmd.AggregateID(),
			"correlation_id", correlationID,
			"error", err.Error(),
			"elapsed_ms", time.Since(start).Milliseconds(),
		).Error("command handling failed")
		return fmt.Errorf("command handling failed: %w", err)
	}

	// Persist events
	if len(events) > 0 && cb.writer != nil {
		if err := cb.writer.Append(ctx, events...); err != nil {
			logger.WithFields(
				"command_type", cmd.CommandType(),
				"aggregate_id", cmd.AggregateID(),
				"correlation_id", correlationID,
				"event_count", len(events),
				"error", err.Error(),
			).Error("event persistence failed")
			return fmt.Errorf("event persistence failed: %w", err)
		}
	}

	// Publish events to bus (optional)
	if len(events) > 0 && cb.bus != nil {
		if err := cb.bus.Publish(ctx, events...); err != nil {
			logger.WithFields(
				"command_type", cmd.CommandType(),
				"aggregate_id", cmd.AggregateID(),
				"correlation_id", correlationID,
				"event_count", len(events),
				"error", err.Error(),
			).Error("required event handling failed after persistence")
			if cb.writer != nil {
				return &PostCommitError{Err: err}
			}
			return fmt.Errorf("event handling failed: %w", err)
		}
	}

	// Build structured payload for command completion
	payload := logger.NewPayload().
		WithCommand(cmd.AggregateID(), cmd.CommandType(), "completed", time.Since(start).Milliseconds()).
		WithCorrelation(correlationID).
		Build()

	// Internal gateway log - not forwarded to OTEL (CategorySystem)
	logger.WithCategory(logger.CategorySystem).
		WithLogEvent(logger.EventCommandCompleted).
		WithPayload(payload).
		SetFields(
			"command_type", cmd.CommandType(),
			"aggregate_id", cmd.AggregateID(),
			"correlation_id", correlationID,
			"event_count", len(events),
			"elapsed_ms", time.Since(start).Milliseconds(),
		).Debug("command processed successfully")

	return nil
}

// GetRegisteredHandlers returns a list of registered command types.
func (cb *DefaultCommandBus) GetRegisteredHandlers() []string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	types := make([]string, 0, len(cb.handlers))
	for cmdType := range cb.handlers {
		types = append(types, cmdType)
	}
	return types
}
