// Package cache provides caching utilities for the gateway.
package cache

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	lru "github.com/hashicorp/golang-lru/v2"
)

// CachedResponse holds a cached LLM response with metadata.
type CachedResponse struct {
	// Response is the serialized response data (JSON or protobuf)
	Response []byte

	// CreatedAt is when this cache entry was created
	CreatedAt time.Time

	// Model is the model that generated this response
	Model string

	// InputTokens is the number of input tokens
	InputTokens int

	// OutputTokens is the number of output tokens
	OutputTokens int

	// FinishReason is why generation stopped
	FinishReason string

	// RequestHash is the xxhash of the original request
	RequestHash uint64
}

// ExactCache provides exact-match caching for LLM requests.
// It uses xxHash for fast hashing and LRU for eviction.
//
// Cache key is computed from:
//   - model + sorted(messages) + temperature + max_tokens
//
// Performance targets:
//   - Cache hit: <1ms
//   - Hash computation: <50µs
type ExactCache struct {
	cache   *lru.Cache[uint64, *CachedResponse]
	ttl     time.Duration
	enabled atomic.Bool

	// Stats
	hits   atomic.Uint64
	misses atomic.Uint64
	evicts atomic.Uint64

	// mu protects cache operations (LRU is not fully thread-safe)
	mu sync.RWMutex
}

// ExactCacheConfig configures the exact match cache.
type ExactCacheConfig struct {
	// MaxEntries is the maximum number of cached responses
	MaxEntries int
	// TTL is how long responses remain valid
	TTL time.Duration
	// Enabled controls whether caching is active
	Enabled bool
}

// DefaultExactCacheConfig returns sensible defaults.
func DefaultExactCacheConfig() ExactCacheConfig {
	return ExactCacheConfig{
		MaxEntries: 50000,
		TTL:        5 * time.Minute,
		Enabled:    true,
	}
}

// NewExactCache creates a new exact-match cache.
func NewExactCache(cfg ExactCacheConfig) (*ExactCache, error) {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 50000
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 5 * time.Minute
	}

	cache, err := lru.New[uint64, *CachedResponse](cfg.MaxEntries)
	if err != nil {
		return nil, err
	}

	ec := &ExactCache{
		cache: cache,
		ttl:   cfg.TTL,
	}
	ec.enabled.Store(cfg.Enabled)

	return ec, nil
}

// ChatRequest represents the fields needed to compute a cache key.
// This is a minimal interface to avoid import cycles with the gateway package.
type ChatRequest interface {
	GetModel() string
	GetMessages() []Message
	GetTemperature() float64
	GetMaxTokens() int
	GetTopP() float64
	GetStream() bool
}

// Message represents a chat message for cache key computation.
type Message interface {
	GetRole() string
	GetContent() string
}

// SimpleChatRequest is a concrete implementation of ChatRequest for testing.
type SimpleChatRequest struct {
	Model       string
	Messages    []SimpleMessage
	Temperature float64
	MaxTokens   int
	TopP        float64
	Stream      bool
}

func (r *SimpleChatRequest) GetModel() string { return r.Model }
func (r *SimpleChatRequest) GetMessages() []Message {
	msgs := make([]Message, len(r.Messages))
	for i := range r.Messages {
		msgs[i] = &r.Messages[i]
	}
	return msgs
}
func (r *SimpleChatRequest) GetTemperature() float64 { return r.Temperature }
func (r *SimpleChatRequest) GetMaxTokens() int       { return r.MaxTokens }
func (r *SimpleChatRequest) GetTopP() float64        { return r.TopP }
func (r *SimpleChatRequest) GetStream() bool         { return r.Stream }

// SimpleMessage is a concrete implementation of Message for testing.
type SimpleMessage struct {
	Role    string
	Content string
}

func (m *SimpleMessage) GetRole() string    { return m.Role }
func (m *SimpleMessage) GetContent() string { return m.Content }

// Get retrieves a cached response for the given request.
// Returns the cached response and true if found and not expired.
// This is the legacy method without tracing - prefer GetWithContext for new code.
func (c *ExactCache) Get(req ChatRequest) (*CachedResponse, bool) {
	return c.GetWithContext(context.Background(), req)
}

// GetWithContext retrieves a cached response with context support.
// Returns the cached response and true if found and not expired.
// Note: Tracing is handled at the fastpath engine layer to avoid import cycles.
func (c *ExactCache) GetWithContext(ctx context.Context, req ChatRequest) (*CachedResponse, bool) {
	_ = ctx // Context reserved for future use

	if !c.enabled.Load() {
		return nil, false
	}

	key := c.ComputeKey(req)

	c.mu.RLock()
	resp, ok := c.cache.Get(key)
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	// Check TTL
	if time.Since(resp.CreatedAt) >= c.ttl {
		c.mu.Lock()
		c.cache.Remove(key)
		c.mu.Unlock()
		c.misses.Add(1)
		c.evicts.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	return resp, true
}

// GetByHash retrieves a cached response by pre-computed hash.
// This is the legacy method without tracing - prefer GetByHashWithContext for new code.
func (c *ExactCache) GetByHash(hash uint64) (*CachedResponse, bool) {
	return c.GetByHashWithContext(context.Background(), hash)
}

// GetByHashWithContext retrieves a cached response by pre-computed hash with context support.
// Note: Tracing is handled at the fastpath engine layer to avoid import cycles.
func (c *ExactCache) GetByHashWithContext(ctx context.Context, hash uint64) (*CachedResponse, bool) {
	_ = ctx // Context reserved for future use

	if !c.enabled.Load() {
		return nil, false
	}

	c.mu.RLock()
	resp, ok := c.cache.Get(hash)
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	if time.Since(resp.CreatedAt) >= c.ttl {
		c.mu.Lock()
		c.cache.Remove(hash)
		c.mu.Unlock()
		c.misses.Add(1)
		c.evicts.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	return resp, true
}

// Set stores a response in the cache.
// This is the legacy method without tracing - prefer SetWithContext for new code.
func (c *ExactCache) Set(req ChatRequest, resp *CachedResponse) {
	c.SetWithContext(context.Background(), req, resp)
}

// SetWithContext stores a response in the cache with context support.
// Note: Tracing is handled at the fastpath engine layer to avoid import cycles.
func (c *ExactCache) SetWithContext(ctx context.Context, req ChatRequest, resp *CachedResponse) {
	_ = ctx // Context reserved for future use

	if !c.enabled.Load() || resp == nil {
		return
	}

	key := c.ComputeKey(req)
	resp.CreatedAt = time.Now()
	resp.RequestHash = key

	c.mu.Lock()
	c.cache.Add(key, resp)
	c.mu.Unlock()
}

// SetByHash stores a response with a pre-computed hash.
// This is the legacy method without tracing - prefer SetByHashWithContext for new code.
func (c *ExactCache) SetByHash(hash uint64, resp *CachedResponse) {
	c.SetByHashWithContext(context.Background(), hash, resp)
}

// SetByHashWithContext stores a response with a pre-computed hash with context support.
// Note: Tracing is handled at the fastpath engine layer to avoid import cycles.
func (c *ExactCache) SetByHashWithContext(ctx context.Context, hash uint64, resp *CachedResponse) {
	_ = ctx // Context reserved for future use

	if !c.enabled.Load() || resp == nil {
		return
	}

	resp.CreatedAt = time.Now()
	resp.RequestHash = hash

	c.mu.Lock()
	c.cache.Add(hash, resp)
	c.mu.Unlock()
}

// Remove removes a specific entry from the cache.
func (c *ExactCache) Remove(req ChatRequest) {
	key := c.ComputeKey(req)
	c.mu.Lock()
	c.cache.Remove(key)
	c.mu.Unlock()
}

// Clear removes all entries from the cache.
func (c *ExactCache) Clear() {
	c.mu.Lock()
	c.cache.Purge()
	c.mu.Unlock()
}

// SetEnabled enables or disables the cache.
func (c *ExactCache) SetEnabled(enabled bool) {
	c.enabled.Store(enabled)
}

// IsEnabled returns whether the cache is enabled.
func (c *ExactCache) IsEnabled() bool {
	return c.enabled.Load()
}

// ComputeKey computes the cache key for a request.
// The key is deterministic for the same logical request.
func (c *ExactCache) ComputeKey(req ChatRequest) uint64 {
	return ComputeCacheKey(req)
}

// ComputeCacheKey computes a cache key from a chat request.
// Exported for use by other packages that need to pre-compute keys.
func ComputeCacheKey(req ChatRequest) uint64 {
	h := xxhash.New()

	// Model
	h.WriteString(req.GetModel())
	h.WriteString("|")

	// Messages (sorted by role for determinism with same content)
	messages := req.GetMessages()
	msgStrs := make([]string, len(messages))
	for i, msg := range messages {
		msgStrs[i] = msg.GetRole() + ":" + msg.GetContent()
	}
	sort.Strings(msgStrs)
	for _, ms := range msgStrs {
		h.WriteString(ms)
		h.WriteString("|")
	}

	// Sampling parameters (these affect output)
	// Use string formatting to avoid floating point comparison issues
	h.WriteString(formatFloat(req.GetTemperature()))
	h.WriteString("|")
	h.WriteString(formatInt(req.GetMaxTokens()))
	h.WriteString("|")
	h.WriteString(formatFloat(req.GetTopP()))

	return h.Sum64()
}

// formatFloat formats a float for hashing (avoids floating point comparison issues).
func formatFloat(f float64) string {
	// Round to 4 decimal places for cache key stability
	return strings.TrimRight(strings.TrimRight(
		strings.Replace(string(rune(int(f*10000))), ".", "", 1), "0"), ".")
}

// formatInt formats an int for hashing.
func formatInt(i int) string {
	if i == 0 {
		return "0"
	}
	// Simple int to string without allocations
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// Stats returns cache statistics.
func (c *ExactCache) Stats() (hits, misses, evicts uint64, hitRatio float64, size int) {
	hits = c.hits.Load()
	misses = c.misses.Load()
	evicts = c.evicts.Load()
	total := hits + misses
	if total > 0 {
		hitRatio = float64(hits) / float64(total)
	}
	c.mu.RLock()
	size = c.cache.Len()
	c.mu.RUnlock()
	return
}

// ResetStats resets the hit/miss/evict counters.
func (c *ExactCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.evicts.Store(0)
}

// Size returns the current number of cached entries.
func (c *ExactCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache.Len()
}

// Cleanup removes expired entries. Call this periodically.
func (c *ExactCache) Cleanup() int {
	now := time.Now()
	removed := 0

	c.mu.Lock()
	defer c.mu.Unlock()

	keys := c.cache.Keys()
	for _, key := range keys {
		if resp, ok := c.cache.Peek(key); ok {
			if now.Sub(resp.CreatedAt) >= c.ttl {
				c.cache.Remove(key)
				removed++
			}
		}
	}

	return removed
}

// StartCleanupRoutine starts a background goroutine that periodically cleans up expired entries.
// Returns a channel that can be closed to stop the routine.
func (c *ExactCache) StartCleanupRoutine(interval time.Duration) chan<- struct{} {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Cleanup()
			case <-stop:
				return
			}
		}
	}()
	return stop
}
