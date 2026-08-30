package events

import "context"

// Bus is an event bus interface for publishing and subscribing to events
type Bus interface {
	// Publish publishes an event to all subscribers
	Publish(ctx context.Context, event interface{}) error
	// Subscribe registers a handler for a specific event type
	Subscribe(eventType string, handler func(ctx context.Context, event interface{}) error)
}

// InMemoryBus is a simple in-memory event bus implementation
type InMemoryBus struct {
	handlers map[string][]func(ctx context.Context, event interface{}) error
}

// NewInMemoryBus creates a new in-memory event bus
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{
		handlers: make(map[string][]func(ctx context.Context, event interface{}) error),
	}
}

// Publish publishes an event to all registered handlers
func (b *InMemoryBus) Publish(ctx context.Context, event interface{}) error {
	// Get event type from the event
	eventType := ""
	if e, ok := event.(interface{ Event() string }); ok {
		eventType = e.Event()
	}

	handlers, exists := b.handlers[eventType]
	if !exists {
		return nil
	}

	// Call all handlers asynchronously
	for _, handler := range handlers {
		go func(h func(ctx context.Context, event interface{}) error) {
			_ = h(ctx, event)
		}(handler)
	}

	return nil
}

// Subscribe registers a handler for a specific event type
func (b *InMemoryBus) Subscribe(eventType string, handler func(ctx context.Context, event interface{}) error) {
	if b.handlers[eventType] == nil {
		b.handlers[eventType] = make([]func(ctx context.Context, event interface{}) error, 0)
	}
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}
