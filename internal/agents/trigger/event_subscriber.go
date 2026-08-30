package trigger

import (
	"context"
	"encoding/json"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// EventSubscriber listens for agent session events and fires event triggers.
type EventSubscriber struct {
	store    Store
	executor *Executor
	eventBus *database.InMemoryEventBus
}

// NewEventSubscriber creates a new event subscriber.
func NewEventSubscriber(store Store, executor *Executor, eventBus *database.InMemoryEventBus) *EventSubscriber {
	return &EventSubscriber{
		store:    store,
		executor: executor,
		eventBus: eventBus,
	}
}

// Start subscribes to relevant events on the event bus.
func (es *EventSubscriber) Start(ctx context.Context) {
	if es.eventBus == nil {
		logger.Warn("trigger_event_subscriber: event bus not available, skipping")
		return
	}

	// Subscribe to session.end events
	es.eventBus.Subscribe(
		"agent_trigger_session_end",
		"session.end",
		"", // all streams
		func(evtCtx context.Context, event database.Event) error {
			return es.handleEvent(evtCtx, event, "session.end")
		},
	)

	// Subscribe to session.error events
	es.eventBus.Subscribe(
		"agent_trigger_session_error",
		"session.error",
		"", // all streams
		func(evtCtx context.Context, event database.Event) error {
			return es.handleEvent(evtCtx, event, "session.error")
		},
	)

	logger.Info("trigger_event_subscriber: subscribed to session.end and session.error")
}

// Stop unsubscribes from the event bus.
func (es *EventSubscriber) Stop() {
	if es.eventBus == nil {
		return
	}
	es.eventBus.Unsubscribe("agent_trigger_session_end")
	es.eventBus.Unsubscribe("agent_trigger_session_error")
}

func (es *EventSubscriber) handleEvent(ctx context.Context, event database.Event, eventType string) error {
	// Parse event payload
	var eventData map[string]interface{}
	if err := json.Unmarshal(event.Payload, &eventData); err != nil {
		logger.WithFields("event_id", event.ID, "error", err.Error()).
			Warn("trigger_event_subscriber: failed to parse event payload")
		return nil
	}

	// Extract agent_id from event data
	agentID, ok := eventData["agent_id"].(string)
	if !ok || agentID == "" {
		return nil
	}

	// Find matching triggers
	triggers, err := es.store.ListEventTriggers(ctx, agentID, eventType)
	if err != nil {
		logger.WithFields("agent_id", agentID, "event_type", eventType, "error", err.Error()).
			Warn("trigger_event_subscriber: failed to list event triggers")
		return nil // Don't fail the event handler
	}

	if len(triggers) == 0 {
		return nil
	}

	// Build event payload
	payload, _ := json.Marshal(map[string]interface{}{
		"event_type":      eventType,
		"source_agent_id": agentID,
		"session_id":      eventData["session_id"],
		"event_id":        event.ID,
		"data":            eventData,
	})

	for _, t := range triggers {
		// Check event filter if present
		if !es.matchesFilter(t, event) {
			continue
		}

		logger.WithFields("trigger_id", t.ID, "name", t.Name, "source_agent", agentID, "event_type", eventType).
			Info("trigger_event_subscriber: firing event trigger")
		go es.executor.Execute(context.Background(), t, payload)
	}

	return nil
}

// matchesFilter checks if the event matches the trigger's event_filter JSONB.
// For now, supports simple key-value equality matching.
func (es *EventSubscriber) matchesFilter(t *Trigger, event database.Event) bool {
	if len(t.EventFilter) == 0 || string(t.EventFilter) == "null" {
		return true
	}

	var filter map[string]interface{}
	if err := json.Unmarshal(t.EventFilter, &filter); err != nil {
		return true // If filter is invalid, pass through
	}

	var eventData map[string]interface{}
	if err := json.Unmarshal(event.Payload, &eventData); err != nil {
		return true
	}

	for key, expected := range filter {
		actual, ok := eventData[key]
		if !ok {
			return false
		}
		// Simple string equality check
		if expectedStr, ok := expected.(string); ok {
			if actualStr, ok := actual.(string); ok {
				if expectedStr != actualStr {
					return false
				}
			}
		}
	}
	return true
}
