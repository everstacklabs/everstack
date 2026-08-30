package database

import (
	"context"
	"fmt"
	"sync"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// EventHandler represents a function that handles domain events.
type EventHandler func(ctx context.Context, event Event) error

// EventSubscription represents a subscription to events.
type EventSubscription struct {
	ID          string
	EventType   string
	EventStream string
	Handler     EventHandler
	Active      bool
	Critical    bool
}

// InMemoryEventBus implements the Bus interface for in-process event publishing.
type InMemoryEventBus struct {
	subscriptions map[string]*EventSubscription
	subscribers   map[string][]string // eventType -> []subscriptionID
	mu            sync.RWMutex
}

// NewInMemoryEventBus creates a new in-memory event bus.
func NewInMemoryEventBus() *InMemoryEventBus {
	return &InMemoryEventBus{
		subscriptions: make(map[string]*EventSubscription),
		subscribers:   make(map[string][]string),
	}
}

// Subscribe registers an event handler for specific event types.
func (bus *InMemoryEventBus) Subscribe(subscriptionID, eventType, eventStream string, handler EventHandler) error {
	return bus.subscribe(subscriptionID, eventType, eventStream, handler, false)
}

// SubscribeCritical registers a handler that must complete successfully before
// Publish returns.
func (bus *InMemoryEventBus) SubscribeCritical(subscriptionID, eventType, eventStream string, handler EventHandler) error {
	return bus.subscribe(subscriptionID, eventType, eventStream, handler, true)
}

func (bus *InMemoryEventBus) subscribe(subscriptionID, eventType, eventStream string, handler EventHandler, critical bool) error {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	subscription := &EventSubscription{
		ID:          subscriptionID,
		EventType:   eventType,
		EventStream: eventStream,
		Handler:     handler,
		Active:      true,
		Critical:    critical,
	}

	bus.subscriptions[subscriptionID] = subscription

	// Add to event type mapping
	if bus.subscribers[eventType] == nil {
		bus.subscribers[eventType] = make([]string, 0)
	}
	bus.subscribers[eventType] = append(bus.subscribers[eventType], subscriptionID)

	logger.WithFields(
		"subscription_id", subscriptionID,
		"event_type", eventType,
		"event_stream", eventStream,
	).Debug("event subscription registered")

	return nil
}

// Unsubscribe removes an event handler.
func (bus *InMemoryEventBus) Unsubscribe(subscriptionID string) error {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	subscription, exists := bus.subscriptions[subscriptionID]
	if !exists {
		return fmt.Errorf("subscription not found: %s", subscriptionID)
	}

	// Remove from subscriptions
	delete(bus.subscriptions, subscriptionID)

	// Remove from event type mapping
	eventType := subscription.EventType
	if subscribers, exists := bus.subscribers[eventType]; exists {
		for i, id := range subscribers {
			if id == subscriptionID {
				bus.subscribers[eventType] = append(subscribers[:i], subscribers[i+1:]...)
				break
			}
		}
		// Clean up empty mappings
		if len(bus.subscribers[eventType]) == 0 {
			delete(bus.subscribers, eventType)
		}
	}

	logger.WithFields(
		"subscription_id", subscriptionID,
		"event_type", subscription.EventType,
	).Info("event subscription removed")

	return nil
}

// Publish publishes events to all registered handlers.
func (bus *InMemoryEventBus) Publish(ctx context.Context, events ...Event) error {
	if len(events) == 0 {
		return nil
	}

	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"event_count", len(events),
		"correlation_id", correlationID,
	).Debug("publishing events to bus")

	for _, event := range events {
		subscriptions := bus.subscriptionsFor(event)
		if len(subscriptions) == 0 {
			logger.WithFields(
				"event_type", event.Type,
				"event_id", event.ID,
				"correlation_id", correlationID,
			).Debug("no subscribers for event type")
			continue
		}

		for _, subscription := range subscriptions {
			subscription := subscription
			if subscription.Critical {
				if err := bus.handle(ctx, correlationID, &subscription, event); err != nil {
					return fmt.Errorf("critical event handler %s failed: %w", subscription.ID, err)
				}
				continue
			}
			go func() { _ = bus.handle(ctx, correlationID, &subscription, event) }()
		}
	}

	// Build structured payload for event publication
	payload := logger.NewPayload().
		WithCorrelation(correlationID).
		WithLogging("system", "event_publication", "info", "event_bus", "events published to bus").
		Build()

	// Internal gateway log - not forwarded to OTEL (CategorySystem)
	logger.WithCategory(logger.CategorySystem).
		WithLogEvent("event.bus.published").
		WithPayload(payload).
		SetFields(
			"event_count", len(events),
			"correlation_id", correlationID,
		).Debug("events published to bus")

	return nil
}

func (bus *InMemoryEventBus) subscriptionsFor(event Event) []EventSubscription {
	bus.mu.RLock()
	defer bus.mu.RUnlock()

	subscriberIDs := bus.subscribers[event.Type]
	result := make([]EventSubscription, 0, len(subscriberIDs))
	for _, subID := range subscriberIDs {
		subscription, exists := bus.subscriptions[subID]
		if !exists || !subscription.Active {
			continue
		}
		if subscription.EventStream != "" && subscription.EventStream != event.Stream {
			continue
		}
		result = append(result, *subscription)
	}
	return result
}

func (bus *InMemoryEventBus) handle(ctx context.Context, correlationID string, sub *EventSubscription, evt Event) error {
	start := time.Now()
	logger.WithFields(
		"subscription_id", sub.ID,
		"event_type", evt.Type,
		"event_id", evt.ID,
		"correlation_id", correlationID,
	).Debug("handling event")

	// Event persistence has already committed. Decouple from request
	// cancellation while carrying the authenticated tenant identity.
	bg := correlation.WithCorrelationID(context.Background(), correlationID)
	if tenantID := contextkeys.GetTenantID(ctx); tenantID != "" {
		bg = contextkeys.WithTenantID(bg, tenantID)
	}
	if schema := TenantSchemaFromContext(ctx); schema != "" {
		bg = WithTenantSchema(bg, schema)
	}
	ctxEvt, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()

	err := sub.Handler(ctxEvt, evt)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		logger.WithFields(
			"subscription_id", sub.ID,
			"event_type", evt.Type,
			"event_id", evt.ID,
			"correlation_id", correlationID,
			"error", err.Error(),
			"elapsed_ms", elapsed,
		).Error("event handler failed")
		return err
	}
	if elapsed > 2000 {
		logger.WithFields(
			"subscription_id", sub.ID,
			"event_type", evt.Type,
			"event_id", evt.ID,
			"correlation_id", correlationID,
			"elapsed_ms", elapsed,
		).Warn("event handler slow (possible pool pressure)")
	} else {
		logger.WithFields(
			"subscription_id", sub.ID,
			"event_type", evt.Type,
			"event_id", evt.ID,
			"correlation_id", correlationID,
			"elapsed_ms", elapsed,
		).Debug("event handled successfully")
	}
	return nil
}

// GetSubscriptions returns information about active subscriptions.
func (bus *InMemoryEventBus) GetSubscriptions() map[string]*EventSubscription {
	bus.mu.RLock()
	defer bus.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make(map[string]*EventSubscription)
	for id, sub := range bus.subscriptions {
		result[id] = &EventSubscription{
			ID:          sub.ID,
			EventType:   sub.EventType,
			EventStream: sub.EventStream,
			Active:      sub.Active,
			Critical:    sub.Critical,
			Handler:     nil, // Don't expose the handler function
		}
	}
	return result
}

// GetEventTypeSubscribers returns subscriber IDs for a given event type.
func (bus *InMemoryEventBus) GetEventTypeSubscribers(eventType string) []string {
	bus.mu.RLock()
	defer bus.mu.RUnlock()

	subscribers, exists := bus.subscribers[eventType]
	if !exists {
		return []string{}
	}

	// Return a copy
	result := make([]string, len(subscribers))
	copy(result, subscribers)
	return result
}
