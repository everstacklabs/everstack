package validator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/everstacklabs/everstack/internal/modelidentity"
	"gopkg.in/yaml.v3"
)

// CatalogManifest represents the manifest.yaml structure
type CatalogManifest struct {
	Version       string             `yaml:"version"`
	GeneratedAt   string             `yaml:"generated_at"`
	SchemaVersion string             `yaml:"schema_version"`
	Providers     []ManifestProvider `yaml:"providers"`
	Stats         ManifestStats      `yaml:"stats"`
}

type CatalogChangelog struct {
	Versions []CatalogChangelogVersion `yaml:"versions"`
}

type CatalogChangelogVersion struct {
	Version string `yaml:"version"`
	Changes struct {
		NewModels []CatalogChangelogModel `yaml:"new_models"`
	} `yaml:"changes"`
}

type CatalogChangelogModel struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// ManifestProvider represents a provider entry in the manifest
type ManifestProvider struct {
	Name   string   `yaml:"name"`
	Files  []string `yaml:"files"`
	Models []string `yaml:"models"`
}

// ManifestStats represents catalog statistics
type ManifestStats struct {
	TotalProviders  int `yaml:"total_providers"`
	TotalModels     int `yaml:"total_models"`
	StaticProviders int `yaml:"static_providers"`
	MetaProviders   int `yaml:"meta_providers"`
}

// CatalogProviderConfig represents a provider.yaml file structure
type CatalogProviderConfig struct {
	Name                   string                 `yaml:"name"`
	DisplayName            string                 `yaml:"display_name"`
	BaseURL                string                 `yaml:"base_url"`
	APIVersion             string                 `yaml:"api_version"`
	ProviderType           string                 `yaml:"provider_type"`
	SupportsModelDiscovery bool                   `yaml:"supports_model_discovery"`
	DiscoveryAPIEndpoint   string                 `yaml:"discovery_api_endpoint"`
	Auth                   CatalogAuthConfig      `yaml:"auth"`
	RateLimits             CatalogRateLimits      `yaml:"rate_limits"`
	Capabilities           map[string]bool        `yaml:"capabilities"`
	ModelFamilies          map[string]interface{} `yaml:"model_families"`
	Defaults               map[string]interface{} `yaml:"defaults"`
	ErrorMapping           map[string]string      `yaml:"error_mapping"`
}

// CatalogAuthConfig represents authentication configuration
type CatalogAuthConfig struct {
	Type         string `yaml:"type"`
	HeaderName   string `yaml:"header_name"`
	HeaderFormat string `yaml:"header_format"`
	EnvVar       string `yaml:"env_var"`
}

// CatalogRateLimits represents rate limit configuration
type CatalogRateLimits struct {
	RequestsPerMinute  int `yaml:"requests_per_minute"`
	TokensPerMinute    int `yaml:"tokens_per_minute"`
	ConcurrentRequests int `yaml:"concurrent_requests"`
}

// CatalogModelConfig represents an individual model YAML file structure
type CatalogModelConfig struct {
	Name             string            `yaml:"name"`
	DisplayName      string            `yaml:"display_name"`
	Family           string            `yaml:"family"`
	Status           string            `yaml:"status"`
	ReleaseDate      string            `yaml:"release_date"`
	AddedInVersion   string            `yaml:"added_in_version"`
	Publisher        string            `yaml:"publisher"`
	CanonicalModelID string            `yaml:"canonical_model_id"`
	Cost             CatalogCost       `yaml:"cost"`
	Limits           CatalogLimits     `yaml:"limits"`
	Capabilities     []string          `yaml:"capabilities"`
	Modalities       CatalogModalities `yaml:"modalities"`
	Parameters       []ModelParameter  `yaml:"parameters"`
	Variants         []ModelVariant    `yaml:"variants"`
	StructuredOutput bool              `yaml:"structured_output"`
}

// CatalogCost represents model cost configuration.
//
// Cache rates are optional: not every provider prices cache reads separately,
// and catalogs synced before these fields existed will not carry them. A zero
// value means "unknown", and callers fall back to a provider-shaped multiplier
// rather than pricing cached tokens at zero.
type CatalogCost struct {
	InputPer1k      float64 `yaml:"input_per_1k"`
	OutputPer1k     float64 `yaml:"output_per_1k"`
	CacheReadPer1k  float64 `yaml:"cache_read_per_1k"`
	CacheWritePer1k float64 `yaml:"cache_write_per_1k"`
}

// CatalogLimits represents model limits
type CatalogLimits struct {
	MaxTokens           int `yaml:"max_tokens"`
	MaxCompletionTokens int `yaml:"max_completion_tokens"`
}

// CatalogModalities represents model modalities
type CatalogModalities struct {
	Input  []string `yaml:"input"`
	Output []string `yaml:"output"`
}

// ModelParameter describes a request parameter supported by one specific
// model. Keeping this metadata at model scope prevents provider-wide defaults
// from exposing controls that a particular model rejects.
type ModelParameter struct {
	Key               string   `yaml:"key" mapstructure:"key"`
	DisplayName       string   `yaml:"display_name" mapstructure:"display_name"`
	Type              string   `yaml:"type" mapstructure:"type"`
	Options           []string `yaml:"options,omitempty" mapstructure:"options"`
	MinValue          float64  `yaml:"min_value,omitempty" mapstructure:"min_value"`
	MaxValue          float64  `yaml:"max_value,omitempty" mapstructure:"max_value"`
	HasMinValue       bool     `yaml:"has_min_value,omitempty" mapstructure:"has_min_value"`
	HasMaxValue       bool     `yaml:"has_max_value,omitempty" mapstructure:"has_max_value"`
	RequiresStreaming bool     `yaml:"requires_streaming,omitempty" mapstructure:"requires_streaming"`
}

// ModelVariant is a named, selectable preset for a model. models.dev effort
// levels are represented as variants whose parameter map contains the
// normalized reasoning_effort value.
type ModelVariant struct {
	ID          string            `yaml:"id" mapstructure:"id"`
	DisplayName string            `yaml:"display_name" mapstructure:"display_name"`
	Description string            `yaml:"description,omitempty" mapstructure:"description"`
	Parameters  map[string]string `yaml:"parameters" mapstructure:"parameters"`
}

// LoadCatalogFromDirectory loads the model catalog from the hierarchical directory structure
// and returns the aggregated models and providers data as YAML bytes (backward compatible)
func LoadCatalogFromDirectory(catalogPath string) (modelsYAML []byte, providersYAML []byte, err error) {
	providersDir := filepath.Join(catalogPath, "providers")
	catalogVersion, latestAdditions, err := loadLatestCatalogAdditions(catalogPath)
	if err != nil {
		return nil, nil, err
	}

	// Check if the providers directory exists
	if _, err := os.Stat(providersDir); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("providers directory not found at %s", providersDir)
	}

	// Read all provider directories
	entries, err := os.ReadDir(providersDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read providers directory: %w", err)
	}

	// Aggregate models and providers
	modelsConfig := struct {
		Providers map[string]interface{} `yaml:"providers"`
	}{
		Providers: make(map[string]interface{}),
	}

	providersConfig := struct {
		Providers map[string]interface{} `yaml:"providers"`
	}{
		Providers: make(map[string]interface{}),
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		providerName := entry.Name()
		providerDir := filepath.Join(providersDir, providerName)

		// Load provider.yaml
		providerYAMLPath := filepath.Join(providerDir, "provider.yaml")
		providerConfig, err := loadProviderConfig(providerYAMLPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load provider config for %s: %w", providerName, err)
		}

		// Load all models from models/ subdirectory
		modelsDir := filepath.Join(providerDir, "models")
		models, err := loadModelsFromDirectory(modelsDir)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load models for %s: %w", providerName, err)
		}
		for index := range models {
			key := providerName + "/" + models[index].Name
			if _, added := latestAdditions[key]; added {
				models[index].AddedInVersion = catalogVersion
			}
		}

		// Convert to the old format for backward compatibility
		modelsConfig.Providers[providerName] = buildLegacyModelsProvider(providerConfig, models)
		providersConfig.Providers[providerName] = buildLegacyProvidersEntry(providerConfig)
	}

	// Marshal back to YAML bytes
	modelsYAML, err = yaml.Marshal(modelsConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal models config: %w", err)
	}

	providersYAML, err = yaml.Marshal(providersConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal providers config: %w", err)
	}

	return modelsYAML, providersYAML, nil
}

// loadProviderConfig loads a provider.yaml file
func loadProviderConfig(path string) (*CatalogProviderConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read provider config: %w", err)
	}

	var config CatalogProviderConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse provider config: %w", err)
	}

	return &config, nil
}

// loadModelsFromDirectory loads all model YAML files from a directory
func loadModelsFromDirectory(modelsDir string) ([]CatalogModelConfig, error) {
	if _, err := os.Stat(modelsDir); os.IsNotExist(err) {
		return nil, nil // No models directory is OK for meta-providers
	}

	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read models directory: %w", err)
	}

	var models []CatalogModelConfig
	seenNames := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		modelPath := filepath.Join(modelsDir, entry.Name())
		data, err := os.ReadFile(modelPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read model file %s: %w", entry.Name(), err)
		}

		var model CatalogModelConfig
		if err := yaml.Unmarshal(data, &model); err != nil {
			return nil, fmt.Errorf("failed to parse model file %s: %w", entry.Name(), err)
		}
		model.Name = strings.TrimSpace(model.Name)
		if model.Name == "" {
			return nil, fmt.Errorf("model file %s has no name", entry.Name())
		}
		if previousFile, duplicate := seenNames[model.Name]; duplicate {
			return nil, fmt.Errorf("model %q is defined by both %s and %s", model.Name, previousFile, entry.Name())
		}
		seenNames[model.Name] = entry.Name()

		models = append(models, model)
	}

	return models, nil
}

// buildLegacyModelsProvider builds the legacy models provider format from new structure
func buildLegacyModelsProvider(provider *CatalogProviderConfig, models []CatalogModelConfig) map[string]interface{} {
	legacyModels := make([]map[string]interface{}, 0, len(models))
	for _, m := range models {
		identity := modelidentity.ResolveWithOverrides(
			provider.Name,
			m.Name,
			m.Publisher,
			m.CanonicalModelID,
		)
		legacyModel := map[string]interface{}{
			"name":               m.Name,
			"display_name":       m.DisplayName,
			"publisher":          identity.Publisher,
			"canonical_model_id": identity.CanonicalModelID,
			"max_tokens":         m.Limits.MaxTokens,
			"input_cost_per_1k":  m.Cost.InputPer1k,
			"output_cost_per_1k": m.Cost.OutputPer1k,
			"capabilities":       m.Capabilities,
			"status":             m.Status,
			"max_output_tokens":  m.Limits.MaxCompletionTokens,
			"input_modalities":   m.Modalities.Input,
			"output_modalities":  m.Modalities.Output,
			"structured_output":  m.StructuredOutput,
			"parameters":         m.Parameters,
			"variants":           m.Variants,
		}
		// Emitted only when priced, so a missing rate stays distinguishable
		// from a genuine zero and consumers can pick their fallback.
		if m.Cost.CacheReadPer1k > 0 {
			legacyModel["cache_read_cost_per_1k"] = m.Cost.CacheReadPer1k
		}
		if m.Cost.CacheWritePer1k > 0 {
			legacyModel["cache_write_cost_per_1k"] = m.Cost.CacheWritePer1k
		}
		if m.ReleaseDate != "" {
			legacyModel["release_date"] = m.ReleaseDate
		}
		if m.AddedInVersion != "" {
			legacyModel["added_in_version"] = m.AddedInVersion
		}
		legacyModels = append(legacyModels, legacyModel)
	}

	return map[string]interface{}{
		"name":     provider.DisplayName,
		"base_url": provider.BaseURL,
		"models":   legacyModels,
	}
}

func loadLatestCatalogAdditions(catalogPath string) (string, map[string]struct{}, error) {
	additions := make(map[string]struct{})
	version, err := GetCatalogVersion(catalogPath)
	if err != nil {
		return "", additions, nil
	}

	data, err := os.ReadFile(filepath.Join(catalogPath, "changelog.yaml"))
	if os.IsNotExist(err) {
		return version, additions, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to read catalog changelog: %w", err)
	}

	var changelog CatalogChangelog
	if err := yaml.Unmarshal(data, &changelog); err != nil {
		return "", nil, fmt.Errorf("failed to parse catalog changelog: %w", err)
	}
	for _, entry := range changelog.Versions {
		if entry.Version != version {
			continue
		}
		for _, model := range entry.Changes.NewModels {
			additions[model.Provider+"/"+model.Model] = struct{}{}
		}
		break
	}
	return version, additions, nil
}

// buildLegacyProvidersEntry builds the legacy providers entry from new structure
func buildLegacyProvidersEntry(provider *CatalogProviderConfig) map[string]interface{} {
	entry := map[string]interface{}{
		"name":         provider.Name,
		"display_name": provider.DisplayName,
		"base_url":     provider.BaseURL,
		"api_version":  provider.APIVersion,
		"auth":         provider.Auth,
		"rate_limits":  provider.RateLimits,
		"capabilities": provider.Capabilities,
	}

	if provider.ProviderType != "" {
		entry["provider_type"] = provider.ProviderType
	}
	if provider.SupportsModelDiscovery {
		entry["supports_model_discovery"] = provider.SupportsModelDiscovery
	}
	if provider.DiscoveryAPIEndpoint != "" {
		entry["discovery_api_endpoint"] = provider.DiscoveryAPIEndpoint
	}
	if len(provider.ModelFamilies) > 0 {
		entry["model_families"] = provider.ModelFamilies
	}
	if len(provider.Defaults) > 0 {
		entry["defaults"] = provider.Defaults
	}
	if len(provider.ErrorMapping) > 0 {
		entry["error_mapping"] = provider.ErrorMapping
	}

	return entry
}

// LoadCatalogManifest loads and parses the manifest.yaml file
func LoadCatalogManifest(catalogPath string) (*CatalogManifest, error) {
	manifestPath := filepath.Join(catalogPath, "manifest.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest CatalogManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

// GetCatalogVersion returns the current catalog version from manifest
func GetCatalogVersion(catalogPath string) (string, error) {
	manifest, err := LoadCatalogManifest(catalogPath)
	if err != nil {
		// Try reading version.txt as fallback
		versionPath := filepath.Join(catalogPath, "version.txt")
		data, err := os.ReadFile(versionPath)
		if err != nil {
			return "", fmt.Errorf("failed to get catalog version: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return manifest.Version, nil
}
