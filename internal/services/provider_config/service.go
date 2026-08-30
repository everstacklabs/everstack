package provider_config

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/services/provider_catalog"
)

// Service provides business logic for provider configuration management
type Service struct {
	catalog *provider_catalog.Service
	repo    *provider_config.Repository
}

func isMaskedKeyPlaceholder(key string) bool {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "*")
}

// ProviderStatus represents the merged view of catalog and configuration
type ProviderStatus struct {
	Catalog               *provider_catalog.ProviderCatalogEntry
	Configuration         *provider_config.Configuration
	IsConfigured          bool
	IsActive              bool
	ConfiguredModelsCount int
	AvailableModelsCount  int
	CatalogStatus         string // "available", "configured", "active", "deprecated"
	IsFromCatalog         bool   // True if synced from catalog
	LastUsedAt            string // ISO timestamp
}

// New creates a new provider configuration service
func New(catalog *provider_catalog.Service, repo *provider_config.Repository) *Service {
	return &Service{
		catalog: catalog,
		repo:    repo,
	}
}

// ListAll returns all providers with their status (catalog + config)
func (s *Service) ListAll(ctx context.Context) ([]ProviderStatus, error) {
	// Get all providers from catalog
	catalogProviders := s.catalog.GetCatalog()

	// Get all configurations from database
	configs, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list configurations: %w", err)
	}

	// Create a map of configurations by provider name
	configMap := make(map[string]*provider_config.Configuration)
	for _, config := range configs {
		configMap[strings.ToLower(config.ProviderName)] = config
	}

	// Merge catalog and configuration data
	statuses := make([]ProviderStatus, 0, len(catalogProviders))
	for providerName, catalogEntry := range catalogProviders {
		status := ProviderStatus{
			Catalog:              catalogEntry,
			AvailableModelsCount: len(catalogEntry.Models),
		}

		// Check if this provider has a configuration
		if config, exists := configMap[providerName]; exists {
			status.Configuration = config
			// A provider is configured if it has at least one enabled model
			// API keys can be in either the main config (legacy) or provider_api_keys table (multi-key)
			status.IsConfigured = len(config.EnabledModels) > 0
			status.IsActive = config.IsActive && status.IsConfigured
			status.ConfiguredModelsCount = len(config.EnabledModels)
			status.CatalogStatus = config.CatalogStatus
			status.IsFromCatalog = config.IsFromCatalog
			if config.LastUsedAt != nil {
				status.LastUsedAt = config.LastUsedAt.Format(time.RFC3339)
			}

			// If catalog status is empty, infer it from configuration
			if status.CatalogStatus == "" {
				if status.IsActive {
					status.CatalogStatus = "active"
				} else if status.IsConfigured {
					status.CatalogStatus = "configured"
				} else {
					status.CatalogStatus = "available"
				}
			}
		} else {
			// No configuration, default to available
			status.CatalogStatus = "available"
			status.IsFromCatalog = false
		}

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// GetProviderStatus returns status for a specific provider
func (s *Service) GetProviderStatus(ctx context.Context, providerName string) (*ProviderStatus, error) {
	// Get from catalog
	catalogEntry, exists := s.catalog.GetProvider(providerName)
	if !exists {
		return nil, fmt.Errorf("provider not found in catalog: %s", providerName)
	}

	status := &ProviderStatus{
		Catalog:              catalogEntry,
		AvailableModelsCount: len(catalogEntry.Models),
	}

	// Try to get configuration
	config, err := s.repo.Get(ctx, providerName)
	if err != nil {
		// If not found, that's ok - provider just isn't configured yet
		if !strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("failed to get configuration: %w", err)
		}
	} else {
		status.Configuration = config
		// A provider is configured if it has at least one enabled model
		// API keys can be in either the main config (legacy) or provider_api_keys table (multi-key)
		status.IsConfigured = len(config.EnabledModels) > 0
		status.IsActive = config.IsActive && status.IsConfigured
		status.ConfiguredModelsCount = len(config.EnabledModels)
		if config.LastUsedAt != nil {
			status.LastUsedAt = config.LastUsedAt.Format(time.RFC3339)
		}
	}

	return status, nil
}

// ConfigureProviderRequest represents a request to configure a provider
type ConfigureProviderRequest struct {
	ProviderName   string
	APIKey         string
	EnabledModels  []string
	CustomBaseURL  *string
	CustomSettings map[string]string
}

// ConfigureProvider validates and saves a provider configuration
func (s *Service) ConfigureProvider(ctx context.Context, req ConfigureProviderRequest) (*ProviderStatus, error) {
	// Validate provider exists in catalog
	catalogEntry, exists := s.catalog.GetProvider(req.ProviderName)
	if !exists {
		return nil, fmt.Errorf("provider '%s' not found in catalog", req.ProviderName)
	}

	// Validate models
	if err := s.ValidateModels(req.ProviderName, req.EnabledModels); err != nil {
		return nil, fmt.Errorf("invalid models: %w", err)
	}

	// Get existing configuration (if any)
	existingConfig, err := s.repo.Get(ctx, req.ProviderName)
	// Ignore "not found" errors - we'll create a new config
	if err != nil && !isNotFoundError(err) {
		return nil, fmt.Errorf("failed to get existing configuration: %w", err)
	}

	// Determine API key to use
	var encryptedKey string
	inputKey := strings.TrimSpace(req.APIKey)
	if inputKey != "" && !isMaskedKeyPlaceholder(inputKey) {
		// New API key provided - use it
		// TODO: Encrypt API key before storing
		// For now, we'll store it as-is (should be encrypted in production)
		encryptedKey = inputKey
	} else if existingConfig != nil {
		// No new API key provided - keep existing one
		encryptedKey = existingConfig.APIKeyEncrypted
	} else {
		// No new API key and no existing config - this is an error
		return nil, fmt.Errorf("api_key is required for initial configuration")
	}

	// Auto-set as default if this is the first/only provider
	if req.CustomSettings == nil || req.CustomSettings["default"] != "true" {
		// Check if there are any other configured providers
		allConfigs, err := s.repo.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing providers: %w", err)
		}

		// Count configured providers (excluding current one if it's an update)
		configuredCount := 0
		for _, cfg := range allConfigs {
			if cfg.ProviderName != req.ProviderName && len(cfg.EnabledModels) > 0 {
				configuredCount++
			}
		}

		// If this is the first/only provider, auto-set as default
		if configuredCount == 0 {
			if req.CustomSettings == nil {
				req.CustomSettings = make(map[string]string)
			}
			req.CustomSettings["default"] = "true"
			// Unset other defaults (shouldn't be any, but be safe)
			if err := s.unsetAllDefaultProviders(ctx); err != nil {
				return nil, fmt.Errorf("failed to unset other default providers: %w", err)
			}
		}
	}

	// If this provider is being set as default, unset all other defaults
	if req.CustomSettings != nil && req.CustomSettings["default"] == "true" {
		if err := s.unsetAllDefaultProviders(ctx); err != nil {
			return nil, fmt.Errorf("failed to unset other default providers: %w", err)
		}
	}

	// Determine API key source
	apiKeySource := "yaml"
	if inputKey != "" && !isMaskedKeyPlaceholder(inputKey) {
		// If API key is provided and it's not an env variable reference, it's from UI
		if !strings.HasPrefix(inputKey, "${") {
			apiKeySource = "ui"
		}
	} else if existingConfig != nil {
		// Keep existing source if no new API key provided
		apiKeySource = existingConfig.APIKeySource
	}

	// Create or update configuration
	config := &provider_config.Configuration{
		ProviderName:    req.ProviderName,
		APIKeyEncrypted: encryptedKey,
		APIKeySource:    apiKeySource,
		EnabledModels:   req.EnabledModels,
		CustomBaseURL:   req.CustomBaseURL,
		CustomSettings:  req.CustomSettings,
		IsActive:        true,
	}

	if err := s.repo.Upsert(ctx, config); err != nil {
		return nil, fmt.Errorf("failed to save configuration: %w", err)
	}

	// Return the updated status
	status := &ProviderStatus{
		Catalog:               catalogEntry,
		Configuration:         config,
		IsConfigured:          true,
		IsActive:              true,
		ConfiguredModelsCount: len(config.EnabledModels),
		AvailableModelsCount:  len(catalogEntry.Models),
	}

	if config.LastUsedAt != nil {
		status.LastUsedAt = config.LastUsedAt.Format(time.RFC3339)
	}

	return status, nil
}

// DeleteConfiguration removes a provider configuration
func (s *Service) ToggleProvider(ctx context.Context, providerName string, isActive bool) (*provider_config.Configuration, error) {
	// Get existing configuration
	config, err := s.repo.Get(ctx, providerName)
	if err != nil {
		return nil, err
	}

	// Update is_active field
	config.IsActive = isActive

	// Save updated configuration
	if err := s.repo.Upsert(ctx, config); err != nil {
		return nil, fmt.Errorf("failed to toggle provider: %w", err)
	}

	return config, nil
}

func (s *Service) DeleteConfiguration(ctx context.Context, providerName string) error {
	return s.repo.Delete(ctx, providerName)
}

// ListAllForOrg returns providers with their status, scoped to a tenant.
// Use this from request-handling code; the unscoped ListAll is retained
// for system paths (gateway boot, sync workers) that expect search_path
// or no scoping.
func (s *Service) ListAllForOrg(ctx context.Context, tenantID string) ([]ProviderStatus, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	catalogProviders := s.catalog.GetCatalog()
	configs, err := s.repo.ListForOrg(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list configurations: %w", err)
	}
	configMap := make(map[string]*provider_config.Configuration)
	for _, config := range configs {
		configMap[strings.ToLower(config.ProviderName)] = config
	}
	statuses := make([]ProviderStatus, 0, len(catalogProviders))
	for providerName, catalogEntry := range catalogProviders {
		status := ProviderStatus{
			Catalog:              catalogEntry,
			AvailableModelsCount: len(catalogEntry.Models),
		}
		if config, exists := configMap[providerName]; exists {
			status.Configuration = config
			status.IsConfigured = len(config.EnabledModels) > 0
			status.IsActive = config.IsActive && status.IsConfigured
			status.ConfiguredModelsCount = len(config.EnabledModels)
			status.CatalogStatus = config.CatalogStatus
			status.IsFromCatalog = config.IsFromCatalog
			if config.LastUsedAt != nil {
				status.LastUsedAt = config.LastUsedAt.Format(time.RFC3339)
			}
			if status.CatalogStatus == "" {
				if status.IsActive {
					status.CatalogStatus = "active"
				} else if status.IsConfigured {
					status.CatalogStatus = "configured"
				} else {
					status.CatalogStatus = "available"
				}
			}
		} else {
			status.CatalogStatus = "available"
			status.IsFromCatalog = false
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// ListActiveProvidersForOrg returns active providers for a tenant.
func (s *Service) ListActiveProvidersForOrg(ctx context.Context, tenantID string) ([]ProviderStatus, error) {
	all, err := s.ListAllForOrg(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderStatus, 0, len(all))
	for _, st := range all {
		if st.IsActive {
			out = append(out, st)
		}
	}
	return out, nil
}

// GetProviderStatusForOrg returns the status for one provider scoped to a
// tenant. The earlier GetProviderStatus called repo.Get, which (in
// single-schema multi-tenant deployments) returned any tenant's row by
// provider_name — that's how Tenant A's keys appeared when Tenant B
// rendered a provider page.
func (s *Service) GetProviderStatusForOrg(ctx context.Context, tenantID, providerName string) (*ProviderStatus, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	catalogEntry, exists := s.catalog.GetProvider(providerName)
	if !exists {
		return nil, fmt.Errorf("provider not found in catalog: %s", providerName)
	}
	status := &ProviderStatus{
		Catalog:              catalogEntry,
		AvailableModelsCount: len(catalogEntry.Models),
	}
	config, err := s.repo.GetForOrg(ctx, tenantID, providerName)
	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("failed to get configuration: %w", err)
		}
	} else {
		status.Configuration = config
		status.IsConfigured = len(config.EnabledModels) > 0
		status.IsActive = config.IsActive && status.IsConfigured
		status.ConfiguredModelsCount = len(config.EnabledModels)
		if config.LastUsedAt != nil {
			status.LastUsedAt = config.LastUsedAt.Format(time.RFC3339)
		}
	}
	return status, nil
}

// DeleteConfigurationForOrg deletes a tenant's provider configuration.
func (s *Service) DeleteConfigurationForOrg(ctx context.Context, tenantID, providerName string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant id is required")
	}
	return s.repo.DeleteForOrg(ctx, tenantID, providerName)
}

// ListConfigurationsForOrg returns raw configurations for a tenant.
func (s *Service) ListConfigurationsForOrg(ctx context.Context, tenantID string) ([]*provider_config.Configuration, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	return s.repo.ListForOrg(ctx, tenantID)
}

// isNotFoundError checks if an error is a "not found" error
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	// Check if error message contains "not found"
	errMsg := err.Error()
	return errMsg == "provider configuration not found" ||
		(len(errMsg) >= 36 && errMsg[:36] == "provider configuration not found: ")
}

// ValidateModels checks if all models exist for the provider
func (s *Service) ValidateModels(providerName string, models []string) error {
	if len(models) == 0 {
		return fmt.Errorf("at least one model must be enabled")
	}

	for _, model := range models {
		if err := s.catalog.ValidateModel(providerName, model); err != nil {
			return err
		}
	}

	return nil
}

// ListActiveProviders returns only providers that are configured and active
func (s *Service) ListActiveProviders(ctx context.Context) ([]ProviderStatus, error) {
	// Get active configurations
	configs, err := s.repo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list active configurations: %w", err)
	}

	statuses := make([]ProviderStatus, 0, len(configs))
	for _, config := range configs {
		// Get catalog entry
		catalogEntry, exists := s.catalog.GetProvider(config.ProviderName)
		if !exists {
			// Skip if provider not in catalog (shouldn't happen, but handle gracefully)
			continue
		}

		status := ProviderStatus{
			Catalog:       catalogEntry,
			Configuration: config,
			// A provider is configured if it has at least one enabled model
			// API keys can be in either the main config (legacy) or provider_api_keys table (multi-key)
			IsConfigured:          len(config.EnabledModels) > 0,
			IsActive:              config.IsActive,
			ConfiguredModelsCount: len(config.EnabledModels),
			AvailableModelsCount:  len(catalogEntry.Models),
		}

		if config.LastUsedAt != nil {
			status.LastUsedAt = config.LastUsedAt.Format(time.RFC3339)
		}

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// GetConfiguration returns just the configuration (without catalog data)
func (s *Service) GetConfiguration(ctx context.Context, providerName string) (*provider_config.Configuration, error) {
	return s.repo.Get(ctx, providerName)
}

// ListConfigurations returns all provider configurations
func (s *Service) ListConfigurations(ctx context.Context) ([]*provider_config.Configuration, error) {
	return s.repo.List(ctx)
}

// unsetAllDefaultProviders removes the default flag from all providers
func (s *Service) unsetAllDefaultProviders(ctx context.Context) error {
	// Get all configurations
	configs, err := s.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list configurations: %w", err)
	}

	// Unset default flag for each provider
	for _, config := range configs {
		if config.CustomSettings != nil && config.CustomSettings["default"] == "true" {
			// Remove the default flag
			delete(config.CustomSettings, "default")

			// Update the configuration
			if err := s.repo.Upsert(ctx, config); err != nil {
				return fmt.Errorf("failed to update provider %s: %w", config.ProviderName, err)
			}
		}
	}

	return nil
}
