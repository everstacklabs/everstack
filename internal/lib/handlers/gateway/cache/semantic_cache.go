// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// SemanticCacheConfig configures the semantic cache.
type SemanticCacheConfig struct {
	// MaxEntries is the maximum number of cached responses
	MaxEntries int

	// TTL is how long cached responses remain valid
	TTL time.Duration

	// MinHash configuration
	MinHash MinHashConfig

	// NumLSHBands controls the LSH index granularity
	// More bands = higher recall but more candidates to check
	NumLSHBands int
}

// DefaultSemanticCacheConfig returns sensible defaults.
// Uses 20 LSH bands (6 rows each) for high recall at 25% similarity threshold.
// Math: P(match) = 1 - (1 - s^r)^b where s=0.25, r=6, b=20 ≈ 85%
// Fewer bands with more rows per band increases recall for lower similarity thresholds.
func DefaultSemanticCacheConfig() SemanticCacheConfig {
	return SemanticCacheConfig{
		MaxEntries:  10000,
		TTL:         5 * time.Minute,
		MinHash:     DefaultMinHashConfig(),
		NumLSHBands: 20, // 20 bands × 6 rows = 120 hashes, optimized for 25% similarity
	}
}

// SemanticCacheEntry holds a cached response with its MinHash signature.
type SemanticCacheEntry struct {
	// Query is the original query text (for similarity verification)
	Query string

	// Signature is the MinHash signature for fast similarity lookup
	Signature []uint64

	// Response is the cached response data
	Response *CachedResponse

	// CreatedAt is when this entry was created
	CreatedAt time.Time

	// AccessCount tracks how often this entry is accessed (for analytics)
	AccessCount atomic.Uint64
}

// SemanticCache provides similarity-based caching using MinHash and LSH.
// It can find cached responses for queries that are semantically similar
// to previously cached queries, even if they're not exact matches.
//
// Performance characteristics:
//   - Lookup: O(candidates) where candidates is typically small due to LSH
//   - Insert: O(numBands) for LSH indexing
//   - Memory: O(maxEntries * numHashes) for signatures
type SemanticCache struct {
	cfg     SemanticCacheConfig
	hasher  *MinHasher
	lsh     *LSHIndex
	entries []*SemanticCacheEntry

	// entryMap maps entry indices to their positions (for efficient removal)
	entryMap map[int]int // index in entries slice
	nextIdx  int         // next entry index to assign

	// mu protects all fields
	mu sync.RWMutex

	// Stats for observability
	hits       atomic.Uint64
	misses     atomic.Uint64
	evictions  atomic.Uint64
	collisions atomic.Uint64 // Similar but different queries
}

// NewSemanticCache creates a new semantic cache with MinHash-based similarity.
func NewSemanticCache(cfg SemanticCacheConfig) *SemanticCache {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 10000
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 5 * time.Minute
	}
	if cfg.NumLSHBands <= 0 {
		cfg.NumLSHBands = 16
	}

	return &SemanticCache{
		cfg:      cfg,
		hasher:   NewMinHasher(cfg.MinHash),
		lsh:      NewLSHIndex(cfg.MinHash.NumHashes, cfg.NumLSHBands),
		entries:  make([]*SemanticCacheEntry, 0, cfg.MaxEntries),
		entryMap: make(map[int]int),
	}
}

// Get looks up a similar cached response for the given query.
// Returns the cached response and true if a similar query was found.
func (c *SemanticCache) Get(query string) (*CachedResponse, bool) {
	if query == "" {
		c.misses.Add(1)
		return nil, false
	}

	// Compute signature for the query
	sig := c.hasher.Signature(query)

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Use LSH to find candidate similar entries
	candidates := c.lsh.Query(sig)
	logger.WithFields(
		"query", query,
		"num_candidates", len(candidates),
		"total_entries", len(c.entries),
	).Info("Semantic cache: LSH candidates found")

	if len(candidates) == 0 {
		c.misses.Add(1)
		logger.WithFields("query", query).Info("Semantic cache: No LSH candidates, MISS")
		return nil, false
	}

	now := time.Now()
	var bestMatch *SemanticCacheEntry
	bestSimilarity := float64(0)

	// Check each candidate for actual similarity
	for _, idx := range candidates {
		pos, ok := c.entryMap[idx]
		if !ok {
			continue
		}

		entry := c.entries[pos]

		// Check TTL
		if now.Sub(entry.CreatedAt) > c.cfg.TTL {
			logger.WithFields(
				"query", query,
				"cached_query", entry.Query,
				"age", now.Sub(entry.CreatedAt),
			).Debug("Semantic cache: Entry expired")
			continue
		}

		// Compute actual similarity
		similarity := c.hasher.EstimateSimilarity(sig, entry.Signature)
		logger.WithFields(
			"query", query,
			"cached_query", entry.Query,
			"similarity", similarity,
			"threshold", c.hasher.Threshold(),
			"passes", similarity >= c.hasher.Threshold(),
		).Info("Semantic cache: Similarity check")

		if similarity >= c.hasher.Threshold() && similarity > bestSimilarity {
			bestMatch = entry
			bestSimilarity = similarity
		}
	}

	if bestMatch == nil {
		c.misses.Add(1)
		logger.WithFields(
			"query", query,
			"best_similarity", bestSimilarity,
			"threshold", c.hasher.Threshold(),
		).Info("Semantic cache: No match above threshold, MISS")
		return nil, false
	}

	// Update access count
	bestMatch.AccessCount.Add(1)
	c.hits.Add(1)

	return bestMatch.Response, true
}

// Put stores a response in the cache associated with the query.
func (c *SemanticCache) Put(query string, response *CachedResponse) {
	if query == "" || response == nil {
		return
	}

	// Compute signature
	sig := c.hasher.Signature(query)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we need to evict entries
	if len(c.entries) >= c.cfg.MaxEntries {
		c.evictOldest()
	}

	// Create new entry
	entry := SemanticCacheEntry{
		Query:     query,
		Signature: sig,
		Response:  response,
		CreatedAt: time.Now(),
	}

	// Add to entries
	pos := len(c.entries)
	c.entries = append(c.entries, &entry)
	c.entryMap[c.nextIdx] = pos

	// Add to LSH index
	c.lsh.Add(sig, c.nextIdx)

	c.nextIdx++
}

// evictOldest removes the oldest entries to make room for new ones.
// Called with lock held.
func (c *SemanticCache) evictOldest() {
	// Evict 10% of entries
	numToEvict := c.cfg.MaxEntries / 10
	if numToEvict < 1 {
		numToEvict = 1
	}

	// Find oldest entries by creation time
	type evictCandidate struct {
		pos       int
		idx       int
		createdAt time.Time
	}

	candidates := make([]evictCandidate, 0, len(c.entries))
	for idx, pos := range c.entryMap {
		candidates = append(candidates, evictCandidate{
			pos:       pos,
			idx:       idx,
			createdAt: c.entries[pos].CreatedAt,
		})
	}

	// Sort by creation time (oldest first)
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].createdAt.Before(candidates[i].createdAt) {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// Evict oldest entries
	for i := 0; i < numToEvict && i < len(candidates); i++ {
		cand := candidates[i]
		entry := c.entries[cand.pos]

		// Remove from LSH
		c.lsh.Remove(entry.Signature, cand.idx)

		// Remove from map
		delete(c.entryMap, cand.idx)

		c.evictions.Add(1)
	}

	// Compact the entries slice (rebuild without evicted entries)
	newEntries := make([]*SemanticCacheEntry, 0, len(c.entries)-numToEvict)
	newEntryMap := make(map[int]int)

	for idx, pos := range c.entryMap {
		newPos := len(newEntries)
		newEntries = append(newEntries, c.entries[pos])
		newEntryMap[idx] = newPos
	}

	c.entries = newEntries
	c.entryMap = newEntryMap
}

// Cleanup removes expired entries from the cache.
func (c *SemanticCache) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := 0

	// Find expired entries
	expiredIndices := make([]int, 0)
	for idx, pos := range c.entryMap {
		if now.Sub(c.entries[pos].CreatedAt) > c.cfg.TTL {
			expiredIndices = append(expiredIndices, idx)
		}
	}

	// Remove expired entries
	for _, idx := range expiredIndices {
		pos := c.entryMap[idx]
		entry := c.entries[pos]

		// Remove from LSH
		c.lsh.Remove(entry.Signature, idx)

		// Remove from map
		delete(c.entryMap, idx)

		removed++
	}

	// Compact if we removed entries
	if removed > 0 {
		newEntries := make([]*SemanticCacheEntry, 0, len(c.entries)-removed)
		newEntryMap := make(map[int]int)

		for idx, pos := range c.entryMap {
			newPos := len(newEntries)
			newEntries = append(newEntries, c.entries[pos])
			newEntryMap[idx] = newPos
		}

		c.entries = newEntries
		c.entryMap = newEntryMap
	}

	return removed
}

// Stats returns cache statistics.
func (c *SemanticCache) Stats() (hits, misses, evictions, size uint64, hitRatio float64) {
	hits = c.hits.Load()
	misses = c.misses.Load()
	evictions = c.evictions.Load()

	c.mu.RLock()
	size = uint64(len(c.entries))
	c.mu.RUnlock()

	total := hits + misses
	if total > 0 {
		hitRatio = float64(hits) / float64(total)
	}

	return
}

// ResetStats resets the hit/miss counters.
func (c *SemanticCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.evictions.Store(0)
	c.collisions.Store(0)
}

// Size returns the number of cached entries.
func (c *SemanticCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Clear removes all entries from the cache.
func (c *SemanticCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make([]*SemanticCacheEntry, 0, c.cfg.MaxEntries)
	c.entryMap = make(map[int]int)
	c.lsh.Clear()
	c.nextIdx = 0
}

// Threshold returns the similarity threshold.
func (c *SemanticCache) Threshold() float64 {
	return c.hasher.Threshold()
}

// EstimateSimilarity computes the estimated similarity between two queries.
// This can be used for debugging or analytics.
func (c *SemanticCache) EstimateSimilarity(query1, query2 string) float64 {
	sig1 := c.hasher.Signature(query1)
	sig2 := c.hasher.Signature(query2)
	return c.hasher.EstimateSimilarity(sig1, sig2)
}

// GetWithContext is a wrapper for Get that accepts a context (for interface compatibility)
func (c *SemanticCache) GetWithContext(ctx context.Context, query string) (*CachedResponse, bool) {
	return c.Get(query)
}

// PutWithContext is a wrapper for Put that accepts a context (for interface compatibility)
func (c *SemanticCache) PutWithContext(ctx context.Context, query string, response *CachedResponse) error {
	c.Put(query, response)
	return nil
}

// ClearWithContext is a wrapper for Clear that accepts a context (for interface compatibility)
func (c *SemanticCache) ClearWithContext(ctx context.Context) error {
	c.Clear()
	return nil
}

// SemanticCacheAdapter wraps SemanticCache to implement SemanticCacheInterface.
// This allows using the MinHash-based cache where the interface is expected.
type SemanticCacheAdapter struct {
	cache *SemanticCache
}

// NewSemanticCacheAdapter creates a new adapter wrapping the MinHash-based cache.
func NewSemanticCacheAdapter(cache *SemanticCache) *SemanticCacheAdapter {
	return &SemanticCacheAdapter{cache: cache}
}

// Get looks up a semantically similar cached response.
func (a *SemanticCacheAdapter) Get(ctx context.Context, query string) (*CachedResponse, bool) {
	return a.cache.Get(query)
}

// Put stores a response in the semantic cache.
func (a *SemanticCacheAdapter) Put(ctx context.Context, query string, response *CachedResponse) error {
	a.cache.Put(query, response)
	return nil
}

// Clear removes all entries from the cache.
func (a *SemanticCacheAdapter) Clear(ctx context.Context) error {
	a.cache.Clear()
	return nil
}

// Size returns the number of cached entries.
func (a *SemanticCacheAdapter) Size() int {
	return a.cache.Size()
}

// Threshold returns the similarity threshold.
func (a *SemanticCacheAdapter) Threshold() float64 {
	return a.cache.Threshold()
}

// Ensure SemanticCacheAdapter implements SemanticCacheInterface
var _ SemanticCacheInterface = (*SemanticCacheAdapter)(nil)
