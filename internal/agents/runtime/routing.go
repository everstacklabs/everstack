package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const sessionRouterKeyPrefix = "agents:session:"
const sessionRouterTTL = 1 * time.Hour

// SessionRouter maps session IDs to the instance that owns them.
type SessionRouter interface {
	// Register records that instanceID owns sessionID.
	Register(ctx context.Context, sessionID, instanceID string) error
	// Lookup returns the instance that owns sessionID.
	Lookup(ctx context.Context, sessionID string) (instanceID string, err error)
	// Unregister removes the session mapping.
	Unregister(ctx context.Context, sessionID string) error
}

// ---------------------------------------------------------------------------
// LocalRouter — default when Redis is not available
// ---------------------------------------------------------------------------

// LocalRouter is a no-op router that always returns the local instance ID.
type LocalRouter struct {
	localID string
}

// NewLocalRouter creates a router that returns localID for every lookup.
func NewLocalRouter(localID string) *LocalRouter {
	return &LocalRouter{localID: localID}
}

func (r *LocalRouter) Register(_ context.Context, _, _ string) error   { return nil }
func (r *LocalRouter) Lookup(_ context.Context, _ string) (string, error) { return r.localID, nil }
func (r *LocalRouter) Unregister(_ context.Context, _ string) error    { return nil }

// ---------------------------------------------------------------------------
// RedisRouter — stores session→instance mapping in Redis
// ---------------------------------------------------------------------------

// RedisRouter stores session ownership in Redis with a TTL that is refreshed
// by the heartbeat writer.
type RedisRouter struct {
	client *redis.Client
}

// NewRedisRouter creates a Redis-backed session router.
func NewRedisRouter(client *redis.Client) *RedisRouter {
	return &RedisRouter{client: client}
}

func (r *RedisRouter) Register(ctx context.Context, sessionID, instanceID string) error {
	return r.client.Set(ctx, sessionRouterKeyPrefix+sessionID, instanceID, sessionRouterTTL).Err()
}

func (r *RedisRouter) Lookup(ctx context.Context, sessionID string) (string, error) {
	val, err := r.client.Get(ctx, sessionRouterKeyPrefix+sessionID).Result()
	if err == redis.Nil {
		return "", fmt.Errorf("session %s not found in router", sessionID)
	}
	return val, err
}

func (r *RedisRouter) Unregister(ctx context.Context, sessionID string) error {
	return r.client.Del(ctx, sessionRouterKeyPrefix+sessionID).Err()
}

// RefreshTTL extends the TTL for a session key. Called by the heartbeat writer
// to keep routing entries alive while the session is active.
func (r *RedisRouter) RefreshTTL(ctx context.Context, sessionID string) error {
	return r.client.Expire(ctx, sessionRouterKeyPrefix+sessionID, sessionRouterTTL).Err()
}
