package catalog

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Cache provides thread-safe in-memory caching for the model catalog
type Cache struct {
	// models: provider -> model -> definition
	models map[string]map[string]*ModelDefinition
	// providers: provider -> definition
	providers map[string]*ProviderDefinition
	// version of the catalog
	version string
	// lastUpdated timestamp
	lastUpdated time.Time
	// mutex for concurrent access
	mu sync.RWMutex
}

// NewCache creates a new catalog cache
func NewCache() *Cache {
	return &Cache{
		models:      make(map[string]map[string]*ModelDefinition),
		providers:   make(map[string]*ProviderDefinition),
		lastUpdated: time.Now(),
	}
}

// Load parses and caches the models and providers YAML
func (c *Cache) Load(modelsYAML, providersYAML []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Parse models.yaml
	var modelsConfig ModelsYAML
	if err := yaml.Unmarshal(modelsYAML, &modelsConfig); err != nil {
		return fmt.Errorf("failed to parse models.yaml: %w", err)
	}

	// Parse providers.yaml
	var providersConfig ProvidersYAML
	if err := yaml.Unmarshal(providersYAML, &providersConfig); err != nil {
		return fmt.Errorf("failed to parse providers.yaml: %w", err)
	}

	// Build model index
	newModels := make(map[string]map[string]*ModelDefinition)
	for providerName, providerModels := range modelsConfig.Providers {
		providerKey := strings.ToLower(providerName)
		newModels[providerKey] = make(map[string]*ModelDefinition)

		for i := range providerModels.Models {
			model := &providerModels.Models[i]
			modelKey := strings.ToLower(model.Name)
			newModels[providerKey][modelKey] = model
		}
	}

	// Build provider index
	newProviders := make(map[string]*ProviderDefinition)
	for providerName, providerDef := range providersConfig.Providers {
		providerKey := strings.ToLower(providerName)
		def := providerDef // Create copy
		newProviders[providerKey] = &def

		// Attach models from models.yaml if available
		if modelMap, ok := newModels[providerKey]; ok {
			def.Models = make([]ModelDefinition, 0, len(modelMap))
			for _, model := range modelMap {
				def.Models = append(def.Models, *model)
			}
			newProviders[providerKey] = &def
		}
	}

	// Atomic swap
	c.models = newModels
	c.providers = newProviders
	c.lastUpdated = time.Now()

	return nil
}

// GetModel returns a model definition (O(1) lookup)
func (c *Cache) GetModel(provider, model string) (*ModelDefinition, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	providerKey := strings.ToLower(provider)
	modelKey := strings.ToLower(model)

	if modelMap, ok := c.models[providerKey]; ok {
		if modelDef, ok := modelMap[modelKey]; ok {
			return modelDef, true
		}
	}
	return nil, false
}

// GetProvider returns a provider definition (O(1) lookup)
func (c *Cache) GetProvider(provider string) (*ProviderDefinition, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	providerKey := strings.ToLower(provider)
	if providerDef, ok := c.providers[providerKey]; ok {
		return providerDef, true
	}
	return nil, false
}

// GetAllModels returns all models for a provider
func (c *Cache) GetAllModels(provider string) ([]*ModelDefinition, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	providerKey := strings.ToLower(provider)
	if modelMap, ok := c.models[providerKey]; ok {
		models := make([]*ModelDefinition, 0, len(modelMap))
		for _, model := range modelMap {
			models = append(models, model)
		}
		return models, true
	}
	return nil, false
}

// GetAllProviders returns all providers
func (c *Cache) GetAllProviders() []*ProviderDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()

	providers := make([]*ProviderDefinition, 0, len(c.providers))
	for _, provider := range c.providers {
		providers = append(providers, provider)
	}
	return providers
}

// GetVersion returns the catalog version
func (c *Cache) GetVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// GetLastUpdated returns when the catalog was last updated
func (c *Cache) GetLastUpdated() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastUpdated
}

// SetVersion sets the catalog version
func (c *Cache) SetVersion(version string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version = version
}

// Refresh hot-reloads the cache with new catalog data
func (c *Cache) Refresh(modelsYAML, providersYAML []byte) error {
	// Load validates and swaps atomically
	return c.Load(modelsYAML, providersYAML)
}

// GetModelByPrefix tries to match a versioned model name to a catalog entry.
// e.g., "gpt-4o-2024-08-06" matches "gpt-4o" if the remaining suffix starts with "-".
func (c *Cache) GetModelByPrefix(provider, model string) (*ModelDefinition, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	providerKey := strings.ToLower(provider)
	modelLower := strings.ToLower(model)

	modelMap, ok := c.models[providerKey]
	if !ok {
		return nil, false
	}

	var bestMatch *ModelDefinition
	bestLen := 0

	for name, def := range modelMap {
		if len(modelLower) > len(name) && strings.HasPrefix(modelLower, name) {
			remaining := modelLower[len(name):]
			if len(remaining) > 0 && remaining[0] == '-' && len(name) > bestLen {
				bestMatch = def
				bestLen = len(name)
			}
		}
	}

	if bestMatch != nil {
		return bestMatch, true
	}
	return nil, false
}

// GetModelWhitelist returns a map of provider -> allowed models (for bootstrap validation)
func (c *Cache) GetModelWhitelist() map[string]map[string]struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	whitelist := make(map[string]map[string]struct{})
	for provider, modelMap := range c.models {
		whitelist[provider] = make(map[string]struct{})
		for modelName := range modelMap {
			whitelist[provider][modelName] = struct{}{}
		}
	}
	return whitelist
}
