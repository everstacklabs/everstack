package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/redis/go-redis/v9"
)

const approvalChannelPrefix = "agents:approvals:"

// ApprovalBridge delivers approval decisions across instances.
type ApprovalBridge interface {
	// Publish sends a decision to the instance that owns the session.
	// The decision's TenantID is used to scope the pub/sub channel.
	Publish(ctx context.Context, decision ApprovalDecision) error
	// Subscribe returns a channel that receives decisions targeted at instanceID.
	// Uses a pattern subscription to receive decisions for all tenants routed
	// to this instance.
	Subscribe(ctx context.Context, instanceID string) (<-chan ApprovalDecision, error)
	// Close releases resources.
	Close() error
}

// ---------------------------------------------------------------------------
// LocalBridge — default, delivers directly to in-memory gates
// ---------------------------------------------------------------------------

// LocalBridge is a no-op bridge used in single-instance mode. Decisions are
// delivered directly to the in-memory gate by SubmitReview; no cross-instance
// pub/sub is needed.
type LocalBridge struct{}

func NewLocalBridge() *LocalBridge { return &LocalBridge{} }

func (b *LocalBridge) Publish(_ context.Context, _ ApprovalDecision) error { return nil }
func (b *LocalBridge) Subscribe(_ context.Context, _ string) (<-chan ApprovalDecision, error) {
	// Return a channel that never receives — SubmitReview delivers directly.
	return make(chan ApprovalDecision), nil
}
func (b *LocalBridge) Close() error { return nil }

// ---------------------------------------------------------------------------
// RedisBridge — cross-instance delivery via Redis Pub/Sub
// ---------------------------------------------------------------------------

// RedisBridge publishes approval decisions to a per-instance Redis channel
// so the owning instance can deliver them to the in-memory gate.
type RedisBridge struct {
	client *redis.Client
	pubsub *redis.PubSub
	cancel context.CancelFunc
}

// NewRedisBridge creates a Redis-backed approval bridge.
func NewRedisBridge(client *redis.Client) *RedisBridge {
	return &RedisBridge{client: client}
}

func (b *RedisBridge) Publish(ctx context.Context, decision ApprovalDecision) error {
	data, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal approval decision: %w", err)
	}
	// Build channel with tenant scoping: agents:approvals:{tenantID}:{instanceID}
	// TenantID is required for channel isolation — if empty, the channel
	// falls back to the unscoped prefix (defense-in-depth: subscriber also
	// validates TenantID on received decisions).
	channel := approvalChannelPrefix + decision.TargetInstanceID
	if decision.TenantID != "" {
		channel = approvalChannelPrefix + decision.TenantID + ":" + decision.TargetInstanceID
	}
	return b.client.Publish(ctx, channel, data).Err()
}

// Subscribe starts listening for decisions on channels for instanceID.
// Uses a pattern subscription (agents:approvals:*:{instanceID}) to receive
// tenant-scoped decisions from all tenants routed to this instance.
func (b *RedisBridge) Subscribe(ctx context.Context, instanceID string) (<-chan ApprovalDecision, error) {
	subCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel

	// Pattern matches tenant-scoped channels: agents:approvals:{tenantID}:{instanceID}
	pattern := approvalChannelPrefix + "*:" + instanceID
	b.pubsub = b.client.PSubscribe(subCtx, pattern)

	// Wait for subscription confirmation
	if _, err := b.pubsub.Receive(subCtx); err != nil {
		cancel()
		return nil, fmt.Errorf("redis subscribe: %w", err)
	}

	ch := make(chan ApprovalDecision, 16)
	go func() {
		defer close(ch)
		msgCh := b.pubsub.Channel()
		for {
			select {
			case <-subCtx.Done():
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				var decision ApprovalDecision
				if err := json.Unmarshal([]byte(msg.Payload), &decision); err != nil {
					logger.WithError(err).Warn("redis_bridge: failed to unmarshal decision")
					continue
				}
				select {
				case ch <- decision:
				case <-subCtx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

func (b *RedisBridge) Close() error {
	if b.cancel != nil {
		b.cancel()
	}
	if b.pubsub != nil {
		return b.pubsub.Close()
	}
	return nil
}
