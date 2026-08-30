package validator

import (
	"fmt"
	"strings"
)

// ModelsConfig represents the models configuration from defaults
type ModelsConfig struct {
	Providers map[string]ProviderConfig `mapstructure:"providers"`
}

// ProviderConfig represents a provider configuration
type ProviderConfig struct {
	Name                   string         `mapstructure:"name"`
	BaseURL                string         `mapstructure:"base_url"`
	Models                 []DefaultModel `mapstructure:"models"`
	ProviderType           string         `mapstructure:"provider_type"`            // "static" or "meta"
	SupportsModelDiscovery bool           `mapstructure:"supports_model_discovery"` // Whether provider supports model discovery
}

// DefaultModel represents a model in the defaults
type DefaultModel struct {
	Name             string  `mapstructure:"name"`
	DisplayName      string  `mapstructure:"display_name"`
	ReleaseDate      string  `mapstructure:"release_date"`
	AddedInVersion   string  `mapstructure:"added_in_version"`
	Publisher        string  `mapstructure:"publisher"`
	CanonicalModelID string  `mapstructure:"canonical_model_id"`
	MaxTokens        int     `mapstructure:"max_tokens"`
	InputCost        float64 `mapstructure:"input_cost_per_1k"`
	OutputCost       float64 `mapstructure:"output_cost_per_1k"`
	// Zero means the catalog does not price cache reads/writes for this model;
	// callers fall back to a multiplier rather than charging nothing.
	CacheReadCost    float64          `mapstructure:"cache_read_cost_per_1k"`
	CacheWriteCost   float64          `mapstructure:"cache_write_cost_per_1k"`
	Capabilities     []string         `mapstructure:"capabilities"`
	Status           string           `mapstructure:"status"`
	MaxOutputTokens  int              `mapstructure:"max_output_tokens"`
	InputModalities  []string         `mapstructure:"input_modalities"`
	OutputModalities []string         `mapstructure:"output_modalities"`
	StructuredOutput bool             `mapstructure:"structured_output"`
	Parameters       []ModelParameter `mapstructure:"parameters"`
	Variants         []ModelVariant   `mapstructure:"variants"`
}

// ParseModelsDefaults parses defaults/models.yaml bytes into a ModelsConfig.
func ParseModelsDefaults(data []byte) (*ModelsConfig, error) {
	var cfg ModelsConfig
	if err := loadYAMLIntoStruct(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// APIKeyConfig represents a single API key with weight for load balancing
type APIKeyConfig struct {
	Key    string `mapstructure:"key" json:"key"`
	Name   string `mapstructure:"name" json:"name"`
	Weight int    `mapstructure:"weight" json:"weight"`
}

// ModelConfig represents a single model configuration
type ModelConfig struct {
	Provider         string                       `mapstructure:"provider"`
	Model            []string                     `mapstructure:"model"`
	APIKey           string                       `mapstructure:"api_key"`  // Legacy: single key
	APIKeys          []APIKeyConfig               `mapstructure:"api_keys"` // New: multiple keys with weights
	BaseURL          string                       `mapstructure:"base_url"`
	MaxTokens        int                          `mapstructure:"max_tokens"`
	Temperature      float64                      `mapstructure:"temperature"`
	TopP             float64                      `mapstructure:"top_p"`
	FrequencyPenalty float64                      `mapstructure:"frequency_penalty"`
	PresencePenalty  float64                      `mapstructure:"presence_penalty"`
	Stop             []string                     `mapstructure:"stop"`
	ModelParameters  map[string]map[string]string `mapstructure:"model_parameters"`
	RateLimit        ModelRateLimitConfig         `mapstructure:"rate_limit"`
	// Default model selection within gateway.models
	Default      bool   `mapstructure:"default"`
	DefaultAlias string `mapstructure:"default_alias"`
}

// ModelRateLimitConfig represents rate limiting for a specific model
type ModelRateLimitConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	RequestsPerMinute int    `mapstructure:"requests_per_minute"`
	Burst             int    `mapstructure:"burst"`
	KeySource         string `mapstructure:"key_source"`
}

// LoadModelsConfig validates user configuration against defaults
func LoadModelsConfig(userConfig *Config, defaults *DefaultConfigs) (*ModelsConfig, error) {
	// Allow zero models configuration for flexible setup
	if userConfig.Gateway == nil || len(userConfig.Gateway.Models) == 0 {
		return &ModelsConfig{
			Providers: make(map[string]ProviderConfig),
		}, nil
	}

	// Load the embedded defaults to use as validation reference
	var defaultModelsConfig ModelsConfig
	if len(defaults.Models) > 0 {
		if err := loadYAMLIntoStruct(defaults.Models, &defaultModelsConfig); err != nil {
			return nil, fmt.Errorf("failed to load models defaults for validation: %w", err)
		}
	}

	// Create a ModelsConfig from the user's gateway models
	userModelsConfig := &ModelsConfig{
		Providers: make(map[string]ProviderConfig),
	}

	// Group user models by provider
	for _, model := range userConfig.Gateway.Models {
		provider := model.Provider
		if _, exists := userModelsConfig.Providers[provider]; !exists {
			userModelsConfig.Providers[provider] = ProviderConfig{
				Name:   provider,
				Models: []DefaultModel{},
			}
		}

		// Convert user model to DefaultModel format
		defaultModel := DefaultModel{
			Name:         strings.Join(model.Model, ","),
			DisplayName:  strings.Join(model.Model, ","),
			MaxTokens:    model.MaxTokens,
			InputCost:    0.0, // User config doesn't have cost info
			OutputCost:   0.0, // User config doesn't have cost info
			Capabilities: []string{},
			Status:       "active",
		}

		providerConfig := userModelsConfig.Providers[provider]
		providerConfig.Models = append(providerConfig.Models, defaultModel)
		userModelsConfig.Providers[provider] = providerConfig
	}

	// Validate user models against defaults if available
	if len(defaultModelsConfig.Providers) > 0 {
		for providerName, userProvider := range userModelsConfig.Providers {
			if defaultProvider, exists := defaultModelsConfig.Providers[providerName]; exists {
				// For meta-providers, skip model whitelist validation
				// These providers support dynamic model discovery
				if defaultProvider.ProviderType == "meta" || defaultProvider.SupportsModelDiscovery {
					// Skip detailed validation for meta-providers
					continue
				}

				// For static providers, validate each model in the user provider
				for _, userModel := range userProvider.Models {
					modelFound := false
					for _, defaultModel := range defaultProvider.Models {
						if defaultModel.Name == userModel.Name {
							modelFound = true
							// Validate max_tokens against default
							if userModel.MaxTokens > defaultModel.MaxTokens {
								return nil, fmt.Errorf("max_tokens %d exceeds maximum allowed %d for model %s",
									userModel.MaxTokens, defaultModel.MaxTokens, userModel.Name)
							}
							break
						}
					}
					if !modelFound {
						// For static providers, model must be in the whitelist
						return nil, fmt.Errorf("model '%s' is not in the whitelist for provider '%s'", userModel.Name, providerName)
					}
				}
			} else {
				return nil, fmt.Errorf("provider '%s' is not in the whitelist", providerName)
			}
		}
	}

	return userModelsConfig, nil
}

// validateModelAgainstDefaults validates a single model against the whitelist
func validateModelAgainstDefaults(model ModelConfig, defaultModelsConfig ModelsConfig) error {
	var errors []string

	// Check if provider exists in defaults
	providerFound := false
	var defaultProvider ProviderConfig
	for providerKey, provider := range defaultModelsConfig.Providers {
		if providerKey == model.Provider {
			providerFound = true
			defaultProvider = provider
			break
		}
	}

	if !providerFound {
		errors = append(errors, fmt.Sprintf("provider '%s' is not in the whitelist", model.Provider))
		if len(errors) > 0 {
			return fmt.Errorf("model validation errors: %s", strings.Join(errors, "; "))
		}
		return nil
	}

	// For meta-providers (OpenRouter, HuggingFace, Ollama, etc.), skip model whitelist validation
	// These providers support dynamic model discovery, so models are added via the UI
	if defaultProvider.ProviderType == "meta" || defaultProvider.SupportsModelDiscovery {
		// Skip whitelist validation for meta-providers - they can have any models
		// Just validate max_tokens is positive
		if model.MaxTokens <= 0 {
			errors = append(errors, "max_tokens must be > 0")
		}
		if len(errors) > 0 {
			return fmt.Errorf("model validation errors: %s", strings.Join(errors, "; "))
		}
		return nil
	}

	// For static providers, validate each model against the whitelist
	for _, modelName := range model.Model {
		modelFound := false
		for _, defaultModel := range defaultProvider.Models {
			if defaultModel.Name == modelName {
				modelFound = true

				// Validate max_tokens against default
				if model.MaxTokens > defaultModel.MaxTokens {
					errors = append(errors, fmt.Sprintf("max_tokens %d exceeds maximum allowed %d for model %s",
						model.MaxTokens, defaultModel.MaxTokens, modelName))
				}
				break
			}
		}

		if !modelFound {
			// For static providers, model must be in the whitelist
			errors = append(errors, fmt.Sprintf("model '%s' is not in the whitelist for provider '%s'", modelName, model.Provider))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("model validation errors: %s", strings.Join(errors, "; "))
	}

	return nil
}

// Validate validates the ModelConfig
func (m *ModelConfig) Validate() error {
	var errors []string

	if m.Provider == "" {
		errors = append(errors, "provider is required")
	}
	if len(m.Model) == 0 {
		errors = append(errors, "model is required")
	}

	// API key validation removed - not required

	// Validate multiple keys (if provided)
	if len(m.APIKeys) > 0 {
		names := make(map[string]bool)
		for i, key := range m.APIKeys {
			if key.Key == "" {
				errors = append(errors, fmt.Sprintf("api_keys[%d].key is required", i))
			}
			if key.Name == "" {
				errors = append(errors, fmt.Sprintf("api_keys[%d].name is required", i))
			}
			if names[key.Name] {
				errors = append(errors, fmt.Sprintf("duplicate key name: %s", key.Name))
			}
			names[key.Name] = true
			// Auto-fix weight if not set or invalid
			if key.Weight <= 0 {
				m.APIKeys[i].Weight = 1
			}
		}
	}

	if m.BaseURL == "" {
		errors = append(errors, "base_url is required")
	}
	if m.MaxTokens <= 0 {
		errors = append(errors, "max_tokens must be > 0")
	}
	if m.Temperature < 0 || m.Temperature > 2 {
		errors = append(errors, "temperature must be between 0 and 2")
	}
	if m.TopP < 0 || m.TopP > 1 {
		errors = append(errors, "top_p must be between 0 and 1")
	}

	// Default alias must exist if provided
	if m.DefaultAlias != "" {
		found := false
		for _, a := range m.Model {
			if strings.EqualFold(a, m.DefaultAlias) {
				found = true
				break
			}
		}
		if !found {
			errors = append(errors, "default_alias must be one of model aliases in the entry")
		}
	}

	// Validate rate limit
	if m.RateLimit.Enabled {
		if m.RateLimit.RequestsPerMinute <= 0 {
			errors = append(errors, "rate_limit.requests_per_minute must be > 0")
		}
		if m.RateLimit.Burst <= 0 {
			errors = append(errors, "rate_limit.burst must be > 0")
		}
		if m.RateLimit.KeySource == "" {
			errors = append(errors, "rate_limit.key_source is required when rate limiting is enabled")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("model validation errors: %s", strings.Join(errors, "; "))
	}

	return nil
}
