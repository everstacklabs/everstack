// Package fastpath provides high-performance request processing for the gateway.
// It implements HFT-inspired optimizations including:
//   - Lock-free authentication caching with Bloom filters
//   - Pre-computed routing tables
//   - Buffer pooling for zero-allocation streaming
//   - Exact-match response caching
package fastpath

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/metrics"
)

// Engine is the fast-path request processing engine.
// It coordinates all fast-path components and provides a unified interface
// for high-performance request handling.
type Engine struct {
	// enabled controls whether the fast-path is active
	enabled atomic.Bool

	// authCache is the lock-free API key validation cache
	authCache *FastPathAuthCache

	// routerCache is the pre-computed routing table
	routerCache *RouterCache

	// cacheManager manages both exact and semantic caches
	cacheManager *cache.CacheManager

	// Legacy fields for backward compatibility
	// exactCache is the response cache for exact request matches
	exactCache *cache.ExactCache

	// semanticCache is the semantic similarity cache for near-duplicate queries (MinHash)
	semanticCache *cache.SemanticCache

	// onnxCache is the ONNX-based semantic cache for deep semantic understanding
	onnxCache *cache.ONNXCache

	// config holds the current configuration
	config validator.FastPathFeaturesConfig

	// mu protects configuration updates
	mu sync.RWMutex

	// stats
	requestsProcessed atomic.Uint64
	requestsFastPath  atomic.Uint64
	requestsFallback  atomic.Uint64

	// tenantEnabledFn lets callers gate cache lookup/insert per request
	// based on a tenant's runtime_config. Returning false skips the
	// cache entirely (useful when a tenant has cache.enabled = false).
	// nil = always allowed (legacy, single-tenant behaviour).
	tenantEnabledFn func(ctx context.Context) bool
}

// SetTenantEnabledFn installs a per-request gate. The engine calls fn
// before every cache lookup and insert; returning false short-circuits
// to "no cache" without disabling the engine globally. Wired at startup
// to consult rtconfig.GetCache(tenantID).Enabled.
func (e *Engine) SetTenantEnabledFn(fn func(ctx context.Context) bool) {
	e.mu.Lock()
	e.tenantEnabledFn = fn
	e.mu.Unlock()
}

// tenantAllows is the safe accessor used on the request hot path.
func (e *Engine) tenantAllows(ctx context.Context) bool {
	e.mu.RLock()
	fn := e.tenantEnabledFn
	e.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn(ctx)
}

// EngineConfig contains configuration for the fast-path engine.
type EngineConfig struct {
	// Features contains feature flags
	Features validator.FastPathFeaturesConfig
}

// DefaultEngineConfig returns sensible defaults.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		Features: validator.DefaultFastPathFeaturesConfig(),
	}
}

// NewEngine creates a new fast-path engine with the given configuration.
// This is the legacy constructor that uses the old fastpath cache config.
// For new code, use NewEngineWithCacheManager instead.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	// Create auth cache
	authCache := NewFastPathAuthCache(AuthCacheConfig{
		ExpectedKeys:      cfg.Features.Auth.BloomFilterSize,
		FalsePositiveRate: cfg.Features.Auth.BloomFalsePositiveRate,
		TTL:               cfg.Features.Auth.CacheTTL,
	})

	// Create router cache
	routerCache := NewRouterCache()

	// Create exact cache
	exactCache, err := cache.NewExactCache(cache.ExactCacheConfig{
		MaxEntries: cfg.Features.Cache.Exact.MaxEntries,
		TTL:        cfg.Features.Cache.Exact.TTL,
		Enabled:    cfg.Features.Cache.Exact.Enabled,
	})
	if err != nil {
		return nil, err
	}

	// Create semantic cache (MinHash for lexical similarity)
	var semanticCache *cache.SemanticCache
	if cfg.Features.Cache.Semantic.Enabled {
		semanticCache = cache.NewSemanticCache(cache.SemanticCacheConfig{
			MaxEntries: cfg.Features.Cache.Semantic.MaxEntries,
			TTL:        cfg.Features.Cache.Semantic.TTL,
			MinHash: cache.MinHashConfig{
				NumHashes:           cfg.Features.Cache.Semantic.NumHashes,
				ShingleSize:         cfg.Features.Cache.Semantic.ShingleSize,
				SimilarityThreshold: cfg.Features.Cache.Semantic.SimilarityThreshold,
			},
			NumLSHBands: 16, // Good default for 128 hashes
		})
	}

	// Create ONNX cache (optional - for deep semantic understanding)
	// Only initialize if model file exists
	var onnxCache *cache.ONNXCache
	if cfg.Features.Cache.Semantic.Enabled {
		// Try to initialize ONNX cache, but don't fail if unavailable
		onnxCfg := cache.DefaultONNXCacheConfig()
		onnxCache, _ = cache.NewONNXCache(onnxCfg)
		// Silently ignore errors - ONNX is optional, MinHash will handle it
	}

	engine := &Engine{
		authCache:     authCache,
		routerCache:   routerCache,
		exactCache:    exactCache,
		semanticCache: semanticCache,
		onnxCache:     onnxCache,
		config:        cfg.Features,
	}

	engine.enabled.Store(cfg.Features.Enabled)

	return engine, nil
}

// NewEngineWithCacheManager creates a new fast-path engine with the unified cache manager.
// This is the recommended constructor that uses the new cache configuration.
func NewEngineWithCacheManager(cfg EngineConfig, cacheConfig validator.CacheConfig, router cache.Router) (*Engine, error) {
	return NewEngineWithCacheManagerAndRedis(cfg, cacheConfig, router, nil)
}

// NewEngineWithCacheManagerAndRedis creates a new fast-path engine with the unified cache manager
// and an optional existing Redis client. If redisClient is nil, a new one will be created.
// This allows reusing an existing Redis connection to avoid duplicate connections.
func NewEngineWithCacheManagerAndRedis(cfg EngineConfig, cacheConfig validator.CacheConfig, router cache.Router, redisClient *cache.RedisClient) (*Engine, error) {
	if router == nil && cacheConfig.Semantic.Enabled {
		return nil, fmt.Errorf("router is required when semantic cache is enabled")
	}

	// Create auth cache
	authCache := NewFastPathAuthCache(AuthCacheConfig{
		ExpectedKeys:      cfg.Features.Auth.BloomFilterSize,
		FalsePositiveRate: cfg.Features.Auth.BloomFalsePositiveRate,
		TTL:               cfg.Features.Auth.CacheTTL,
	})

	// Create router cache
	routerCache := NewRouterCache()

	// Create cache manager with router and optional Redis client
	var cacheManager *cache.CacheManager
	var err error
	if router != nil {
		cacheManager, err = cache.NewCacheManagerWithRedis(cacheConfig, router, redisClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create cache manager: %w", err)
		}
	}

	// Get exact and semantic caches from manager for backward compatibility
	var exactCache *cache.ExactCache
	var semanticCache *cache.SemanticCache
	if cacheManager != nil {
		exactCache = cacheManager.ExactCache()
		// Note: semanticCache will be nil if using SemanticCacheV2
		// The engine will use cacheManager methods instead
	}

	engine := &Engine{
		authCache:     authCache,
		routerCache:   routerCache,
		cacheManager:  cacheManager,
		exactCache:    exactCache,
		semanticCache: semanticCache,
		config:        cfg.Features,
	}

	engine.enabled.Store(cfg.Features.Enabled)

	return engine, nil
}

// SetEnabled enables or disables the fast-path engine.
func (e *Engine) SetEnabled(enabled bool) {
	e.enabled.Store(enabled)
}

// IsEnabled returns whether the fast-path is enabled.
func (e *Engine) IsEnabled() bool {
	return e.enabled.Load()
}

// AuthCache returns the authentication cache for direct access.
func (e *Engine) AuthCache() *FastPathAuthCache {
	return e.authCache
}

// RouterCache returns the router cache for direct access.
func (e *Engine) RouterCache() *RouterCache {
	return e.routerCache
}

// ExactCache returns the exact response cache for direct access.
func (e *Engine) ExactCache() *cache.ExactCache {
	return e.exactCache
}

// CacheManager returns the cache manager for direct access.
func (e *Engine) CacheManager() *cache.CacheManager {
	return e.cacheManager
}

// SetSemanticCacheTracingHooks sets tracing hooks on the semantic cache.
// This allows tracing to be injected without creating import cycles.
func (e *Engine) SetSemanticCacheTracingHooks(hooks cache.TracingHooks) {
	if e.cacheManager != nil {
		e.cacheManager.SetSemanticCacheTracingHooks(hooks)
	}
}

// ValidateAPIKey validates an API key using the fast-path cache.
// Returns:
//   - valid: true if the key is confirmed valid (fast-path hit)
//   - definitelyInvalid: always false (this is a positive cache, not a negative cache)
//   - If valid is false, the caller MUST fall through to DB validation
func (e *Engine) ValidateAPIKey(apiKey string) (valid bool, definitelyInvalid bool) {
	if !e.enabled.Load() {
		return false, false
	}

	tracker := metrics.StartLatencyTracker()
	tracker.StartStage(metrics.StageAuth)
	defer tracker.EndStage(metrics.StageAuth)

	valid, definitelyInvalid = e.authCache.IsValid(apiKey)

	if valid {
		metrics.RecordAuthCacheHit()
	} else {
		metrics.RecordAuthCacheMiss()
	}

	return valid, definitelyInvalid
}

// MarkAPIKeyValid adds a validated API key to the cache.
func (e *Engine) MarkAPIKeyValid(apiKey string) {
	if e.enabled.Load() {
		e.authCache.MarkValid(apiKey)
	}
}

// MarkAPIKeyInvalid removes an API key from the cache.
func (e *Engine) MarkAPIKeyInvalid(apiKey string) {
	if e.enabled.Load() {
		e.authCache.MarkInvalid(apiKey)
	}
}

// ResolveModel looks up the provider for a model using the fast-path cache.
// Returns the provider info and true if found, nil and false otherwise.
func (e *Engine) ResolveModel(model string) (*ProviderInfo, bool) {
	if !e.enabled.Load() || !e.routerCache.IsWarmed() {
		return nil, false
	}

	tracker := metrics.StartLatencyTracker()
	tracker.StartStage(metrics.StageRouting)
	defer tracker.EndStage(metrics.StageRouting)

	info, found := e.routerCache.Resolve(model)
	if found {
		metrics.RecordRouterCacheHit()
	} else {
		metrics.RecordRouterCacheMiss()
	}

	return info, found
}

// GetCachedResponse looks up a cached response for the given request.
// Returns the cached response and true if found and not expired.
// Legacy method without context - prefer GetCachedResponseWithContext for new code.
func (e *Engine) GetCachedResponse(req cache.ChatRequest) (*cache.CachedResponse, bool) {
	return e.GetCachedResponseWithContext(context.Background(), req)
}

// GetCachedResponseWithContext looks up a cached response with context support.
// Returns the cached response and true if found and not expired.
// Note: Tracing is handled at the gRPC/HTTP handler layer to avoid import cycles.
func (e *Engine) GetCachedResponseWithContext(ctx context.Context, req cache.ChatRequest) (*cache.CachedResponse, bool) {
	if !e.enabled.Load() {
		return nil, false
	}
	if !e.tenantAllows(ctx) {
		return nil, false
	}

	tracker := metrics.StartLatencyTracker()
	tracker.StartStage(metrics.StageCacheLookup)
	defer tracker.EndStage(metrics.StageCacheLookup)

	// Use the context-aware version
	resp, found := e.exactCache.GetWithContext(ctx, req)
	if found {
		metrics.RecordExactCacheHit()
	} else {
		metrics.RecordExactCacheMiss()
	}

	return resp, found
}

// CacheResponse stores a response in the cache.
// Legacy method without context - prefer CacheResponseWithContext for new code.
func (e *Engine) CacheResponse(req cache.ChatRequest, resp *cache.CachedResponse) {
	e.CacheResponseWithContext(context.Background(), req, resp)
}

// CacheResponseWithContext stores a response in the cache with context support.
// Note: Tracing is handled at the gRPC/HTTP handler layer to avoid import cycles.
func (e *Engine) CacheResponseWithContext(ctx context.Context, req cache.ChatRequest, resp *cache.CachedResponse) {
	if !e.enabled.Load() {
		return
	}
	if !e.tenantAllows(ctx) {
		return
	}

	tracker := metrics.StartLatencyTracker()
	tracker.StartStage(metrics.StageCacheStore)
	e.exactCache.SetWithContext(ctx, req, resp)
	tracker.EndStage(metrics.StageCacheStore)
}

// GetSemanticCachedResponse looks up a semantically similar cached response.
// This is called when exact cache misses to find similar queries.
// Returns the cached response and true if a similar query was found.
// Legacy method without context - prefer GetSemanticCachedResponseWithContext for new code.
func (e *Engine) GetSemanticCachedResponse(query string) (*cache.CachedResponse, bool) {
	return e.GetSemanticCachedResponseWithContext(context.Background(), query)
}

// GetSemanticCachedResponseWithContext looks up a semantically similar cached response with context support.
// This is called when exact cache misses to find similar queries.
// Returns the cached response and true if a similar query was found.
// Note: Tracing is handled at the gRPC/HTTP handler layer to avoid import cycles.
func (e *Engine) GetSemanticCachedResponseWithContext(ctx context.Context, query string) (*cache.CachedResponse, bool) {
	if !e.enabled.Load() {
		return nil, false
	}

	// Use cache manager if available (new path)
	if e.cacheManager != nil && e.cacheManager.IsSemanticCacheEnabled() {
		tracker := metrics.StartLatencyTracker()
		tracker.StartStage(metrics.StageSemanticLookup)
		defer tracker.EndStage(metrics.StageSemanticLookup)

		resp, found := e.cacheManager.GetSemanticCachedResponse(ctx, query)
		if found {
			metrics.RecordSemanticCacheHit()
		} else {
			metrics.RecordSemanticCacheMiss()
		}
		return resp, found
	}

	// Hybrid approach: Try MinHash first (fast), then ONNX (deep semantic)
	tracker := metrics.StartLatencyTracker()
	tracker.StartStage(metrics.StageSemanticLookup)
	defer tracker.EndStage(metrics.StageSemanticLookup)

	// 1. Try MinHash cache (lexical similarity, ~1-2ms)
	if e.semanticCache != nil {
		if resp, found := e.semanticCache.Get(query); found {
			metrics.RecordSemanticCacheHit()
			return resp, true
		}
	}

	// 2. Try ONNX cache (deep semantic, ~5ms)
	if e.onnxCache != nil {
		if resp, found := e.onnxCache.GetWithContext(ctx, query); found {
			metrics.RecordSemanticCacheHit()
			return resp, true
		}
	}

	metrics.RecordSemanticCacheMiss()
	return nil, false
}

// CacheSemanticResponse stores a response in the semantic cache.
// This associates the response with the query for similarity matching.
// Legacy method without context - prefer CacheSemanticResponseWithContext for new code.
func (e *Engine) CacheSemanticResponse(query string, resp *cache.CachedResponse) {
	e.CacheSemanticResponseWithContext(context.Background(), query, resp)
}

// CacheSemanticResponseWithContext stores a response in the semantic cache with context support.
// This associates the response with the query for similarity matching.
// Note: Tracing is handled at the gRPC/HTTP handler layer to avoid import cycles.
func (e *Engine) CacheSemanticResponseWithContext(ctx context.Context, query string, resp *cache.CachedResponse) {
	if !e.enabled.Load() {
		return
	}

	// Use cache manager if available (new path)
	if e.cacheManager != nil && e.cacheManager.IsSemanticCacheEnabled() {
		tracker := metrics.StartLatencyTracker()
		tracker.StartStage(metrics.StageSemanticStore)
		_ = e.cacheManager.CacheSemanticResponse(ctx, query, resp)
		tracker.EndStage(metrics.StageSemanticStore)
		return
	}

	// Store in both MinHash and ONNX caches
	tracker := metrics.StartLatencyTracker()
	tracker.StartStage(metrics.StageSemanticStore)
	defer tracker.EndStage(metrics.StageSemanticStore)

	// Store in MinHash cache (fast lexical matching)
	if e.semanticCache != nil {
		e.semanticCache.Put(query, resp)
	}

	// Store in ONNX cache (deep semantic matching)
	if e.onnxCache != nil {
		e.onnxCache.PutWithContext(ctx, query, resp)
	}
}

// SemanticCache returns the semantic cache for direct access.
func (e *Engine) SemanticCache() *cache.SemanticCache {
	return e.semanticCache
}

// IsSemanticCacheEnabled returns whether semantic caching is enabled.
func (e *Engine) IsSemanticCacheEnabled() bool {
	if !e.enabled.Load() {
		return false
	}
	// Check cache manager first (new path)
	if e.cacheManager != nil {
		return e.cacheManager.IsSemanticCacheEnabled()
	}
	// Fallback to legacy check
	return e.semanticCache != nil
}

// WarmRouterCache populates the router cache with routes.
func (e *Engine) WarmRouterCache(warmFn WarmFunc) {
	e.routerCache.Warm(warmFn)
}

// WarmAuthCache populates the auth cache with API key hashes.
func (e *Engine) WarmAuthCache(apiKeyHashes []uint64) {
	e.authCache.Warm(apiKeyHashes)
}

// RecordFastPathRequest records that a request was processed via fast-path.
func (e *Engine) RecordFastPathRequest() {
	e.requestsProcessed.Add(1)
	e.requestsFastPath.Add(1)
	metrics.RecordFastPathRequest()
}

// RecordFallbackRequest records that a request fell back to legacy path.
func (e *Engine) RecordFallbackRequest() {
	e.requestsProcessed.Add(1)
	e.requestsFallback.Add(1)
	metrics.RecordLegacyRequest()
}

// Stats returns engine statistics.
func (e *Engine) Stats() EngineStats {
	authHits, authMisses, authHitRatio := e.authCache.Stats()
	routerHits, routerMisses, routerHitRatio := e.routerCache.Stats()
	cacheHits, cacheMisses, cacheEvicts, cacheHitRatio, cacheSize := e.exactCache.Stats()

	return EngineStats{
		Enabled:             e.enabled.Load(),
		RequestsProcessed:   e.requestsProcessed.Load(),
		RequestsFastPath:    e.requestsFastPath.Load(),
		RequestsFallback:    e.requestsFallback.Load(),
		AuthCacheHits:       authHits,
		AuthCacheMisses:     authMisses,
		AuthCacheHitRatio:   authHitRatio,
		RouterCacheHits:     routerHits,
		RouterCacheMisses:   routerMisses,
		RouterCacheHitRatio: routerHitRatio,
		ExactCacheHits:      cacheHits,
		ExactCacheMisses:    cacheMisses,
		ExactCacheEvicts:    cacheEvicts,
		ExactCacheHitRatio:  cacheHitRatio,
		ExactCacheSize:      cacheSize,
	}
}

// EngineStats contains statistics about the fast-path engine.
type EngineStats struct {
	Enabled           bool
	RequestsProcessed uint64
	RequestsFastPath  uint64
	RequestsFallback  uint64

	AuthCacheHits     uint64
	AuthCacheMisses   uint64
	AuthCacheHitRatio float64

	RouterCacheHits     uint64
	RouterCacheMisses   uint64
	RouterCacheHitRatio float64

	ExactCacheHits     uint64
	ExactCacheMisses   uint64
	ExactCacheEvicts   uint64
	ExactCacheHitRatio float64
	ExactCacheSize     int
}

// Cleanup runs maintenance tasks (cache cleanup, etc.)
func (e *Engine) Cleanup() {
	e.exactCache.Cleanup()
	e.authCache.Cleanup()
	if e.semanticCache != nil {
		e.semanticCache.Cleanup()
	}
	if e.onnxCache != nil {
		e.onnxCache.Cleanup()
	}
}

// Global engine instance
var (
	globalEngine     *Engine
	globalEngineOnce sync.Once
	globalEngineMu   sync.RWMutex
)

// InitGlobalEngine initializes the global fast-path engine.
// This should be called once at startup.
func InitGlobalEngine(cfg EngineConfig) error {
	var initErr error
	globalEngineOnce.Do(func() {
		engine, err := NewEngine(cfg)
		if err != nil {
			initErr = err
			return
		}
		globalEngineMu.Lock()
		globalEngine = engine
		globalEngineMu.Unlock()
	})
	return initErr
}

// GetGlobalEngine returns the global fast-path engine.
// Returns nil if not initialized.
func GetGlobalEngine() *Engine {
	globalEngineMu.RLock()
	defer globalEngineMu.RUnlock()
	return globalEngine
}

// SetGlobalEngine sets the global engine (for testing).
func SetGlobalEngine(e *Engine) {
	globalEngineMu.Lock()
	globalEngine = e
	globalEngineMu.Unlock()
}

// contextKey is a type for context keys used by the fast-path package.
type contextKey int

const (
	engineContextKey contextKey = iota
)

// WithEngine adds the engine to a context.
func WithEngine(ctx context.Context, engine *Engine) context.Context {
	return context.WithValue(ctx, engineContextKey, engine)
}

// EngineFromContext retrieves the engine from a context.
// Falls back to global engine if not in context.
func EngineFromContext(ctx context.Context) *Engine {
	if e, ok := ctx.Value(engineContextKey).(*Engine); ok {
		return e
	}
	return GetGlobalEngine()
}
