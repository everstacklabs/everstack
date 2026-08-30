package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/lib/pq"
)

// Event is the JSON payload pushed by the sandbox_instances NOTIFY
// trigger. Mirrors the json_build_object in the migration. Only emitted
// when lifecycle_state or status actually change.
type Event struct {
	ID             string  `json:"id"`
	TenantID       string  `json:"tenant_id"`
	SessionID      string  `json:"session_id"`
	LifecycleState string  `json:"lifecycle_state"`
	Status         string  `json:"status"`
	UpdatedAt      float64 `json:"updated_at"` // unix epoch seconds
}

// EventBus is a per-gateway-pod fan-out. One PG LISTEN connection
// feeds N per-tenant subscribers. SSE handlers Subscribe on connect
// and Unsubscribe on disconnect.
//
// Subscribers receive events filtered by tenant_id only. There's no
// per-sandbox subscription channel — the FE filters client-side, and
// the volume is small enough that fan-out cost is negligible.
type EventBus struct {
	connStr string

	mu     sync.RWMutex
	subs   map[string]map[uint64]chan Event // tenantID → subID → chan
	nextID atomic.Uint64

	// Set after Run starts, used by Subscribe so the first subscriber
	// after pod start doesn't race with listener startup.
	ready atomic.Bool
}

// NewEventBus builds an EventBus bound to the given Postgres
// connection string. The connection is opened lazily by Run; calling
// Subscribe before Run is fine — subscribers just don't receive
// anything until LISTEN succeeds.
func NewEventBus(connStr string) *EventBus {
	return &EventBus{
		connStr: connStr,
		subs:    make(map[string]map[uint64]chan Event),
	}
}

// Subscribe returns a buffered channel that receives all events for
// the given tenant. The returned id is opaque; pass it to Unsubscribe
// when the consumer disconnects. Buffer size is 32 — slow consumers
// drop events past that to keep the fanout goroutine non-blocking.
func (b *EventBus) Subscribe(tenantID string) (uint64, <-chan Event) {
	ch := make(chan Event, 32)
	id := b.nextID.Add(1)
	b.mu.Lock()
	if _, ok := b.subs[tenantID]; !ok {
		b.subs[tenantID] = make(map[uint64]chan Event)
	}
	b.subs[tenantID][id] = ch
	b.mu.Unlock()
	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel. Safe to
// call multiple times for the same id.
func (b *EventBus) Unsubscribe(tenantID string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tenantSubs, ok := b.subs[tenantID]
	if !ok {
		return
	}
	ch, ok := tenantSubs[id]
	if !ok {
		return
	}
	delete(tenantSubs, id)
	if len(tenantSubs) == 0 {
		delete(b.subs, tenantID)
	}
	close(ch)
}

// Ready reports whether the listener loop is connected and forwarding.
// SSE handlers can use this to decide whether to issue a synchronous
// 'fetch latest list' before subscribing (so the first paint is fresh).
func (b *EventBus) Ready() bool {
	return b.ready.Load()
}

// Run blocks until ctx is cancelled. Opens a PG LISTEN connection,
// reads notifications, decodes payloads, and fans out to per-tenant
// subscribers. Auto-reconnects on connection loss via lib/pq's
// internal logic; the EventBus stays usable across reconnects.
func (b *EventBus) Run(ctx context.Context) error {
	if b.connStr == "" {
		return fmt.Errorf("sandbox_eventbus: connection string is required")
	}

	// pq's Listener handles auto-reconnect with exponential backoff.
	// minReconnectInterval=10s, maxReconnectInterval=60s.
	listener := pq.NewListener(b.connStr,
		10*time.Second,
		1*time.Minute,
		func(event pq.ListenerEventType, err error) {
			switch event {
			case pq.ListenerEventConnected, pq.ListenerEventReconnected:
				logger.Info("sandbox_eventbus: connected")
				b.ready.Store(true)
			case pq.ListenerEventDisconnected:
				logger.WithFields("error", errStr(err)).
					Warn("sandbox_eventbus: disconnected")
				b.ready.Store(false)
			case pq.ListenerEventConnectionAttemptFailed:
				logger.WithFields("error", errStr(err)).
					Debug("sandbox_eventbus: connection attempt failed (will retry)")
			}
		},
	)
	defer listener.Close()

	if err := listener.Listen("sandbox_events"); err != nil {
		return fmt.Errorf("sandbox_eventbus: LISTEN sandbox_events: %w", err)
	}

	logger.Info("sandbox_eventbus: started")
	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_eventbus: stopping")
			return ctx.Err()

		case n := <-listener.Notify:
			if n == nil {
				// Reconnect signal from pq — channel returns nil when
				// the listener loses connection. Re-acquire LISTEN on
				// the new connection.
				logger.Debug("sandbox_eventbus: NOTIFY channel reset, re-listening")
				if err := listener.Listen("sandbox_events"); err != nil {
					logger.WithFields("error", err.Error()).
						Warn("sandbox_eventbus: re-LISTEN failed")
				}
				continue
			}
			b.dispatch(n.Extra)

		case <-time.After(90 * time.Second):
			// Periodic health ping in case the connection silently
			// went stale. lib/pq's Ping returns the underlying error
			// if the connection is dead, which triggers a reconnect.
			go func() {
				if err := listener.Ping(); err != nil {
					logger.WithFields("error", err.Error()).
						Warn("sandbox_eventbus: ping failed (will reconnect)")
				}
			}()
		}
	}
}

// dispatch decodes the NOTIFY payload and fans out to subscribers.
// Slow subscribers (full channel buffer) get dropped events — we
// prefer keeping the fanout loop responsive over guaranteed delivery,
// since the SSE 30s safety-net poll on the FE catches anything we drop.
func (b *EventBus) dispatch(payload string) {
	var evt Event
	if err := json.Unmarshal([]byte(payload), &evt); err != nil {
		logger.WithFields("error", err.Error(), "payload", payload).
			Warn("sandbox_eventbus: malformed NOTIFY payload")
		return
	}

	b.mu.RLock()
	subs, ok := b.subs[evt.TenantID]
	if !ok || len(subs) == 0 {
		b.mu.RUnlock()
		return
	}
	// Snapshot so we don't hold the lock during channel sends.
	channels := make([]chan Event, 0, len(subs))
	for _, ch := range subs {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- evt:
		default:
			// Slow consumer; drop. The FE's safety-net refetch will
			// reconverge state.
			logger.WithFields(
				"tenant_id", evt.TenantID,
				"sandbox_id", evt.ID,
			).Debug("sandbox_eventbus: subscriber buffer full, dropped event")
		}
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
