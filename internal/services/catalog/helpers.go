package catalog

// Helper methods for database-first catalog loading

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
	"gopkg.in/yaml.v3"
)

// loadFromDatabase loads catalog from the database
func (s *Service) loadFromDatabase(ctx context.Context) error {
	if s.repo == nil {
		return fmt.Errorf("repository not initialized")
	}

	// Get latest metadata
	metadata, err := s.repo.GetLatestMetadata(ctx)
	if err != nil {
		return fmt.Errorf("failed to get catalog metadata: %w", err)
	}

	if metadata == nil {
		return fmt.Errorf("no catalog found in database")
	}

	// Load providers
	providers, err := s.repo.GetAllProviders(ctx)
	if err != nil {
		return fmt.Errorf("failed to load providers from database: %w", err)
	}

	// Load models
	models, err := s.repo.GetAllModels(ctx)
	if err != nil {
		return fmt.Errorf("failed to load models from database: %w", err)
	}

	// Convert to YAML format and load into cache
	// This is a simplified approach - we could optimize by loading directly into cache
	modelsYAML, err := convertModelsToYAML(models)
	if err != nil {
		return fmt.Errorf("failed to convert models to YAML: %w", err)
	}

	providersYAML, err := convertProvidersToYAML(providers)
	if err != nil {
		return fmt.Errorf("failed to convert providers to YAML: %w", err)
	}
	if err := catalogdistribution.ValidateCatalogDocuments(modelsYAML, providersYAML); err != nil {
		return fmt.Errorf("database catalog is not a complete last-known-good release: %w", err)
	}

	if err := s.cache.Load(modelsYAML, providersYAML); err != nil {
		return fmt.Errorf("failed to load catalog into cache: %w", err)
	}

	s.cache.SetVersion(metadata.Version)
	return nil
}

// syncToDatabase persists catalog data to the database
func (s *Service) syncToDatabase(ctx context.Context, modelsYAML, providersYAML []byte, version string, source CatalogSource) error {
	if s.repo == nil {
		return nil // No repository, skip sync
	}

	// Parse YAML to extract data
	var modelsConfig ModelsYAML
	if err := yaml.Unmarshal(modelsYAML, &modelsConfig); err != nil {
		return fmt.Errorf("failed to parse models.yaml: %w", err)
	}

	var providersConfig ProvidersYAML
	if err := yaml.Unmarshal(providersYAML, &providersConfig); err != nil {
		return fmt.Errorf("failed to parse providers.yaml: %w", err)
	}

	// Convert to repository types
	catalogProviders := make([]*CatalogProvider, 0)
	for providerName, providerDef := range providersConfig.Providers {
		catalogProviders = append(catalogProviders, &CatalogProvider{
			Name:        providerName,
			DisplayName: providerDef.DisplayName,
			BaseURL:     providerDef.BaseURL,
			APIVersion:  providerDef.APIVersion,
			Config: map[string]interface{}{
				"auth":        providerDef.Auth,
				"rate_limits": providerDef.RateLimits,
			},
		})
	}

	catalogModels := make([]*CatalogModel, 0)
	for providerName, providerModels := range modelsConfig.Providers {
		for _, model := range providerModels.Models {
			catalogModels = append(catalogModels, &CatalogModel{
				ProviderName:        providerName,
				ModelName:           model.Name,
				DisplayName:         model.DisplayName,
				MaxTokens:           model.MaxTokens,
				InputCostPer1k:      model.InputCostPer1k,
				OutputCostPer1k:     model.OutputCostPer1k,
				CacheReadCostPer1k:  model.CacheReadCostPer1k,
				CacheWriteCostPer1k: model.CacheWriteCostPer1k,
				Capabilities:        model.Capabilities,
				Status:              model.Status,
			})
		}
	}

	// Sync to database
	if version == "" {
		version = "unknown"
	}

	return s.repo.SyncCatalog(ctx, catalogProviders, catalogModels, version, source)
}

// loadFallback loads a minimal hardcoded catalog as last resort
func (s *Service) loadFallback() error {
	// Minimal fallback catalog with just OpenAI and Anthropic
	minimalModels := `
providers:
  openai:
    name: "OpenAI"
    base_url: "https://api.openai.com/v1"
    models:
      - name: "gpt-4o"
        display_name: "GPT-4o"
        max_tokens: 128000
        input_cost_per_1k: 0.005
        output_cost_per_1k: 0.015
        capabilities: ["chat", "function_calling", "vision"]
        status: "stable"
  anthropic:
    name: "Anthropic"
    base_url: "https://api.anthropic.com/v1"
    models:
      - name: "claude-3-5-sonnet-20241022"
        display_name: "Claude 3.5 Sonnet"
        max_tokens: 200000
        input_cost_per_1k: 0.003
        output_cost_per_1k: 0.015
        capabilities: ["chat", "function_calling", "vision"]
        status: "stable"
`

	minimalProviders := `
providers:
  openai:
    name: "OpenAI"
    display_name: "OpenAI"
    base_url: "https://api.openai.com/v1"
    api_version: "2024-01-01"
  anthropic:
    name: "Anthropic"
    display_name: "Anthropic"
    base_url: "https://api.anthropic.com/v1"
    api_version: "2023-06-01"
`

	if err := s.cache.Load([]byte(minimalModels), []byte(minimalProviders)); err != nil {
		return fmt.Errorf("failed to load fallback catalog: %w", err)
	}

	s.cache.SetVersion("fallback")
	s.setSource(SourceFallback)
	return nil
}

// convertModelsToYAML converts database models to YAML format
func convertModelsToYAML(models []*CatalogModel) ([]byte, error) {
	// Group models by provider
	providerModels := make(map[string][]ModelDefinition)
	for _, model := range models {
		providerModels[model.ProviderName] = append(providerModels[model.ProviderName], ModelDefinition{
			Name:                model.ModelName,
			DisplayName:         model.DisplayName,
			MaxTokens:           model.MaxTokens,
			InputCostPer1k:      model.InputCostPer1k,
			OutputCostPer1k:     model.OutputCostPer1k,
			CacheReadCostPer1k:  model.CacheReadCostPer1k,
			CacheWriteCostPer1k: model.CacheWriteCostPer1k,
			Capabilities:        model.Capabilities,
			Status:              model.Status,
		})
	}

	// Create ModelsYAML structure
	modelsYAML := ModelsYAML{
		Providers: make(map[string]ProviderModels),
	}

	for providerName, models := range providerModels {
		modelsYAML.Providers[providerName] = ProviderModels{
			Name:   providerName,
			Models: models,
		}
	}

	return yaml.Marshal(modelsYAML)
}

// convertProvidersToYAML converts database providers to YAML format
func convertProvidersToYAML(providers []*CatalogProvider) ([]byte, error) {
	providersYAML := ProvidersYAML{
		Providers: make(map[string]ProviderDefinition),
	}

	for _, provider := range providers {
		providersYAML.Providers[provider.Name] = ProviderDefinition{
			Name:        provider.Name,
			DisplayName: provider.DisplayName,
			BaseURL:     provider.BaseURL,
			APIVersion:  provider.APIVersion,
		}
	}

	return yaml.Marshal(providersYAML)
}
