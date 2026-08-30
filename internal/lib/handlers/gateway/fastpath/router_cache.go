// Package fastpath provides high-performance request processing for the gateway.
package fastpath

import (
	"strings"
	"sync"
	"sync/atomic"
)

// ProviderInfo holds pre-resolved provider information for fast routing.
type ProviderInfo struct {
	Name        string
	ModelName   string // Canonical model name for this provider
	IsCustom    bool
	ProviderRef interface{} // Reference to the actual provider (avoids import cycle)
}

// RouterCache provides O(1) model-to-provider lookups using pre-computed maps.
//
// Performance targets:
//   - Lookup: <10µs (single sync.Map load)
//   - Warm: O(n) where n is number of models
type RouterCache struct {
	// modelToProvider maps lowercase model name -> *ProviderInfo
	modelToProvider sync.Map

	// providerByName maps provider name -> *ProviderInfo
	providerByName sync.Map

	// aliasToModel maps model aliases -> canonical model name
	aliasToModel sync.Map

	// stats for observability
	hits   atomic.Uint64
	misses atomic.Uint64

	// warmed indicates if the cache has been populated
	warmed atomic.Bool
}

// NewRouterCache creates a new pre-computed router cache.
func NewRouterCache() *RouterCache {
	return &RouterCache{}
}

// Resolve looks up the provider for a given model name.
// Returns the provider info and true if found, nil and false otherwise.
func (c *RouterCache) Resolve(model string) (*ProviderInfo, bool) {
	if model == "" {
		c.misses.Add(1)
		return nil, false
	}

	// Normalize model name to lowercase for lookup
	key := strings.ToLower(model)

	// Check alias mapping first
	if canonical, ok := c.aliasToModel.Load(key); ok {
		key = canonical.(string)
	}

	// O(1) lookup
	if info, ok := c.modelToProvider.Load(key); ok {
		c.hits.Add(1)
		return info.(*ProviderInfo), true
	}

	c.misses.Add(1)
	return nil, false
}

// ResolveProvider looks up a provider by name.
func (c *RouterCache) ResolveProvider(name string) (*ProviderInfo, bool) {
	if info, ok := c.providerByName.Load(strings.ToLower(name)); ok {
		return info.(*ProviderInfo), true
	}
	return nil, false
}

// Register adds or updates a model -> provider mapping.
func (c *RouterCache) Register(model string, info *ProviderInfo) {
	if model == "" || info == nil {
		return
	}

	key := strings.ToLower(model)
	c.modelToProvider.Store(key, info)
	c.providerByName.Store(strings.ToLower(info.Name), info)
}

// RegisterAlias maps an alias to a canonical model name.
func (c *RouterCache) RegisterAlias(alias, canonical string) {
	if alias == "" || canonical == "" {
		return
	}
	c.aliasToModel.Store(strings.ToLower(alias), strings.ToLower(canonical))
}

// RegisterBatch registers multiple model -> provider mappings at once.
func (c *RouterCache) RegisterBatch(mappings map[string]*ProviderInfo) {
	for model, info := range mappings {
		c.Register(model, info)
	}
}

// Unregister removes a model from the cache.
func (c *RouterCache) Unregister(model string) {
	if model == "" {
		return
	}
	c.modelToProvider.Delete(strings.ToLower(model))
}

// Clear removes all entries from the cache.
func (c *RouterCache) Clear() {
	c.modelToProvider = sync.Map{}
	c.providerByName = sync.Map{}
	c.aliasToModel = sync.Map{}
	c.warmed.Store(false)
}

// MarkWarmed indicates that the cache has been fully populated.
func (c *RouterCache) MarkWarmed() {
	c.warmed.Store(true)
}

// IsWarmed returns true if the cache has been populated.
func (c *RouterCache) IsWarmed() bool {
	return c.warmed.Load()
}

// Stats returns cache statistics for observability.
func (c *RouterCache) Stats() (hits, misses uint64, hitRatio float64) {
	hits = c.hits.Load()
	misses = c.misses.Load()
	total := hits + misses
	if total > 0 {
		hitRatio = float64(hits) / float64(total)
	}
	return
}

// ResetStats resets the hit/miss counters.
func (c *RouterCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
}

// Size returns the approximate number of cached models.
func (c *RouterCache) Size() int {
	count := 0
	c.modelToProvider.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// AllModels returns all registered model names.
func (c *RouterCache) AllModels() []string {
	var models []string
	c.modelToProvider.Range(func(key, _ interface{}) bool {
		models = append(models, key.(string))
		return true
	})
	return models
}

// AllProviders returns all registered provider names.
func (c *RouterCache) AllProviders() []string {
	var providers []string
	c.providerByName.Range(func(key, _ interface{}) bool {
		providers = append(providers, key.(string))
		return true
	})
	return providers
}

// WarmFromRouter populates the cache from a router instance.
// The routerAccessor should be a function that returns model->provider mappings.
type RouteInfo struct {
	ProviderName string
	ModelName    string
	IsCustom     bool
	Provider     interface{}
}

// WarmFunc is a function type for providing routes to warm the cache.
type WarmFunc func() map[string]RouteInfo

// Warm populates the cache using the provided warm function.
func (c *RouterCache) Warm(warmFn WarmFunc) {
	routes := warmFn()
	for model, route := range routes {
		c.Register(model, &ProviderInfo{
			Name:        route.ProviderName,
			ModelName:   route.ModelName,
			IsCustom:    route.IsCustom,
			ProviderRef: route.Provider,
		})
	}
	c.MarkWarmed()
}

