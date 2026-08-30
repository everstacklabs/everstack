// Package fastpath provides high-performance request processing for the gateway.
package fastpath

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/cespare/xxhash/v2"
)

// FastPathAuthCache provides ultra-fast API key validation using a Bloom filter
// for negative lookups and a lock-free sync.Map for validated keys.
//
// Performance targets:
//   - Cache hit: <10µs
//   - Bloom filter negative: <5µs
//   - Cache miss (fallback to DB): depends on DB
type FastPathAuthCache struct {
	// bloom provides fast negative lookups (key definitely doesn't exist)
	bloom *bloom.BloomFilter

	// validKeys stores validated API key hashes: hash -> expiresAt (unix nano)
	validKeys sync.Map

	// precomputed stores pre-hashed API keys: raw key -> hash
	// This avoids re-hashing the same key on every request
	precomputed sync.Map

	// stats for observability
	hits   atomic.Uint64
	misses atomic.Uint64

	// ttl for cached entries
	ttl time.Duration

	// mu protects bloom filter updates (writes only)
	mu sync.Mutex
}

// AuthCacheConfig configures the FastPathAuthCache.
type AuthCacheConfig struct {
	// ExpectedKeys is the expected number of API keys (for Bloom filter sizing)
	ExpectedKeys uint
	// FalsePositiveRate is the target false positive rate for the Bloom filter
	FalsePositiveRate float64
	// TTL is how long validated keys remain cached
	TTL time.Duration
}

// DefaultAuthCacheConfig returns sensible defaults for the auth cache.
func DefaultAuthCacheConfig() AuthCacheConfig {
	return AuthCacheConfig{
		ExpectedKeys:      100000,
		FalsePositiveRate: 0.001, // 0.1%
		TTL:               60 * time.Second,
	}
}

// NewFastPathAuthCache creates a new high-performance auth cache.
func NewFastPathAuthCache(cfg AuthCacheConfig) *FastPathAuthCache {
	if cfg.ExpectedKeys == 0 {
		cfg.ExpectedKeys = 100000
	}
	if cfg.FalsePositiveRate == 0 {
		cfg.FalsePositiveRate = 0.001
	}
	if cfg.TTL == 0 {
		cfg.TTL = 60 * time.Second
	}

	return &FastPathAuthCache{
		bloom: bloom.NewWithEstimates(cfg.ExpectedKeys, cfg.FalsePositiveRate),
		ttl:   cfg.TTL,
	}
}

// cacheEntry holds the expiration time for a cached key
type cacheEntry struct {
	expiresAt int64 // unix nano
}

// IsValid checks if an API key is valid using the fast path.
// Returns:
//   - valid: true if the key is confirmed valid (cache hit)
//   - definitelyInvalid: always false - we can only confirm validity, not invalidity
//   - If valid is false, the caller should fall through to DB validation
//
// IMPORTANT: This is a POSITIVE cache (caches valid keys), not a negative cache.
// The Bloom filter is used to quickly check if a key MIGHT be cached as valid.
// If a key is not in the Bloom filter, it just means we haven't validated it yet,
// NOT that it's invalid. The caller MUST fall through to DB validation.
func (c *FastPathAuthCache) IsValid(apiKey string) (valid bool, definitelyInvalid bool) {
	if apiKey == "" {
		return false, false // Empty key should be handled by caller, not rejected here
	}

	// 1. Get or compute hash (O(n) on key length, cached after first access)
	hash := c.getOrComputeHash(apiKey)

	// 2. Bloom filter check (<5µs)
	// If Bloom filter says "no", the key is NOT in our cache of valid keys.
	// This does NOT mean the key is invalid - it just means we need to check the DB.
	if !c.bloomTest(hash) {
		c.misses.Add(1)
		return false, false // Fall through to DB validation
	}

	// 3. Lock-free map lookup for validated keys
	// Bloom filter said "maybe in cache" - now check the actual cache
	if entry, ok := c.validKeys.Load(hash); ok {
		e := entry.(cacheEntry)
		if time.Now().UnixNano() < e.expiresAt {
			c.hits.Add(1)
			return true, false // Confirmed valid!
		}
		// Expired - remove from cache
		c.validKeys.Delete(hash)
	}

	c.misses.Add(1)
	// Bloom filter said "maybe" but key not in validated cache (or expired)
	// Caller should validate against DB
	return false, false
}

// MarkValid adds a validated API key to the cache.
// This should be called after successful DB validation.
func (c *FastPathAuthCache) MarkValid(apiKey string) {
	if apiKey == "" {
		return
	}

	hash := c.getOrComputeHash(apiKey)

	// Add to Bloom filter (requires lock for concurrent writes)
	c.mu.Lock()
	c.bloom.Add(c.hashToBytes(hash))
	c.mu.Unlock()

	// Add to validated keys cache
	c.validKeys.Store(hash, cacheEntry{
		expiresAt: time.Now().Add(c.ttl).UnixNano(),
	})
}

// MarkInvalid removes an API key from the cache (e.g., on revocation).
// Note: Cannot remove from Bloom filter, but that's okay - it will just
// cause a cache miss that falls through to DB validation.
func (c *FastPathAuthCache) MarkInvalid(apiKey string) {
	if apiKey == "" {
		return
	}

	hash := c.getOrComputeHash(apiKey)
	c.validKeys.Delete(hash)
	c.precomputed.Delete(apiKey)
}

// Warm pre-populates the cache with known valid API key hashes.
// This should be called on startup to avoid cold-start latency.
func (c *FastPathAuthCache) Warm(apiKeyHashes []uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Add(c.ttl).UnixNano()
	for _, hash := range apiKeyHashes {
		c.bloom.Add(c.hashToBytes(hash))
		c.validKeys.Store(hash, cacheEntry{expiresAt: now})
	}
}

// WarmFromKeys pre-populates the cache with raw API keys.
func (c *FastPathAuthCache) WarmFromKeys(apiKeys []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Add(c.ttl).UnixNano()
	for _, key := range apiKeys {
		hash := xxhash.Sum64String(key)
		c.precomputed.Store(key, hash)
		c.bloom.Add(c.hashToBytes(hash))
		c.validKeys.Store(hash, cacheEntry{expiresAt: now})
	}
}

// Stats returns cache statistics for observability.
func (c *FastPathAuthCache) Stats() (hits, misses uint64, hitRatio float64) {
	hits = c.hits.Load()
	misses = c.misses.Load()
	total := hits + misses
	if total > 0 {
		hitRatio = float64(hits) / float64(total)
	}
	return
}

// ResetStats resets the hit/miss counters.
func (c *FastPathAuthCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
}

// Size returns the approximate number of cached entries.
func (c *FastPathAuthCache) Size() int {
	count := 0
	c.validKeys.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// Cleanup removes expired entries from the cache.
// This can be called periodically to prevent memory bloat.
func (c *FastPathAuthCache) Cleanup() int {
	now := time.Now().UnixNano()
	removed := 0

	c.validKeys.Range(func(key, value interface{}) bool {
		e := value.(cacheEntry)
		if now >= e.expiresAt {
			c.validKeys.Delete(key)
			removed++
		}
		return true
	})

	return removed
}

// getOrComputeHash returns the xxhash of the API key, using cache if available.
func (c *FastPathAuthCache) getOrComputeHash(apiKey string) uint64 {
	if cached, ok := c.precomputed.Load(apiKey); ok {
		return cached.(uint64)
	}

	hash := xxhash.Sum64String(apiKey)
	c.precomputed.Store(apiKey, hash)
	return hash
}

// bloomTest checks the Bloom filter for the given hash.
func (c *FastPathAuthCache) bloomTest(hash uint64) bool {
	return c.bloom.Test(c.hashToBytes(hash))
}

// hashToBytes converts a uint64 hash to a byte slice for Bloom filter.
func (c *FastPathAuthCache) hashToBytes(hash uint64) []byte {
	b := make([]byte, 8)
	b[0] = byte(hash)
	b[1] = byte(hash >> 8)
	b[2] = byte(hash >> 16)
	b[3] = byte(hash >> 24)
	b[4] = byte(hash >> 32)
	b[5] = byte(hash >> 40)
	b[6] = byte(hash >> 48)
	b[7] = byte(hash >> 56)
	return b
}

// HashAPIKey computes the xxhash of an API key.
// This is exported for use by warming logic.
func HashAPIKey(apiKey string) uint64 {
	return xxhash.Sum64String(apiKey)
}
