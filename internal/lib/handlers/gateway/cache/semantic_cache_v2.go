// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// SemanticCacheV2 implements semantic similarity caching using embeddings and Redis
type SemanticCacheV2 struct {
	vectorStore  VectorStore
	memoryCache  *SemanticCache // Fallback to existing MinHash-based cache
	embedder     *EmbeddingService
	tokenizer    Tokenizer
	config       validator.SemanticCacheConfig
	tracingHooks TracingHooks // Optional tracing hooks for span hierarchy

	// Stats
	hits       atomic.Uint64
	misses     atomic.Uint64
	memoryHits atomic.Uint64
	errors     atomic.Uint64
}

// NewSemanticCacheV2 creates a new semantic cache with embeddings and vector search
func NewSemanticCacheV2(
	cfg validator.SemanticCacheConfig,
	redisClient *RedisClient,
	router Router,
) (*SemanticCacheV2, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("semantic cache is not enabled")
	}

	if router == nil {
		return nil, fmt.Errorf("router is required for semantic cache")
	}

	// Initialize tokenizer
	var tokenizer Tokenizer
	if cfg.Tokenizer.Type == "bpe" && cfg.Tokenizer.VocabFile != "" {
		tk, err := NewBPETokenizer(cfg.Tokenizer.VocabFile)
		if err != nil {
			logger.WithError(err).Warn("Failed to load BPE tokenizer, falling back to simple tokenizer")
			tokenizer = NewSimpleTokenizer()
		} else {
			tokenizer = tk
		}
	} else {
		tokenizer = NewSimpleTokenizer()
	}

	// Initialize embedding service using router
	// Router will resolve the model from gateway.models configuration
	embedder, err := NewEmbeddingService(cfg.Embedding, router)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize embedding service: %w", err)
	}

	// Auto-detect dimensions if not specified
	dimensions := cfg.Embedding.Dimensions
	if dimensions == 0 {
		// Default to 1536 (OpenAI text-embedding-3-small)
		dimensions = 1536
		logger.WithFields("model", cfg.Embedding.Model).Info("Dimensions not specified, using default 1536")
	}

	// Initialize vector store if Redis is available
	var vectorStore VectorStore
	if redisClient != nil && redisClient.IsConnected() {
		indexName := redisClient.Config().Search.IndexName
		if indexName == "" {
			indexName = "semantic_cache_idx"
		}

		vs, err := NewRedisVectorStore(redisClient, indexName, dimensions)
		if err != nil {
			logger.WithError(err).Warn("Failed to initialize vector store, will use memory cache only")
		} else {
			vectorStore = vs
		}
	}

	// Initialize memory cache as fallback
	// Use existing MinHash-based semantic cache
	memoryCache := NewSemanticCache(SemanticCacheConfig{
		MaxEntries:  cfg.MaxEntries,
		TTL:         cfg.TTL,
		MinHash:     DefaultMinHashConfig(),
		NumLSHBands: 64,
	})

	logger.WithFields(
		"backend", cfg.Backend,
		"model", cfg.Embedding.Model,
		"threshold", cfg.SimilarityThreshold,
		"vector_store_enabled", vectorStore != nil,
	).Info("SemanticCacheV2 initialized")

	return &SemanticCacheV2{
		vectorStore:  vectorStore,
		memoryCache:  memoryCache,
		embedder:     embedder,
		tokenizer:    tokenizer,
		config:       cfg,
		tracingHooks: &NoopTracingHooks{}, // Default to no-op, can be set via SetTracingHooks
	}, nil
}

// SetTracingHooks sets the tracing hooks for this cache.
// This allows tracing to be injected without the cache package importing telemetry.
func (c *SemanticCacheV2) SetTracingHooks(hooks TracingHooks) {
	if hooks != nil {
		c.tracingHooks = hooks
	}
}

// Get looks up a semantically similar cached response
// Tracing hooks are used to create proper span hierarchy for embedding and search operations.
func (c *SemanticCacheV2) Get(ctx context.Context, query string) (*CachedResponse, bool) {
	if query == "" {
		c.misses.Add(1)
		return nil, false
	}

	// Extract tenantID from context — skip vector store cache when empty
	// to prevent cross-tenant data leakage. Memory cache fallback is
	// process-local so it's inherently single-tenant safe.
	tenantID := contextkeys.GetTenantID(ctx)

	// 1. Generate embedding using router (automatically resolves provider)
	// Wrap in embedding span for proper trace hierarchy
	embeddingCtx, embeddingSpan := c.tracingHooks.StartEmbeddingSpan(ctx, c.embedder.Model(), len(query))
	embedding, err := c.embedder.Embed(embeddingCtx, query)
	if err != nil {
		embeddingSpan.SetError(err)
		embeddingSpan.End()

		logger.WithError(err).Warn("Failed to generate embedding, falling back to memory cache")
		c.errors.Add(1)
		// Fallback to memory cache with MinHash
		resp, found := c.memoryCache.Get(query)
		if found {
			c.memoryHits.Add(1)
		} else {
			c.misses.Add(1)
		}
		return resp, found
	}
	embeddingSpan.SetAttributes(map[string]interface{}{
		"cache.semantic.embedding_dimensions": len(embedding),
	})
	embeddingSpan.SetSuccess()
	embeddingSpan.End()

	// 2. Search in Redis vector store if available and tenantID is set
	// Wrap in search span for proper trace hierarchy
	if c.vectorStore != nil && tenantID != "" {
		searchCtx, searchSpan := c.tracingHooks.StartSearchSpan(ctx, "redis")
		results, err := c.vectorStore.Search(searchCtx, embedding, 5, c.config.SimilarityThreshold, tenantID)
		if err != nil {
			// Use SetWarning instead of SetError - cache failures are non-critical
			// The request will continue with fallback to provider
			searchSpan.SetWarning(err)
			searchSpan.End()

			logger.WithError(err).Warn("Vector search failed, falling back to memory cache")
			c.errors.Add(1)
		} else if len(results) > 0 {
			// Found a match above threshold
			searchSpan.SetAttributes(map[string]interface{}{
				"cache.semantic.candidates_returned": len(results),
				"cache.semantic.best_score":          results[0].Score,
				"cache.semantic.threshold_met":       true,
			})
			searchSpan.SetSuccess()
			searchSpan.End()

			c.hits.Add(1)
			logger.WithFields(
				"query", query,
				"similarity", results[0].Score,
				"cached_query", results[0].Query,
			).Debug("Semantic cache HIT (vector store)")
			return &results[0].Response, true
		} else {
			searchSpan.SetAttributes(map[string]interface{}{
				"cache.semantic.candidates_returned": 0,
				"cache.semantic.threshold_met":       false,
			})
			searchSpan.SetSuccess()
			searchSpan.End()
		}
	}

	// 3. Fallback to memory cache with MinHash
	if resp, found := c.memoryCache.Get(query); found {
		c.memoryHits.Add(1)
		logger.WithFields("query", query).Debug("Semantic cache HIT (memory fallback)")
		return resp, true
	}

	c.misses.Add(1)
	logger.WithFields("query", query).Debug("Semantic cache MISS")
	return nil, false
}

// Put stores a response in the semantic cache
// Tracing hooks are used to create proper span hierarchy for embedding operations.
func (c *SemanticCacheV2) Put(ctx context.Context, query string, response *CachedResponse) error {
	if query == "" || response == nil {
		return fmt.Errorf("query and response are required")
	}

	// Extract tenantID from context — skip vector store when empty
	// to prevent unscoped cache entries from being created.
	tenantID := contextkeys.GetTenantID(ctx)

	// 1. Generate embedding using router
	// Wrap in embedding span for proper trace hierarchy
	embeddingCtx, embeddingSpan := c.tracingHooks.StartEmbeddingSpan(ctx, c.embedder.Model(), len(query))
	embedding, err := c.embedder.Embed(embeddingCtx, query)
	if err != nil {
		embeddingSpan.SetError(err)
		embeddingSpan.End()

		logger.WithError(err).Warn("Failed to generate embedding for caching")
		c.errors.Add(1)
		// Still store in memory cache
		c.memoryCache.Put(query, response)
		return err
	}
	embeddingSpan.SetAttributes(map[string]interface{}{
		"cache.semantic.embedding_dimensions": len(embedding),
	})
	embeddingSpan.SetSuccess()
	embeddingSpan.End()

	// 2. Store in Redis vector store if available and tenantID is set
	if c.vectorStore != nil && tenantID != "" {
		key := fmt.Sprintf("cache:semantic:%s:%s", tenantID, hashQuery(query))
		if err := c.vectorStore.Store(ctx, key, embedding, response, tenantID); err != nil {
			logger.WithError(err).Warn("Failed to store in Redis vector store")
			c.errors.Add(1)
		} else {
			logger.WithFields("query", query, "key", key).Debug("Stored in vector store")
		}
	}

	// 3. Also store in memory cache as fallback
	c.memoryCache.Put(query, response)

	return nil
}

// Stats returns cache statistics
func (c *SemanticCacheV2) Stats() (hits, misses, memoryHits, errors uint64, hitRatio float64) {
	hits = c.hits.Load()
	misses = c.misses.Load()
	memoryHits = c.memoryHits.Load()
	errors = c.errors.Load()

	totalHits := hits + memoryHits
	total := totalHits + misses
	if total > 0 {
		hitRatio = float64(totalHits) / float64(total)
	}

	return
}

// ResetStats resets the cache statistics
func (c *SemanticCacheV2) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.memoryHits.Store(0)
	c.errors.Store(0)
}

// Clear removes all entries from the cache for the tenant in context.
func (c *SemanticCacheV2) Clear(ctx context.Context) error {
	tenantID := contextkeys.GetTenantID(ctx)

	// Clear vector store — scoped to tenant when tenantID is available
	if c.vectorStore != nil {
		if err := c.vectorStore.Clear(ctx, tenantID); err != nil {
			logger.WithError(err).Warn("Failed to clear vector store")
		}
	}

	// Clear memory cache
	c.memoryCache.Clear()

	logger.Info("Semantic cache cleared")
	return nil
}

// Size returns the number of cached entries (from memory cache)
func (c *SemanticCacheV2) Size() int {
	return c.memoryCache.Size()
}

// Threshold returns the similarity threshold
func (c *SemanticCacheV2) Threshold() float64 {
	return c.config.SimilarityThreshold
}

// Model returns the embedding model name
func (c *SemanticCacheV2) Model() string {
	return c.config.Embedding.Model
}

// hashQuery generates a hash for a query string
func hashQuery(query string) string {
	hash := sha256.Sum256([]byte(query))
	return hex.EncodeToString(hash[:])
}

// SemanticCacheInterface defines the interface for semantic caches
// This allows both SemanticCache (MinHash) and SemanticCacheV2 (embeddings) to be used interchangeably
type SemanticCacheInterface interface {
	Get(ctx context.Context, query string) (*CachedResponse, bool)
	Put(ctx context.Context, query string, response *CachedResponse) error
	Clear(ctx context.Context) error
	Size() int
	Threshold() float64
}

// Ensure SemanticCacheV2 implements the interface
var _ SemanticCacheInterface = (*SemanticCacheV2)(nil)
