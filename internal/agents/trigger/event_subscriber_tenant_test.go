package trigger

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/database"
)

type eventTenantStore struct {
	Store
	calls    int
	tenantID string
}

func (s *eventTenantStore) ListEventTriggers(_ context.Context, _, _, tenantID string) ([]*Trigger, error) {
	s.calls++
	s.tenantID = tenantID
	return nil, nil
}

func TestEventSubscriberScopesLookupToTheSourceTenant(t *testing.T) {
	store := &eventTenantStore{}
	subscriber := &EventSubscriber{store: store}
	event := database.Event{ID: "event-1", Payload: []byte(`{"agent_id":"agent-1","tenant_id":"tenant-a"}`)}
	if err := subscriber.handleEvent(context.Background(), event, "session.end"); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}
	if store.calls != 1 || store.tenantID != "tenant-a" {
		t.Fatalf("calls = %d, tenant = %q", store.calls, store.tenantID)
	}
}

func TestEventSubscriberSkipsEventsWithoutSourceTenant(t *testing.T) {
	store := &eventTenantStore{}
	subscriber := &EventSubscriber{store: store}
	event := database.Event{ID: "event-1", Payload: []byte(`{"agent_id":"agent-1"}`)}
	if err := subscriber.handleEvent(context.Background(), event, "session.end"); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("calls = %d", store.calls)
	}
}
