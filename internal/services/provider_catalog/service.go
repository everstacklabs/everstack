package provider_catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/mitchellh/mapstructure"
)

// ModelMetadata represents metadata about a specific model
type ModelMetadata struct {
	Name             string
	DisplayName      string
	MaxTokens        int64
	MaxOutputTokens  int64
	Capabilities     []string
	InputModalities  []string
	OutputModalities []string
	StructuredOutput bool
	Parameters       []validator.ModelParameter
	Variants         []validator.ModelVariant
	InputCostPer1K   float64
	OutputCostPer1K  float64
	Status           string
	ReleaseDate      string
	AddedInVersion   string
}

// ModelFamily represents a family of models
type ModelFamily struct {
	Name         string
	Description  string
	Capabilities []string
	MaxTokens    int64
}

// RateLimits represents rate limiting configuration
type RateLimits struct {
	RequestsPerMinute  int32
	TokensPerMinute    int32
	ConcurrentRequests int32
}

// ProviderCapabilities represents what features a provider supports
type ProviderCapabilities struct {
	Chat            bool
	Completions     bool
	Embeddings      bool
	FunctionCalling bool
	Vision          bool
	Streaming       bool
	FineTuning      bool
	Assistants      bool
}

// ProviderCatalogEntry represents complete provider metadata from defaults
type ProviderCatalogEntry struct {
	Name                   string
	DisplayName            string
	BaseURL                string
	APIVersion             string
	Capabilities           ProviderCapabilities
	ModelFamilies          []ModelFamily
	Models                 []ModelMetadata
	RateLimits             RateLimits
	Description            string
	ProviderType           string // "static" or "meta"
	SupportsModelDiscovery bool
	DiscoveryAPIEndpoint   string
}

// Service provides access to the provider catalog loaded from defaults
type Service struct {
	mu           sync.RWMutex
	catalog      map[string]*ProviderCatalogEntry
	models       *validator.ModelsConfig
	providers    *ProviderDefaultsConfig
	catalogSync  CatalogSyncService // Optional catalog sync service
	providerRepo ProviderRepository // Optional database repository
	modelRepo    ModelRepository    // Optional model status repository
}

// CatalogSyncService interface for catalog sync integration
type CatalogSyncService interface {
	GetCachedProviders() map[string]interface{}
	GetCachedCatalog() (*validator.ModelsConfig, error)
}

// ProviderRepository interface for database operations (optional)
type ProviderRepository interface {
	ListAllWithCatalogStatus(ctx context.Context) ([]*ProviderConfiguration, error)
}

// ModelRepository interface for model status operations (optional)
type ModelRepository interface {
	ListByProvider(ctx context.Context, providerName string) ([]*ModelStatus, error)
}

// ProviderConfiguration minimal interface for database provider data
type ProviderConfiguration struct {
	ProviderName  string
	CatalogStatus string
	IsFromCatalog bool
	IsActive      bool
}

// ModelStatus minimal interface for database model data
type ModelStatus struct {
	ModelName string
	Freshness string
	Status    string
}

// ProviderDefaultsConfig represents the structure of providers.yaml
type ProviderDefaultsConfig struct {
	Providers map[string]ProviderDetails `mapstructure:"providers"`
}

// ProviderDetails represents detailed provider configuration
type ProviderDetails struct {
	Name                   string                       `mapstructure:"name"`
	DisplayName            string                       `mapstructure:"display_name"`
	BaseURL                string                       `mapstructure:"base_url"`
	APIVersion             string                       `mapstructure:"api_version"`
	RateLimits             RateLimitsConfig             `mapstructure:"rate_limits"`
	Capabilities           CapabilitiesConfig           `mapstructure:"capabilities"`
	ModelFamilies          map[string]ModelFamilyConfig `mapstructure:"model_families"`
	ProviderType           string                       `mapstructure:"provider_type"`
	SupportsModelDiscovery bool                         `mapstructure:"supports_model_discovery"`
	DiscoveryAPIEndpoint   string                       `mapstructure:"discovery_api_endpoint"`
}

// RateLimitsConfig from YAML
type RateLimitsConfig struct {
	RequestsPerMinute  int `mapstructure:"requests_per_minute"`
	TokensPerMinute    int `mapstructure:"tokens_per_minute"`
	ConcurrentRequests int `mapstructure:"concurrent_requests"`
}

// CapabilitiesConfig from YAML
type CapabilitiesConfig struct {
	Chat            bool `mapstructure:"chat"`
	Completions     bool `mapstructure:"completions"`
	Embeddings      bool `mapstructure:"embeddings"`
	FunctionCalling bool `mapstructure:"function_calling"`
	Vision          bool `mapstructure:"vision"`
	Streaming       bool `mapstructure:"streaming"`
	FineTuning      bool `mapstructure:"fine_tuning"`
	Assistants      bool `mapstructure:"assistants"`
}

// ModelFamilyConfig from YAML
type ModelFamilyConfig struct {
	Description  string   `mapstructure:"description"`
	Capabilities []string `mapstructure:"capabilities"`
	MaxTokens    int      `mapstructure:"max_tokens"`
}

// New creates a new Provider Catalog Service by loading defaults
func New() (*Service, error) {
	// Load only models and providers defaults (not the full config)
	modelsData, providersData, err := validator.LoadModelsAndProvidersDefaults()
	if err != nil {
		return nil, fmt.Errorf("failed to load default configs: %w", err)
	}

	// Parse models defaults
	modelsConfig, err := validator.ParseModelsDefaults(modelsData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse models defaults: %w", err)
	}

	// Parse providers defaults
	var providersConfig ProviderDefaultsConfig
	if len(providersData) > 0 {
		if err := validator.LoadYAMLIntoStruct(providersData, &providersConfig); err != nil {
			return nil, fmt.Errorf("failed to parse providers defaults: %w", err)
		}
	}

	s := &Service{
		catalog:   make(map[string]*ProviderCatalogEntry),
		models:    modelsConfig,
		providers: &providersConfig,
	}

	// Build the catalog by merging models and providers data
	if err := s.buildCatalog(); err != nil {
		return nil, fmt.Errorf("failed to build catalog: %w", err)
	}

	return s, nil
}

// SetCatalogSync sets the catalog sync service for dynamic provider updates
// Deprecated: Use SetRepositories for database-backed catalog
func (s *Service) SetCatalogSync(catalogSync CatalogSyncService) {
	s.catalogSync = catalogSync
	// Rebuild catalog with merged providers from catalog sync
	_ = s.buildCatalog()
}

// SetRepositories sets the database repositories for catalog data
func (s *Service) SetRepositories(providerRepo ProviderRepository, modelRepo ModelRepository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerRepo = providerRepo
	s.modelRepo = modelRepo
}

// Refresh reloads the catalog from the catalog sync service (if available)
// This should be called after the catalog sync service updates its cache
func (s *Service) Refresh() error {
	return s.buildCatalog()
}

// buildCatalog merges models and providers data into a unified catalog
func (s *Service) buildCatalog() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	nextCatalog := make(map[string]*ProviderCatalogEntry)

	// PRIORITY 2: Use merged models and providers from catalog sync if available
	modelsToUse := s.models
	providersToUse := s.providers

	if s.catalogSync != nil {
		// Get merged catalog (models.yaml equivalent)
		if mergedCatalog, err := s.catalogSync.GetCachedCatalog(); err == nil && mergedCatalog != nil {
			modelsToUse = mergedCatalog
		}

		// Get merged providers (providers.yaml equivalent)
		mergedProviders := s.catalogSync.GetCachedProviders()

		if len(mergedProviders) > 0 {
			// Check if mergedProviders has the 'providers' key (raw YAML structure)
			if providersMap, ok := mergedProviders["providers"].(map[string]interface{}); ok {
				// Convert to ProviderDefaultsConfig format
				var tmpProviders ProviderDefaultsConfig
				tmpProviders.Providers = make(map[string]ProviderDetails)

				for key, value := range providersMap {
					if valueMap, ok := value.(map[string]interface{}); ok {
						// Convert map to ProviderDetails struct
						var providerDetail ProviderDetails
						if err := mapstructure.Decode(valueMap, &providerDetail); err == nil {
							tmpProviders.Providers[key] = providerDetail
						}
					}
				}

				if len(tmpProviders.Providers) > 0 {
					providersToUse = &tmpProviders
				}
			}
		}
	}

	// Iterate through providers in MERGED models (includes catalog synced providers like OpenRouter)
	for providerKey, providerConfig := range modelsToUse.Providers {
		entry := &ProviderCatalogEntry{
			Name:        providerKey,
			DisplayName: providerConfig.Name,
			BaseURL:     providerConfig.BaseURL, // Set base_url from models.yaml if present
			Models:      make([]ModelMetadata, 0, len(providerConfig.Models)),
		}

		// Convert models
		for _, model := range providerConfig.Models {
			entry.Models = append(entry.Models, ModelMetadata{
				Name:             model.Name,
				DisplayName:      model.DisplayName,
				MaxTokens:        int64(model.MaxTokens),
				MaxOutputTokens:  int64(model.MaxOutputTokens),
				Capabilities:     model.Capabilities,
				InputModalities:  model.InputModalities,
				OutputModalities: model.OutputModalities,
				StructuredOutput: model.StructuredOutput,
				Parameters:       model.Parameters,
				Variants:         model.Variants,
				InputCostPer1K:   model.InputCost,
				OutputCostPer1K:  model.OutputCost,
				Status:           model.Status,
				ReleaseDate:      model.ReleaseDate,
				AddedInVersion:   model.AddedInVersion,
			})
		}
		sortModelsByReleaseDate(entry.Models)

		// Merge with provider details from providers.yaml if available
		if providerDetails, exists := providersToUse.Providers[providerKey]; exists {
			entry.DisplayName = providerDetails.DisplayName
			// Only override BaseURL if provided in providers.yaml
			if providerDetails.BaseURL != "" {
				entry.BaseURL = providerDetails.BaseURL
			}
			entry.APIVersion = providerDetails.APIVersion

			// Convert rate limits
			entry.RateLimits = RateLimits{
				RequestsPerMinute:  int32(providerDetails.RateLimits.RequestsPerMinute),
				TokensPerMinute:    int32(providerDetails.RateLimits.TokensPerMinute),
				ConcurrentRequests: int32(providerDetails.RateLimits.ConcurrentRequests),
			}

			// Convert capabilities
			entry.Capabilities = ProviderCapabilities{
				Chat:            providerDetails.Capabilities.Chat,
				Completions:     providerDetails.Capabilities.Completions,
				Embeddings:      providerDetails.Capabilities.Embeddings,
				FunctionCalling: providerDetails.Capabilities.FunctionCalling,
				Vision:          providerDetails.Capabilities.Vision,
				Streaming:       providerDetails.Capabilities.Streaming,
				FineTuning:      providerDetails.Capabilities.FineTuning,
				Assistants:      providerDetails.Capabilities.Assistants,
			}

			// Set meta-provider fields
			entry.ProviderType = providerDetails.ProviderType
			entry.SupportsModelDiscovery = providerDetails.SupportsModelDiscovery
			entry.DiscoveryAPIEndpoint = providerDetails.DiscoveryAPIEndpoint

			// Convert model families
			entry.ModelFamilies = make([]ModelFamily, 0, len(providerDetails.ModelFamilies))
			for familyName, familyConfig := range providerDetails.ModelFamilies {
				entry.ModelFamilies = append(entry.ModelFamilies, ModelFamily{
					Name:         familyName,
					Description:  familyConfig.Description,
					Capabilities: familyConfig.Capabilities,
					MaxTokens:    int64(familyConfig.MaxTokens),
				})
			}

			// Use entry.BaseURL which may come from models.yaml or providers.yaml
			if entry.BaseURL != "" {
				entry.Description = fmt.Sprintf("%s - %s", providerDetails.DisplayName, entry.BaseURL)
			}
		} else if entry.BaseURL != "" {
			// No provider details found, but we have base_url from models.yaml
			entry.Description = fmt.Sprintf("%s - %s", entry.DisplayName, entry.BaseURL)
		}

		nextCatalog[strings.ToLower(providerKey)] = entry
	}
	s.catalog = nextCatalog

	return nil
}

// sortModelsByReleaseDate keeps the provider picker predictable: dated models
// are ordered newest-first, while legacy entries without a release date retain
// a deterministic alphabetical order at the end.
func sortModelsByReleaseDate(models []ModelMetadata) {
	sort.SliceStable(models, func(i, j int) bool {
		left := models[i]
		right := models[j]

		switch {
		case left.ReleaseDate != "" && right.ReleaseDate != "":
			if left.ReleaseDate != right.ReleaseDate {
				return left.ReleaseDate > right.ReleaseDate
			}
		case left.ReleaseDate != "":
			return true
		case right.ReleaseDate != "":
			return false
		}

		leftLabel := strings.ToLower(left.DisplayName)
		rightLabel := strings.ToLower(right.DisplayName)
		if leftLabel != rightLabel {
			return leftLabel < rightLabel
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
}

// GetCatalog returns all providers in the catalog
func (s *Service) GetCatalog() map[string]*ProviderCatalogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[string]*ProviderCatalogEntry, len(s.catalog))
	for k, v := range s.catalog {
		result[k] = v
	}
	return result
}

// GetProvider returns a specific provider from the catalog
func (s *Service) GetProvider(name string) (*ProviderCatalogEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.catalog[strings.ToLower(name)]
	return entry, exists
}

// ValidateModel checks if a model exists for a given provider
func (s *Service) ValidateModel(provider, model string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.catalog[strings.ToLower(provider)]
	if !exists {
		return fmt.Errorf("provider '%s' not found in catalog", provider)
	}

	// Check if model exists
	for _, m := range entry.Models {
		if strings.EqualFold(m.Name, model) {
			return nil
		}
	}

	return fmt.Errorf("model '%s' not found for provider '%s'", model, provider)
}

// ListProviders returns a list of all provider names
func (s *Service) ListProviders() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	providers := make([]string, 0, len(s.catalog))
	for name := range s.catalog {
		providers = append(providers, name)
	}
	return providers
}

// GetModelsForProvider returns all models for a specific provider
func (s *Service) GetModelsForProvider(provider string) ([]ModelMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.catalog[strings.ToLower(provider)]
	if !exists {
		return nil, fmt.Errorf("provider '%s' not found in catalog", provider)
	}

	return entry.Models, nil
}

// Helper function to get keys from provider map
func getProviderKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
