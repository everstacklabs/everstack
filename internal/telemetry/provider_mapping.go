package telemetry

import (
	"strings"
	"sync"

	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// ProviderMapper efficiently maps model names to provider names using the catalog
type ProviderMapper struct {
	catalogSvc *catalog.Service
	// Fast lookup cache: model_name -> provider_name
	modelToProvider map[string]string
	mu              sync.RWMutex
}

// NewProviderMapper creates a new provider mapper with catalog integration
func NewProviderMapper(catalogSvc *catalog.Service) *ProviderMapper {
	pm := &ProviderMapper{
		catalogSvc:      catalogSvc,
		modelToProvider: make(map[string]string),
	}

	// Build initial cache from catalog
	pm.rebuildCache()

	return pm
}

// GetProviderForModel returns the provider name for a given model
// Returns "unknown" if the model is not found in the catalog
func (pm *ProviderMapper) GetProviderForModel(modelName string) string {
	if modelName == "" {
		return "unknown"
	}

	// Try cache first (O(1) lookup)
	pm.mu.RLock()
	if provider, ok := pm.modelToProvider[strings.ToLower(modelName)]; ok {
		pm.mu.RUnlock()
		return provider
	}
	pm.mu.RUnlock()

	// Cache miss - try catalog directly (in case of catalog update)
	if pm.catalogSvc != nil {
		cache := pm.catalogSvc.GetModels()
		providers := cache.GetAllProviders()

		for _, provider := range providers {
			if models, ok := cache.GetAllModels(provider.Name); ok {
				for _, model := range models {
					if strings.EqualFold(model.Name, modelName) {
						// Update cache for next time
						pm.updateCache(modelName, provider.Name)
						return provider.Name
					}
				}
			}
		}
	}

	// Fallback: Extract from model name prefix (for models not in catalog)
	return pm.extractProviderFromPrefix(modelName)
}

// rebuildCache rebuilds the entire model->provider cache from the catalog
func (pm *ProviderMapper) rebuildCache() {
	if pm.catalogSvc == nil {
		return
	}

	cache := pm.catalogSvc.GetModels()
	providers := cache.GetAllProviders()

	newCache := make(map[string]string)

	for _, provider := range providers {
		if models, ok := cache.GetAllModels(provider.Name); ok {
			for _, model := range models {
				modelKey := strings.ToLower(model.Name)
				newCache[modelKey] = provider.Name
			}
		}
	}

	pm.mu.Lock()
	pm.modelToProvider = newCache
	pm.mu.Unlock()
}

// updateCache adds a single model->provider mapping to the cache
func (pm *ProviderMapper) updateCache(modelName, providerName string) {
	pm.mu.Lock()
	pm.modelToProvider[strings.ToLower(modelName)] = providerName
	pm.mu.Unlock()
}

// RefreshFromCatalog refreshes the cache when the catalog is updated
// Call this after catalog sync completes
func (pm *ProviderMapper) RefreshFromCatalog() {
	pm.rebuildCache()
}

// extractProviderFromPrefix is a fallback heuristic for models not in catalog
// This handles edge cases like custom models or new models before catalog update
func (pm *ProviderMapper) extractProviderFromPrefix(modelName string) string {
	lower := strings.ToLower(modelName)

	// Common prefixes (fallback only - catalog is source of truth)
	switch {
	case strings.HasPrefix(lower, "gpt-"), strings.HasPrefix(lower, "o1-"), strings.HasPrefix(lower, "text-"):
		return "openai"
	case strings.HasPrefix(lower, "claude-"):
		return "anthropic"
	case strings.HasPrefix(lower, "gemini-"), strings.HasPrefix(lower, "palm-"):
		return "google"
	case strings.HasPrefix(lower, "command-"), strings.HasPrefix(lower, "embed-"):
		return "cohere"
	case strings.HasPrefix(lower, "mistral-"), strings.HasPrefix(lower, "mixtral-"):
		return "mistral"
	case strings.HasPrefix(lower, "llama-"), strings.HasPrefix(lower, "codellama-"):
		return "meta"
	case strings.HasPrefix(lower, "deepseek-"):
		return "deepseek"
	case strings.Contains(lower, "huggingface"), strings.Contains(lower, "hf-"):
		return "huggingface"
	default:
		return "unknown"
	}
}

// Global provider mapper (initialized when catalog is loaded)
var globalProviderMapper *ProviderMapper
var mapperMu sync.RWMutex

// SetGlobalProviderMapper sets the global provider mapper instance
func SetGlobalProviderMapper(pm *ProviderMapper) {
	mapperMu.Lock()
	globalProviderMapper = pm
	mapperMu.Unlock()
}

// GetGlobalProviderMapper returns the global provider mapper
func GetGlobalProviderMapper() *ProviderMapper {
	mapperMu.RLock()
	defer mapperMu.RUnlock()
	return globalProviderMapper
}

// GetProviderForModel is a convenience function using the global mapper.
// Returns "" when the provider cannot be determined (mapper not initialized or
// model not found). Empty rather than an "unknown" sentinel: this value is
// stamped as the "provider" span attribute, and a non-empty sentinel would win
// the trace provider aggregation (anyIf(provider != '')) and render as
// "Unknown" in the UI instead of falling back to the real provider.
func GetProviderForModel(modelName string) string {
	mapper := GetGlobalProviderMapper()
	if mapper == nil {
		return ""
	}
	if p := mapper.GetProviderForModel(modelName); p != "unknown" {
		return p
	}
	return ""
}
