package database

import (
	"context"
	"errors"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestInMemoryEventBusPropagatesTenantIdentityToSubscribers(t *testing.T) {
	bus := NewInMemoryEventBus()
	seen := make(chan struct {
		tenantID string
		schema   string
	}, 1)
	if err := bus.Subscribe("tenant-probe", "provider.configured", "providers", func(ctx context.Context, _ Event) error {
		seen <- struct {
			tenantID string
			schema   string
		}{
			tenantID: contextkeys.GetTenantID(ctx),
			schema:   TenantSchemaFromContext(ctx),
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
	ctx = WithTenantSchema(ctx, "tenant-a")
	if err := bus.Publish(ctx, Event{
		ID:     "event-1",
		Type:   "provider.configured",
		Stream: "providers",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-seen:
		if got.tenantID != "tenant-a" {
			t.Fatalf("subscriber tenant ID = %q, want %q", got.tenantID, "tenant-a")
		}
		if got.schema != "tenant-a" {
			t.Fatalf("subscriber tenant schema = %q, want %q", got.schema, "tenant-a")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event subscriber")
	}
}

func TestInMemoryEventBusWaitsForCriticalSubscriber(t *testing.T) {
	bus := NewInMemoryEventBus()
	release := make(chan struct{})
	started := make(chan struct{})
	if err := bus.SubscribeCritical("critical", "storage_config.created", "storage_configs", func(context.Context, Event) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- bus.Publish(context.Background(), Event{Type: "storage_config.created", Stream: "storage_configs"})
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("Publish() returned before critical projection completed: %v", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
}

func TestInMemoryEventBusReturnsCriticalSubscriberError(t *testing.T) {
	bus := NewInMemoryEventBus()
	want := errors.New("projection failed")
	if err := bus.SubscribeCritical("critical", "storage_config.updated", "storage_configs", func(context.Context, Event) error {
		return want
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	err := bus.Publish(context.Background(), Event{Type: "storage_config.updated", Stream: "storage_configs"})
	if !errors.Is(err, want) {
		t.Fatalf("Publish() error = %v, want wrapped projection error", err)
	}
}
