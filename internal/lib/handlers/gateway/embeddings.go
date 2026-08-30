package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// EmbeddingsCache provides simple in-memory caching for embeddings.
// For production, consider using Redis or the semantic cache system.
type EmbeddingsCache struct {
	mu      sync.RWMutex
	entries map[string]*embeddingsCacheEntry
	maxSize int
	ttl     time.Duration
}

type embeddingsCacheEntry struct {
	response  EmbeddingsResponse
	expiresAt time.Time
}

// Global embeddings cache (can be replaced with Redis in production)
var globalEmbeddingsCache = NewEmbeddingsCache(10000, 1*time.Hour)

// NewEmbeddingsCache creates a new embeddings cache.
func NewEmbeddingsCache(maxSize int, ttl time.Duration) *EmbeddingsCache {
	return &EmbeddingsCache{
		entries: make(map[string]*embeddingsCacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves a cached embedding response.
func (c *EmbeddingsCache) Get(key string) (EmbeddingsResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return EmbeddingsResponse{}, false
	}

	if time.Now().After(entry.expiresAt) {
		return EmbeddingsResponse{}, false
	}

	return entry.response, true
}

// Put stores an embedding response in the cache.
func (c *EmbeddingsCache) Put(key string, resp EmbeddingsResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simple eviction: if at max size, remove oldest entries
	if len(c.entries) >= c.maxSize {
		// Remove expired entries first
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
		// If still at max, remove ~10% of entries
		if len(c.entries) >= c.maxSize {
			count := 0
			for k := range c.entries {
				delete(c.entries, k)
				count++
				if count >= c.maxSize/10 {
					break
				}
			}
		}
	}

	c.entries[key] = &embeddingsCacheEntry{
		response:  resp,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// CacheKey generates a cache key for an embeddings request.
func CacheKey(model, input string) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte(":"))
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

// GetGlobalEmbeddingsCache returns the global embeddings cache.
func GetGlobalEmbeddingsCache() *EmbeddingsCache {
	return globalEmbeddingsCache
}

// HandleEmbeddings invokes the routed provider to generate embeddings.
func HandleEmbeddings(ctx context.Context, router *Router, req EmbeddingsRequest) (EmbeddingsResponse, error) {
	provider, route, err := router.Resolve(req.Model)
	if err != nil {
		return EmbeddingsResponse{}, err
	}
	if route.ModelName != "" {
		req.Model = route.ModelName
	}

	resp, err := provider.Embed(ctx, req)
	if err != nil {
		return EmbeddingsResponse{}, err
	}

	// Set model in response if not set
	if resp.Model == "" {
		resp.Model = req.Model
	}

	// Estimate tokens for embeddings if not set by provider (approximate: ~4 chars per token)
	if resp.Usage == nil {
		estimatedTokens := len(req.Input) / 4
		if estimatedTokens < 1 {
			estimatedTokens = 1
		}
		resp.Usage = &Usage{
			PromptTokens: estimatedTokens,
			TotalTokens:  estimatedTokens,
		}
	}

	return resp, nil
}

// HandleEmbeddingsWithCache invokes the provider with caching support.
func HandleEmbeddingsWithCache(ctx context.Context, router *Router, req EmbeddingsRequest, cache *EmbeddingsCache) (EmbeddingsResponse, bool, error) {
	if cache == nil {
		cache = globalEmbeddingsCache
	}

	// Check cache
	key := CacheKey(req.Model, req.Input)
	if cached, found := cache.Get(key); found {
		return cached, true, nil
	}

	// Call provider
	resp, err := HandleEmbeddings(ctx, router, req)
	if err != nil {
		return EmbeddingsResponse{}, false, err
	}

	// Cache the response
	cache.Put(key, resp)

	return resp, false, nil
}

// EstimateEmbeddingTokens provides a rough token count estimate for text.
// Most embedding models use ~4 characters per token on average.
func EstimateEmbeddingTokens(text string) int {
	tokens := len(text) / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}
