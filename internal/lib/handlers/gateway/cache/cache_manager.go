// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// CacheManager manages both exact and semantic caches
type CacheManager struct {
	exactCache    *ExactCache
	semanticCache SemanticCacheInterface
	redisClient   *RedisClient
	config        validator.CacheConfig
}

// NewCacheManager creates a new cache manager with the given configuration
func NewCacheManager(cfg validator.CacheConfig, router Router) (*CacheManager, error) {
	return NewCacheManagerWithRedis(cfg, router, nil)
}

// NewCacheManagerWithRedis creates a new cache manager with an existing Redis client.
// If redisClient is nil, a new one will be created based on the configuration.
// This allows reusing an existing Redis connection to avoid duplicate connections.
func NewCacheManagerWithRedis(cfg validator.CacheConfig, router Router, redisClient *RedisClient) (*CacheManager, error) {
	if router == nil {
		return nil, fmt.Errorf("router is required for cache manager")
	}

	var err error

	// Initialize Redis if not provided and configured
	if redisClient == nil && cfg.Type == "redis" && cfg.Redis.Address != "" {
		redisClient, err = NewRedisClient(cfg.Redis)
		if err != nil {
			logger.WithError(err).Warn("Failed to connect to Redis, falling back to memory")
			// Continue without Redis - will use memory caches
		}
	} else if redisClient != nil {
		logger.Info("Reusing existing Redis client for cache manager")
	}

	// Initialize exact cache
	exactCfg := ExactCacheConfig{
		MaxEntries: cfg.Memory.MaxSize,
		TTL:        cfg.Memory.TTL,
		Enabled:    cfg.Enabled,
	}
	if exactCfg.MaxEntries == 0 {
		exactCfg.MaxEntries = 50000
	}
	if exactCfg.TTL == 0 {
		exactCfg.TTL = 10 * time.Minute
	}

	exactCache, err := NewExactCache(exactCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize exact cache: %w", err)
	}

	logger.WithFields(
		"max_entries", exactCfg.MaxEntries,
		"ttl", exactCfg.TTL,
	).Info("Exact cache initialized")

	// Initialize semantic cache if enabled
	var semanticCache SemanticCacheInterface
	if cfg.Semantic.Enabled {
		backend := cfg.Semantic.Backend
		if backend == "" {
			backend = "memory" // Default to memory if not specified
		}

		// Decide which semantic cache implementation to use based on backend config
		useRedis := false
		if backend == "redis" || backend == "auto" {
			// Use Redis vector store if available
			if redisClient != nil && redisClient.IsConnected() && cfg.Redis.Search.Enabled {
				useRedis = true
			} else if backend == "redis" {
				logger.Warn("Redis backend requested but Redis is not available or RedisSearch not enabled, falling back to memory")
			}
		}

		if useRedis {
			// Use SemanticCacheV2 with Redis vector store and embeddings
			v2Cache, err := NewSemanticCacheV2(cfg.Semantic, redisClient, router)
			if err != nil {
				logger.WithError(err).Warn("Failed to initialize SemanticCacheV2, falling back to MinHash")
				// Fallback to MinHash
				minHashCache := NewSemanticCache(SemanticCacheConfig{
					MaxEntries:  cfg.Semantic.MaxEntries,
					TTL:         cfg.Semantic.TTL,
					MinHash:     DefaultMinHashConfig(),
					NumLSHBands: 20,
				})
				semanticCache = NewSemanticCacheAdapter(minHashCache)
				logger.WithFields(
					"max_entries", cfg.Semantic.MaxEntries,
					"ttl", cfg.Semantic.TTL,
					"type", "minhash",
				).Info("Semantic cache initialized (MinHash fallback)")
			} else {
				semanticCache = v2Cache
				logger.WithFields(
					"max_entries", cfg.Semantic.MaxEntries,
					"ttl", cfg.Semantic.TTL,
					"type", "embedding+redis",
					"model", cfg.Semantic.Embedding.Model,
				).Info("Semantic cache initialized (SemanticCacheV2 with Redis vector store)")
			}
		} else {
			// Use MinHash-based semantic cache - purely local, no embedding API calls
			minHashCache := NewSemanticCache(SemanticCacheConfig{
				MaxEntries:  cfg.Semantic.MaxEntries,
				TTL:         cfg.Semantic.TTL,
				MinHash:     DefaultMinHashConfig(),
				NumLSHBands: 20, // Optimized for ~25% similarity threshold
			})
			semanticCache = NewSemanticCacheAdapter(minHashCache)
			logger.WithFields(
				"max_entries", cfg.Semantic.MaxEntries,
				"ttl", cfg.Semantic.TTL,
				"type", "minhash",
			).Info("Semantic cache initialized (MinHash-based, no external API calls)")
		}
	}

	return &CacheManager{
		exactCache:    exactCache,
		semanticCache: semanticCache,
		redisClient:   redisClient,
		config:        cfg,
	}, nil
}

// ExactCache returns the exact match cache
func (cm *CacheManager) ExactCache() *ExactCache {
	return cm.exactCache
}

// SemanticCache returns the semantic similarity cache
func (cm *CacheManager) SemanticCache() SemanticCacheInterface {
	return cm.semanticCache
}

// IsSemanticCacheEnabled returns whether semantic caching is enabled
func (cm *CacheManager) IsSemanticCacheEnabled() bool {
	return cm.semanticCache != nil
}

// SetSemanticCacheTracingHooks sets tracing hooks for the semantic cache.
// This is only relevant for SemanticCacheV2 (embedding-based) which makes external API calls.
// For MinHash-based cache, this is a no-op.
func (cm *CacheManager) SetSemanticCacheTracingHooks(hooks TracingHooks) {
	if cm.semanticCache == nil {
		return
	}

	// Check if the semantic cache is SemanticCacheV2 (supports tracing hooks)
	if v2Cache, ok := cm.semanticCache.(*SemanticCacheV2); ok {
		v2Cache.SetTracingHooks(hooks)
	}
	// For MinHash-based cache (via adapter), this is a no-op
}

// GetSemanticCachedResponse looks up a semantically similar cached response
func (cm *CacheManager) GetSemanticCachedResponse(ctx context.Context, query string) (*CachedResponse, bool) {
	if cm.semanticCache == nil {
		return nil, false
	}
	return cm.semanticCache.Get(ctx, query)
}

// CacheSemanticResponse stores a response in the semantic cache
func (cm *CacheManager) CacheSemanticResponse(ctx context.Context, query string, resp *CachedResponse) error {
	if cm.semanticCache == nil {
		return fmt.Errorf("semantic cache not enabled")
	}
	return cm.semanticCache.Put(ctx, query, resp)
}

// Close closes all cache connections
func (cm *CacheManager) Close() error {
	if cm.redisClient != nil {
		return cm.redisClient.Close()
	}
	return nil
}

// Stats returns statistics for both caches
func (cm *CacheManager) Stats() CacheManagerStats {
	stats := CacheManagerStats{}

	// Exact cache stats
	if cm.exactCache != nil {
		hits, misses, evicts, hitRatio, size := cm.exactCache.Stats()
		stats.Exact = ExactCacheStats{
			Hits:     hits,
			Misses:   misses,
			Evicts:   evicts,
			HitRatio: hitRatio,
			Size:     size,
		}
	}

	// Semantic cache stats
	if cm.semanticCache != nil {
		stats.Semantic = SemanticCacheStats{
			Size:      cm.semanticCache.Size(),
			Threshold: cm.semanticCache.Threshold(),
		}

		// Try to get detailed stats from the underlying cache implementation
		if adapter, ok := cm.semanticCache.(*SemanticCacheAdapter); ok {
			hits, misses, evictions, size, hitRatio := adapter.cache.Stats()
			stats.Semantic.Hits = hits
			stats.Semantic.Misses = misses
			stats.Semantic.Size = int(size)
			stats.Semantic.HitRatio = hitRatio
			_ = evictions // Available but not exposed in stats struct
		}
	}

	return stats
}

// CacheManagerStats holds statistics for all caches
type CacheManagerStats struct {
	Exact    ExactCacheStats
	Semantic SemanticCacheStats
}

// ExactCacheStats holds statistics for the exact cache
type ExactCacheStats struct {
	Hits     uint64
	Misses   uint64
	Evicts   uint64
	HitRatio float64
	Size     int
}

// SemanticCacheStats holds statistics for the semantic cache
type SemanticCacheStats struct {
	Hits       uint64
	Misses     uint64
	MemoryHits uint64
	Errors     uint64
	HitRatio   float64
	Size       int
	Threshold  float64
}
