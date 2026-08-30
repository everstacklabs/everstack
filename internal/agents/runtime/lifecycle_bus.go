package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/redis/go-redis/v9"
)

const lifecycleChannelPrefix = "agents:lifecycle:"

// LifecycleEvent is a single agent lifecycle transition broadcast to
// subscribers (typically the UI on the agent detail page) so they can
// reflect status changes — including recovery attempts — in real time
// without polling the database.
type LifecycleEvent struct {
	AgentID   string    `json:"agent_id"`
	TenantID  string    `json:"tenant_id,omitempty"`
	OldStatus string    `json:"old_status,omitempty"`
	NewStatus string    `json:"new_status"`
	SandboxID string    `json:"sandbox_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// LifecycleBus publishes and subscribes to per-agent lifecycle events.
// Concrete implementations either no-op (LocalLifecycleBus, used when
// Redis isn't configured) or fan out over Redis Pub/Sub.
type LifecycleBus interface {
	// Publish emits an event for evt.AgentID. Publishing to an agent
	// with no subscribers is a successful no-op. The event's TenantID
	// field is used to scope the pub/sub channel.
	Publish(ctx context.Context, evt LifecycleEvent) error
	// Subscribe returns a channel of events for the given agent. The
	// returned channel is closed when ctx is cancelled or Close is called.
	// tenantID scopes the subscription channel to prevent cross-tenant leakage.
	Subscribe(ctx context.Context, agentID string, tenantID string) (<-chan LifecycleEvent, error)
	// Close releases resources held by the bus.
	Close() error
}

// ---------------------------------------------------------------------------
// LocalLifecycleBus — no-op default for single-instance deployments.
// ---------------------------------------------------------------------------

// LocalLifecycleBus drops Publish calls on the floor and returns an empty
// channel from Subscribe. Use when Redis isn't available; the UI falls
// back to polling.
type LocalLifecycleBus struct{}

// NewLocalLifecycleBus returns a no-op LifecycleBus.
func NewLocalLifecycleBus() *LocalLifecycleBus { return &LocalLifecycleBus{} }

func (LocalLifecycleBus) Publish(context.Context, LifecycleEvent) error { return nil }
func (LocalLifecycleBus) Subscribe(_ context.Context, _ string, _ string) (<-chan LifecycleEvent, error) {
	return make(chan LifecycleEvent), nil
}
func (LocalLifecycleBus) Close() error { return nil }

// ---------------------------------------------------------------------------
// RedisLifecycleBus — Redis Pub/Sub fanout, one channel per agent ID.
// ---------------------------------------------------------------------------

// RedisLifecycleBus publishes lifecycle transitions to per-agent Redis
// channels so subscribers on any replica receive them.
type RedisLifecycleBus struct {
	client *redis.Client
}

// NewRedisLifecycleBus returns a Redis-backed lifecycle bus.
func NewRedisLifecycleBus(client *redis.Client) *RedisLifecycleBus {
	return &RedisLifecycleBus{client: client}
}

func (b *RedisLifecycleBus) Publish(ctx context.Context, evt LifecycleEvent) error {
	if evt.AgentID == "" {
		return fmt.Errorf("lifecycle_bus: AgentID required")
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("lifecycle_bus: marshal: %w", err)
	}
	// Scope channel by tenantID to prevent cross-tenant event leakage.
	channel := lifecycleChannelPrefix + evt.AgentID
	if evt.TenantID != "" {
		channel = lifecycleChannelPrefix + evt.TenantID + ":" + evt.AgentID
	}
	return b.client.Publish(ctx, channel, payload).Err()
}

// Subscribe opens a Redis subscription for the given agentID and returns
// a channel of decoded events. The subscription is torn down when ctx is
// cancelled. Each subscriber gets its own underlying PubSub so multiple
// concurrent watchers don't share a connection.
// tenantID scopes the channel to prevent cross-tenant event leakage.
func (b *RedisLifecycleBus) Subscribe(ctx context.Context, agentID string, tenantID string) (<-chan LifecycleEvent, error) {
	if agentID == "" {
		return nil, fmt.Errorf("lifecycle_bus: agentID required")
	}
	channel := lifecycleChannelPrefix + agentID
	if tenantID != "" {
		channel = lifecycleChannelPrefix + tenantID + ":" + agentID
	}
	subCtx, cancel := context.WithCancel(ctx)
	pubsub := b.client.Subscribe(subCtx, channel)

	if _, err := pubsub.Receive(subCtx); err != nil {
		cancel()
		_ = pubsub.Close()
		return nil, fmt.Errorf("lifecycle_bus: subscribe: %w", err)
	}

	ch := make(chan LifecycleEvent, 16)
	go func() {
		defer close(ch)
		defer pubsub.Close()
		defer cancel()
		msgCh := pubsub.Channel()
		for {
			select {
			case <-subCtx.Done():
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				var evt LifecycleEvent
				if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
					logger.WithError(err).Warn("lifecycle_bus: failed to unmarshal event")
					continue
				}
				select {
				case ch <- evt:
				case <-subCtx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

func (b *RedisLifecycleBus) Close() error { return nil }
