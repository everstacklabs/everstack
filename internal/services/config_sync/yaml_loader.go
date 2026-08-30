package config_sync

import (
	"encoding/json"
	"fmt"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
)

// LoadFromGatewayConfig converts gateway config models to provider configurations
func LoadFromGatewayConfig(cfg *validator.GatewayConfig) ([]*provider_config.Configuration, error) {
	if cfg == nil || cfg.Models == nil {
		return []*provider_config.Configuration{}, nil
	}

	// Group models by provider (existing logic from LoadFromYAML)
	providerModels := make(map[string]*provider_config.Configuration)

	for _, modelConfig := range cfg.Models {
		provider := modelConfig.Provider
		config, exists := providerModels[provider]
		if !exists {
			config = &provider_config.Configuration{
				ProviderName:    provider,
				APIKeyEncrypted: modelConfig.APIKey,
				APIKeySource:    "yaml",
				EnabledModels:   []string{},
				CustomSettings:  make(map[string]string),
				IsActive:        true,
			}
			providerModels[provider] = config
		}

		// Set API key if not already set
		if config.APIKeyEncrypted == "" && modelConfig.APIKey != "" {
			config.APIKeyEncrypted = modelConfig.APIKey
		}

		// Set base URL
		if modelConfig.BaseURL != "" {
			config.CustomBaseURL = &modelConfig.BaseURL
		}

		// Add models
		config.EnabledModels = append(config.EnabledModels, modelConfig.Model...)

		// Store custom settings including default flags
		if modelConfig.MaxTokens > 0 {
			config.CustomSettings["max_tokens"] = fmt.Sprintf("%d", modelConfig.MaxTokens)
		}
		if modelConfig.Default {
			config.CustomSettings["default"] = "true"
		}
		if modelConfig.DefaultAlias != "" {
			config.CustomSettings["default_alias"] = modelConfig.DefaultAlias
		}
		if err := mergeModelParametersSetting(
			config.CustomSettings,
			modelConfig.ModelParameters,
		); err != nil {
			return nil, fmt.Errorf(
				"failed to merge model parameters for provider %s: %w",
				provider,
				err,
			)
		}
	}

	// Convert map to slice
	result := make([]*provider_config.Configuration, 0, len(providerModels))
	for _, config := range providerModels {
		result = append(result, config)
	}

	return result, nil
}

// LoadFromYAML reads provider configurations from a gateway YAML file
func LoadFromYAML(configPath string) ([]*provider_config.Configuration, error) {
	// Load the config file
	cfg, err := validator.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Gateway == nil || len(cfg.Gateway.Models) == 0 {
		// No models configured, return empty slice
		return []*provider_config.Configuration{}, nil
	}

	// Group models by provider
	providerModels := make(map[string]*provider_config.Configuration)

	for _, modelConfig := range cfg.Gateway.Models {
		provider := modelConfig.Provider

		// Get or create configuration for this provider
		config, exists := providerModels[provider]
		if !exists {
			// Store API keys as plaintext (no encryption for now)
			config = &provider_config.Configuration{
				ProviderName:    provider,
				APIKeyEncrypted: modelConfig.APIKey, // Actually plaintext, field name is legacy
				APIKeySource:    "yaml",
				EnabledModels:   []string{},
				CustomSettings:  make(map[string]string),
				IsActive:        true,
			}
			providerModels[provider] = config
		}

		// Set API key if not already set
		if config.APIKeyEncrypted == "" && modelConfig.APIKey != "" {
			config.APIKeyEncrypted = modelConfig.APIKey
		}

		// Set custom base URL if provided
		if modelConfig.BaseURL != "" {
			config.CustomBaseURL = &modelConfig.BaseURL
		}

		// Add models from this config entry
		config.EnabledModels = append(config.EnabledModels, modelConfig.Model...)

		// Store custom settings
		if modelConfig.MaxTokens > 0 {
			config.CustomSettings["max_tokens"] = fmt.Sprintf("%d", modelConfig.MaxTokens)
		}
		if modelConfig.Default {
			config.CustomSettings["default"] = "true"
		}
		if modelConfig.DefaultAlias != "" {
			config.CustomSettings["default_alias"] = modelConfig.DefaultAlias
		}
		if modelConfig.Temperature > 0 {
			config.CustomSettings["temperature"] = fmt.Sprintf("%f", modelConfig.Temperature)
		}
		if modelConfig.TopP > 0 {
			config.CustomSettings["top_p"] = fmt.Sprintf("%f", modelConfig.TopP)
		}
		if err := mergeModelParametersSetting(
			config.CustomSettings,
			modelConfig.ModelParameters,
		); err != nil {
			return nil, fmt.Errorf(
				"failed to merge model parameters for provider %s: %w",
				provider,
				err,
			)
		}
	}

	// Convert map to slice
	configs := make([]*provider_config.Configuration, 0, len(providerModels))
	for _, config := range providerModels {
		configs = append(configs, config)
	}

	return configs, nil
}

func mergeModelParametersSetting(
	settings map[string]string,
	incoming map[string]map[string]string,
) error {
	if len(incoming) == 0 {
		return nil
	}

	merged := make(map[string]map[string]string)
	if raw := settings[modelParametersSetting]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &merged); err != nil {
			return fmt.Errorf("decode existing %s: %w", modelParametersSetting, err)
		}
	}
	for modelName, parameters := range incoming {
		values := make(map[string]string, len(parameters))
		for key, value := range parameters {
			values[key] = value
		}
		merged[modelName] = values
	}

	encoded, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("encode %s: %w", modelParametersSetting, err)
	}
	settings[modelParametersSetting] = string(encoded)
	return nil
}

// GetYAMLModifiedTime returns the last modification time of the YAML file
func GetYAMLModifiedTime(configPath string) (int64, error) {
	// This would check the file's mtime
	// For now, return 0 to indicate we should implement this
	return 0, fmt.Errorf("not implemented yet - need to check file mtime")
}
