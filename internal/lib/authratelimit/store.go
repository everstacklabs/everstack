package authratelimit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Rule is a fixed window: at most Limit requests per Window for one key.
type Rule struct {
	Limit  int
	Window time.Duration
}

// Decision reports whether a request fits its rule and, when it does not,
// how long until the window frees up.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	// Count is the number of requests in the window, this one included.
	Count int64
}

// Window is a read of a counter: how many requests it holds and how long
// until it resets. A missing counter reads as zero.
type Window struct {
	Count      int64
	RetryAfter time.Duration
}

// Store holds fixed-window counters.
type Store interface {
	// Consume records one request against a key and decides it in a single
	// atomic step, so concurrent requests cannot all pass a stale read.
	Consume(ctx context.Context, key string, rule Rule) (Decision, error)
	// Peek reads a counter without recording anything. It is for checks that
	// must run before the outcome that decides whether to count is known,
	// such as a password attempt that only counts if it fails.
	Peek(ctx context.Context, key string) (Window, error)
}

// ErrUnavailable means the shared store is required and could not answer.
// Callers refuse the request: the operator chose shared counting because
// divergent per-replica counts are unacceptable, so letting the request
// through unlimited would defeat the point.
var ErrUnavailable = errors.New("authratelimit: shared rate-limit store unavailable")

const memoryStoreSweepInterval = time.Minute

// MemoryStore counts fixed windows in process memory. It is the store when no
// Redis is configured and the per-request fallback when an optional Redis is
// unreachable. Same semantics as RedisStore, so a replica flipping between
// the two does not change what a caller experiences.
type MemoryStore struct {
	mu        sync.Mutex
	windows   map[string]*memoryWindow
	lastSweep time.Time
	now       func() time.Time
}

type memoryWindow struct {
	count   int
	resetAt time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{windows: make(map[string]*memoryWindow), now: time.Now}
}

func (m *MemoryStore) Consume(_ context.Context, key string, rule Rule) (Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	// Expired windows are dropped lazily so scanner traffic with throwaway
	// keys cannot grow the map without bound, without a goroutine per store.
	if now.Sub(m.lastSweep) >= memoryStoreSweepInterval {
		for k, w := range m.windows {
			if !now.Before(w.resetAt) {
				delete(m.windows, k)
			}
		}
		m.lastSweep = now
	}
	w, ok := m.windows[key]
	if !ok || !now.Before(w.resetAt) {
		w = &memoryWindow{resetAt: now.Add(rule.Window)}
		m.windows[key] = w
	}
	w.count++
	if w.count <= rule.Limit {
		return Decision{Allowed: true, Count: int64(w.count)}, nil
	}
	return Decision{RetryAfter: w.resetAt.Sub(now), Count: int64(w.count)}, nil
}

func (m *MemoryStore) Peek(_ context.Context, key string) (Window, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	w, ok := m.windows[key]
	if !ok || !now.Before(w.resetAt) {
		return Window{}, nil
	}
	return Window{Count: int64(w.count), RetryAfter: w.resetAt.Sub(now)}, nil
}

// fixedWindowScript makes INCR and the first expiry one step, so replicas
// cannot interleave between them. The PTTL repair covers a key that lost its
// expiry (a manual PERSIST, a restore): without it the counter would never
// reset and the caller would stay locked out. The identity service runs the
// same script (services/identity/src/rate-limit-store.ts).
var fixedWindowScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {count, ttl}
`)

var peekScript = redis.NewScript(`
local count = redis.call('GET', KEYS[1])
if not count then
  return {0, -2}
end
return {tonumber(count), redis.call('PTTL', KEYS[1])}
`)

// RedisStore counts fixed windows in Redis, shared by every replica.
type RedisStore struct {
	client    redis.Scripter
	keyPrefix string
}

// DefaultKeyPrefix keeps auth counters apart from the license and billing keys
// in the same Redis.
const DefaultKeyPrefix = "evs:auth:rl:"

func NewRedisStore(client redis.Scripter, keyPrefix string) *RedisStore {
	if keyPrefix == "" {
		keyPrefix = DefaultKeyPrefix
	}
	return &RedisStore{client: client, keyPrefix: keyPrefix}
}

func (r *RedisStore) Consume(ctx context.Context, key string, rule Rule) (Decision, error) {
	reply, err := fixedWindowScript.Run(ctx, r.client, []string{r.keyPrefix + key}, rule.Window.Milliseconds()).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("authratelimit: redis consume: %w", err)
	}
	if len(reply) < 2 {
		return Decision{}, fmt.Errorf("authratelimit: unexpected redis reply %v", reply)
	}
	if reply[0] <= int64(rule.Limit) {
		return Decision{Allowed: true, Count: reply[0]}, nil
	}
	return Decision{RetryAfter: time.Duration(reply[1]) * time.Millisecond, Count: reply[0]}, nil
}

func (r *RedisStore) Peek(ctx context.Context, key string) (Window, error) {
	reply, err := peekScript.Run(ctx, r.client, []string{r.keyPrefix + key}).Int64Slice()
	if err != nil {
		return Window{}, fmt.Errorf("authratelimit: redis peek: %w", err)
	}
	if len(reply) < 2 {
		return Window{}, fmt.Errorf("authratelimit: unexpected redis reply %v", reply)
	}
	if reply[0] <= 0 || reply[1] <= 0 {
		return Window{Count: max(reply[0], 0)}, nil
	}
	return Window{Count: reply[0], RetryAfter: time.Duration(reply[1]) * time.Millisecond}, nil
}

const fallbackWarningInterval = time.Minute

// FallbackStore tries the shared store on every request, so recovery is
// immediate, and decides what a failure means: with required set it returns
// ErrUnavailable; otherwise it counts in the local fallback for that request.
type FallbackStore struct {
	primary  Store
	fallback Store
	required bool

	mu          sync.Mutex
	lastWarning time.Time
}

func NewFallbackStore(primary, fallback Store, required bool) *FallbackStore {
	return &FallbackStore{primary: primary, fallback: fallback, required: required}
}

func (f *FallbackStore) Consume(ctx context.Context, key string, rule Rule) (Decision, error) {
	decision, err := f.primary.Consume(ctx, key, rule)
	if err == nil {
		return decision, nil
	}
	if f.required {
		return Decision{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	f.warn(err)
	return f.fallback.Consume(ctx, key, rule)
}

func (f *FallbackStore) Peek(ctx context.Context, key string) (Window, error) {
	window, err := f.primary.Peek(ctx, key)
	if err == nil {
		return window, nil
	}
	if f.required {
		return Window{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	f.warn(err)
	return f.fallback.Peek(ctx, key)
}

// warn is throttled: an outage would otherwise log once per login attempt.
func (f *FallbackStore) warn(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if now.Sub(f.lastWarning) < fallbackWarningInterval {
		return
	}
	f.lastWarning = now
	logger.WithError(err).Warn("auth: redis rate-limit store failed; counting in this process until it recovers")
}
