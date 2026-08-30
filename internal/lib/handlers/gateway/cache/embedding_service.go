// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"fmt"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Router interface to avoid circular dependency with gateway package
type Router interface {
	Resolve(model string) (Provider, Route, error)
}

// Provider interface for embedding generation
type Provider interface {
	Embed(ctx context.Context, request EmbeddingsRequest) (EmbeddingsResponse, error)
}

// Route represents a routing decision
type Route struct {
	ModelName string
}

// EmbeddingsRequest represents an embedding generation request
type EmbeddingsRequest struct {
	Model string
	Input string
}

// EmbeddingsResponse represents an embedding generation response
type EmbeddingsResponse struct {
	Embedding []float64
}

// EmbeddingService handles embedding generation using the existing provider infrastructure
type EmbeddingService struct {
	router    Router
	model     string // Just the model name, router resolves provider
	cache     *EmbeddingCache
	batchSize int
	timeout   time.Duration
}

// NewEmbeddingService creates a new embedding service that uses the router to resolve models
func NewEmbeddingService(cfg validator.EmbeddingCacheConfig, router Router) (*EmbeddingService, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("embedding model name is required")
	}

	if router == nil {
		return nil, fmt.Errorf("router is required for embedding service")
	}

	// Validate that the model exists in the router
	_, _, err := router.Resolve(cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("embedding model '%s' not found in gateway configuration: %w", cfg.Model, err)
	}

	// Initialize embedding cache if enabled
	var cache *EmbeddingCache
	if cfg.CacheEmbeddings {
		cacheTTL := cfg.CacheTTL
		if cacheTTL == 0 {
			cacheTTL = 1 * time.Hour // Default TTL
		}
		cache = NewEmbeddingCache(10000, cacheTTL)
		logger.WithFields("ttl", cacheTTL).Info("Embedding cache enabled")
	}

	// Set default batch size
	batchSize := cfg.BatchSize
	if batchSize == 0 {
		batchSize = 10
	}

	// Set default timeout
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	logger.WithFields(
		"model", cfg.Model,
		"cache_enabled", cfg.CacheEmbeddings,
		"batch_size", batchSize,
		"timeout", timeout,
	).Info("Embedding service initialized")

	return &EmbeddingService{
		router:    router,
		model:     cfg.Model,
		cache:     cache,
		batchSize: batchSize,
		timeout:   timeout,
	}, nil
}

// Embed generates an embedding for the given text
func (s *EmbeddingService) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Check cache first
	if s.cache != nil {
		if cached, found := s.cache.Get(text); found {
			return cached, nil
		}
	}

	// Apply timeout and mark as internal call
	// Internal calls are not tracked in health metrics
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	ctx = contextkeys.WithInternalCall(ctx)

	// Resolve provider and route from router
	provider, route, err := s.router.Resolve(s.model)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve model '%s': %w", s.model, err)
	}

	// Create embedding request
	req := EmbeddingsRequest{
		Model: s.model,
		Input: text,
	}

	// Use route's model name if specified
	if route.ModelName != "" {
		req.Model = route.ModelName
	}

	// Call provider's Embed method
	resp, err := provider.Embed(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("embedding generation failed for model '%s': %w", s.model, err)
	}

	// Convert []float64 to []float32 for Redis
	embedding := make([]float32, len(resp.Embedding))
	for i, v := range resp.Embedding {
		embedding[i] = float32(v)
	}

	// Cache result
	if s.cache != nil {
		s.cache.Set(text, embedding)
	}

	return embedding, nil
}

// EmbedBatch generates embeddings for multiple texts
func (s *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	results := make([][]float32, len(texts))

	// Process in batches
	for i := 0; i < len(texts); i += s.batchSize {
		end := i + s.batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batch := texts[i:end]
		for j, text := range batch {
			embedding, err := s.Embed(ctx, text)
			if err != nil {
				return nil, fmt.Errorf("failed to embed text at index %d: %w", i+j, err)
			}
			results[i+j] = embedding
		}
	}

	return results, nil
}

// Model returns the embedding model name
func (s *EmbeddingService) Model() string {
	return s.model
}

// EmbeddingCache caches embeddings in memory to avoid re-computation
type EmbeddingCache struct {
	cache *lru.Cache[string, *embeddingEntry]
	ttl   time.Duration
}

type embeddingEntry struct {
	embedding []float32
	createdAt time.Time
}

// NewEmbeddingCache creates a new embedding cache
func NewEmbeddingCache(size int, ttl time.Duration) *EmbeddingCache {
	cache, _ := lru.New[string, *embeddingEntry](size)
	return &EmbeddingCache{
		cache: cache,
		ttl:   ttl,
	}
}

// Get retrieves an embedding from the cache
func (c *EmbeddingCache) Get(text string) ([]float32, bool) {
	entry, ok := c.cache.Get(text)
	if !ok {
		return nil, false
	}

	// Check TTL
	if time.Since(entry.createdAt) > c.ttl {
		c.cache.Remove(text)
		return nil, false
	}

	return entry.embedding, true
}

// Set stores an embedding in the cache
func (c *EmbeddingCache) Set(text string, embedding []float32) {
	entry := &embeddingEntry{
		embedding: embedding,
		createdAt: time.Now(),
	}
	c.cache.Add(text, entry)
}

// Size returns the number of cached embeddings
func (c *EmbeddingCache) Size() int {
	return c.cache.Len()
}

// Clear removes all cached embeddings
func (c *EmbeddingCache) Clear() {
	c.cache.Purge()
}
