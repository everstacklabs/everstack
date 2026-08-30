package database

import (
	"context"

	"github.com/everstacklabs/everstack/internal/events"
)

// Event represents a CQRS event to be persisted and published.
type Event struct {
	ID         string
	Type       string
	Stream     string
	Payload    []byte
	CreatedAt  int64
	Visibility events.EventVisibility // Who can see this event (user/internal/both)
}

// NewEvent creates an event with automatic visibility based on event type
func NewEvent(id, eventType, stream string, payload []byte, createdAt int64) Event {
	return Event{
		ID:         id,
		Type:       eventType,
		Stream:     stream,
		Payload:    payload,
		CreatedAt:  createdAt,
		Visibility: events.GetVisibility(eventType),
	}
}

// IsVisibleToUser returns true if this event should appear in user audit logs
func (e Event) IsVisibleToUser() bool {
	return events.IsVisibleToUser(e.Type)
}

// IsVisibleToCloud returns true if this event should be sent to Everstack Cloud
func (e Event) IsVisibleToCloud() bool {
	return events.IsVisibleToCloud(e.Type)
}

// Writer captures command-side persistence operations.
type Writer interface {
	Append(ctx context.Context, events ...Event) error
}

// Reader captures query-side reads optimized for projections.
type Reader interface {
	Get(ctx context.Context, stream string, id string, out any) error
}

// Bus is a minimal event bus/dispatcher abstraction (in-proc for now).
type Bus interface {
	Publish(ctx context.Context, events ...Event) error
}
