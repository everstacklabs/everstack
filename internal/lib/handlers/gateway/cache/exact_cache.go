// Package cache provides caching utilities for the gateway.
package cache

import (
	"context"
	"strconv"
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

	// TenantID is the authenticated tenant that produced this entry. It is
	// set by the cache on store and re-checked on every read; an entry is
	// never served to any other tenant.
	TenantID string
}

// ExactCache provides exact-match caching for LLM requests.
// It uses xxHash for fast hashing and LRU for eviction.
//
// Cache key is computed from:
//   - key version + authenticated tenant + the request's CacheFingerprint
//     (every output-affecting field, in order)
//
// Entries are tenant-owned: see scope.go.
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

	// CacheFingerprint is a deterministic encoding of every request field
	// that can change the output: model, every message (in order, all
	// content parts), system prompt / instructions, tools and tool choice,
	// response format and sampling parameters. Transport-only fields
	// (stream flag, request metadata) are excluded. It is the exact-cache key
	// material.
	CacheFingerprint() []byte

	// SemanticContextFingerprint is CacheFingerprint with the text of the
	// final user turn (the semantic query) left out. Semantic matches are
	// only considered between requests with identical context.
	SemanticContextFingerprint() []byte
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
func (r *SimpleChatRequest) CacheFingerprint() []byte {
	return FieldFingerprint(r)
}
func (r *SimpleChatRequest) SemanticContextFingerprint() []byte {
	return FieldSemanticContextFingerprint(r)
}

// SimpleMessage is a concrete implementation of Message for testing.
type SimpleMessage struct {
	Role    string
	Content string
}

func (m *SimpleMessage) GetRole() string    { return m.Role }
func (m *SimpleMessage) GetContent() string { return m.Content }

// GetWithContext retrieves the caller tenant's cached response for req.
// The tenant comes from ctx (TenantFromContext); without one it is a miss.
// Returns the cached response and true if found and not expired.
func (c *ExactCache) GetWithContext(ctx context.Context, req ChatRequest) (*CachedResponse, bool) {
	if !c.enabled.Load() {
		return nil, false
	}

	tenantID := TenantFromContext(ctx)
	if tenantID == "" {
		c.misses.Add(1)
		return nil, false
	}

	key, ok := ComputeCacheKey(tenantID, req)
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	return c.getOwned(key, tenantID)
}

// GetByHashWithContext retrieves a cached response by a hash previously
// produced by ComputeCacheKey. The entry is only returned when it is owned by
// the tenant in ctx.
func (c *ExactCache) GetByHashWithContext(ctx context.Context, hash uint64) (*CachedResponse, bool) {
	if !c.enabled.Load() {
		return nil, false
	}

	tenantID := TenantFromContext(ctx)
	if tenantID == "" {
		c.misses.Add(1)
		return nil, false
	}

	return c.getOwned(hash, tenantID)
}

func (c *ExactCache) getOwned(key uint64, tenantID string) (*CachedResponse, bool) {
	c.mu.RLock()
	resp, ok := c.cache.Get(key)
	c.mu.RUnlock()

	// Ownership is re-checked, not implied by the key: xxhash64 collisions
	// can be constructed, and entries written before tenant scoping carry no
	// owner at all.
	if !ok || resp.TenantID == "" || resp.TenantID != tenantID {
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

// SetWithContext stores resp for the tenant in ctx. Without a tenant nothing
// is stored. resp is copied; the caller keeps ownership of its value.
func (c *ExactCache) SetWithContext(ctx context.Context, req ChatRequest, resp *CachedResponse) {
	if !c.enabled.Load() || resp == nil {
		return
	}

	tenantID := TenantFromContext(ctx)
	if tenantID == "" {
		return
	}

	key, ok := ComputeCacheKey(tenantID, req)
	if !ok {
		return
	}
	c.setOwned(key, tenantID, resp)
}

// SetByHashWithContext stores resp under a hash previously produced by
// ComputeCacheKey for the tenant in ctx. Without a tenant nothing is stored.
func (c *ExactCache) SetByHashWithContext(ctx context.Context, hash uint64, resp *CachedResponse) {
	if !c.enabled.Load() || resp == nil {
		return
	}

	tenantID := TenantFromContext(ctx)
	if tenantID == "" {
		return
	}

	c.setOwned(hash, tenantID, resp)
}

func (c *ExactCache) setOwned(key uint64, tenantID string, resp *CachedResponse) {
	entry := cloneForStore(resp)
	entry.CreatedAt = time.Now()
	entry.RequestHash = key
	entry.TenantID = tenantID

	c.mu.Lock()
	c.cache.Add(key, entry)
	c.mu.Unlock()
}

// RemoveWithContext removes the tenant-in-ctx's entry for req.
func (c *ExactCache) RemoveWithContext(ctx context.Context, req ChatRequest) {
	tenantID := TenantFromContext(ctx)
	if tenantID == "" {
		return
	}
	key, ok := ComputeCacheKey(tenantID, req)
	if !ok {
		return
	}
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

// ComputeCacheKey computes the exact-cache key for a tenant's request.
// The key is deterministic for the same tenant and logical request, and
// differs across tenants for identical requests. ok is false when the request
// must not be cached (no tenant, or no fingerprint could be produced).
func ComputeCacheKey(tenantID string, req ChatRequest) (key uint64, ok bool) {
	if tenantID == "" || req == nil {
		return 0, false
	}
	fp := req.CacheFingerprint()
	if fp == nil {
		return 0, false
	}
	h := xxhash.New()
	writeField(h, []byte(exactKeyVersion))
	writeField(h, []byte(tenantID))
	writeField(h, fp)
	return h.Sum64(), true
}

// formatFloat formats a float for hashing (avoids floating point comparison issues).
func formatFloat(f float64) string {
	// Round to 4 decimal places for cache key stability.
	return strconv.FormatFloat(f, 'f', 4, 64)
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
