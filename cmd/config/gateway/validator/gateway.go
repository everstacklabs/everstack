package validator

import (
	"fmt"
	"strings"
)

// ProvidersMetadata represents provider metadata from providers.yaml
type ProvidersMetadata struct {
	Providers map[string]ProviderMetadata `mapstructure:"providers"`
}

// ProviderMetadata represents metadata for a single provider from providers.yaml
type ProviderMetadata struct {
	Name                   string `mapstructure:"name"`
	DisplayName            string `mapstructure:"display_name"`
	BaseURL                string `mapstructure:"base_url"`
	ProviderType           string `mapstructure:"provider_type"`
	SupportsModelDiscovery bool   `mapstructure:"supports_model_discovery"`
}

// GatewayConfig represents the gateway configuration
type GatewayConfig struct {
	Models         []ModelConfig        `mapstructure:"models"`
	RateLimit      RateLimitConfig      `mapstructure:"rate_limit"`
	LoadBalancer   LoadBalancerConfig   `mapstructure:"load_balancer"`
	Memory         MemoryConfig         `mapstructure:"memory"`
	Capabilities   CapabilitiesConfig   `mapstructure:"capabilities"`
	Backup         BackupConfig         `mapstructure:"backup"`
	FileProcessing FileProcessingConfig `mapstructure:"file_processing"`
	Plugins        PluginsConfig        `mapstructure:"plugins"`
	Guardrails     GuardrailsConfig     `mapstructure:"guardrails"`
	Agents         AgentsConfig         `mapstructure:"agents"`
	EnableSSE      bool                 `mapstructure:"enable_sse"`
	SSE            SSEConfig            `mapstructure:"sse"`
	Catalog        CatalogConfig        `mapstructure:"catalog"`
	McpGateway     McpGatewayConfig     `mapstructure:"mcp_gateway"`
}

// SSEConfig defines how SSE output should be framed.
type SSEConfig struct {
	DefaultFormat string     `mapstructure:"default_format"`
	Routes        []SSERoute `mapstructure:"routes"`
}

// SSERoute allows route-specific SSE format overrides.
type SSERoute struct {
	Path   string `mapstructure:"path"`
	Format string `mapstructure:"format"`
}

// RateLimitConfig represents gateway-level rate limiting
type RateLimitConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	RequestsPerMinute int    `mapstructure:"requests_per_minute"`
	Burst             int    `mapstructure:"burst"`
	KeySource         string `mapstructure:"key_source"`
}

// LoadBalancerConfig represents load balancing configuration
type LoadBalancerConfig struct {
	Enabled   bool           `mapstructure:"enabled"`
	Strategy  string         `mapstructure:"strategy"`
	Weights   map[string]int `mapstructure:"weights"`
	KeySource string         `mapstructure:"key_source"`
	Fallback  FallbackConfig `mapstructure:"fallback"`
}

// FallbackConfig represents fallback configuration
type FallbackConfig struct {
	Enabled bool                   `mapstructure:"enabled"`
	Default FallbackModelConfig    `mapstructure:"default"`
	Factors []FallbackFactorConfig `mapstructure:"factors"`
}

// FallbackModelConfig represents a fallback model
type FallbackModelConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	Provider    string  `mapstructure:"provider"`
	Model       string  `mapstructure:"model"`
	MaxTokens   int     `mapstructure:"max_tokens"`
	Temperature float64 `mapstructure:"temperature"`
}

// FallbackFactorConfig represents a fallback factor
type FallbackFactorConfig struct {
	Name        string                   `mapstructure:"name"`
	Priority    int                      `mapstructure:"priority"`
	Criteria    []map[string]interface{} `mapstructure:"criteria"`
	Strategy    string                   `mapstructure:"strategy"`
	Models      []FallbackModelConfig    `mapstructure:"models"`
	TimeoutMs   int                      `mapstructure:"timeout_ms"`
	BackoffMs   int                      `mapstructure:"backoff_ms"`
	MaxAttempts int                      `mapstructure:"max_attempts"`
}

// MemoryConfig represents memory store configuration
type MemoryConfig struct {
	Enabled   bool               `mapstructure:"enabled"`
	Type      string             `mapstructure:"type"`
	TTL       string             `mapstructure:"ttl"`
	MaxTokens int                `mapstructure:"max_tokens"`
	Redis     RedisMemoryConfig  `mapstructure:"redis"`
	Vector    VectorMemoryConfig `mapstructure:"vector"`
}

// RedisMemoryConfig represents Redis memory configuration
type RedisMemoryConfig struct {
	Address   string `mapstructure:"address"`
	Password  string `mapstructure:"password"`
	DB        int    `mapstructure:"db"`
	KeyPrefix string `mapstructure:"key_prefix"`
}

// VectorMemoryConfig represents vector database configuration
type VectorMemoryConfig struct {
	Type      string         `mapstructure:"type"`
	Dimension int            `mapstructure:"dimension"`
	Metric    string         `mapstructure:"metric"`
	Pinecone  PineconeConfig `mapstructure:"pinecone"`
	Weaviate  WeaviateConfig `mapstructure:"weaviate"`
}

// PineconeConfig represents Pinecone configuration
type PineconeConfig struct {
	APIKey      string `mapstructure:"api_key"`
	Environment string `mapstructure:"environment"`
	IndexName   string `mapstructure:"index_name"`
}

// WeaviateConfig represents Weaviate configuration
type WeaviateConfig struct {
	URL       string `mapstructure:"url"`
	APIKey    string `mapstructure:"api_key"`
	ClassName string `mapstructure:"class_name"`
}

// CapabilitiesConfig represents model capabilities configuration
type CapabilitiesConfig struct {
	FunctionCalling FunctionCallingConfig `mapstructure:"function_calling"`
}

// FunctionCallingConfig represents function calling configuration
type FunctionCallingConfig struct {
	Enabled   bool     `mapstructure:"enabled"`
	Providers []string `mapstructure:"providers"`
}

// FileProcessingConfig represents file processing configuration
type FileProcessingConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// PluginsConfig represents plugins configuration
type PluginsConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// GuardrailsConfig represents guardrails configuration
type GuardrailsConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// AgentsConfig represents agents configuration
type AgentsConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// LoadGatewayConfig validates user configuration against defaults
func LoadGatewayConfig(userConfig *Config, defaults *DefaultConfigs) (*GatewayConfig, error) {
	// Validate that user provided gateway configuration
	if userConfig.Gateway == nil {
		return nil, fmt.Errorf("gateway configuration is required in gateway.yaml")
	}

	// Load the embedded defaults to use as validation reference
	var defaultModelsConfig ModelsConfig
	if len(defaults.Models) > 0 {
		if err := loadYAMLIntoStruct(defaults.Models, &defaultModelsConfig); err != nil {
			return nil, fmt.Errorf("failed to load models defaults for validation: %w", err)
		}
	}

	// Load provider metadata from providers.yaml and merge into models config
	if len(defaults.Providers) > 0 {
		var providersMetadata ProvidersMetadata
		if err := loadYAMLIntoStruct(defaults.Providers, &providersMetadata); err != nil {
			return nil, fmt.Errorf("failed to load providers defaults for validation: %w", err)
		}

		// Merge provider metadata into models config
		for providerName, providerMeta := range providersMetadata.Providers {
			if providerConfig, exists := defaultModelsConfig.Providers[providerName]; exists {
				providerConfig.ProviderType = providerMeta.ProviderType
				providerConfig.SupportsModelDiscovery = providerMeta.SupportsModelDiscovery
				defaultModelsConfig.Providers[providerName] = providerConfig
			}
		}
	}

	// Validate the user's gateway configuration against defaults
	if err := validateGatewayConfigAgainstDefaults(*userConfig.Gateway, defaultModelsConfig); err != nil {
		return nil, fmt.Errorf("gateway configuration validation failed: %w", err)
	}

	// Validate the configuration structure
	if err := userConfig.Gateway.Validate(); err != nil {
		return nil, fmt.Errorf("invalid gateway config: %w", err)
	}

	// Merge SSE routes from defaults (users cannot override routes; only toggle enablement)
	if defaults != nil && len(defaults.Gateway) > 0 {
		if def, err := ParseGatewayDefaults(defaults.Gateway); err == nil && def != nil {
			userConfig.Gateway.SSE.Routes = def.SSE.Routes
			if userConfig.Gateway.SSE.DefaultFormat == "" && def.SSE.DefaultFormat != "" {
				userConfig.Gateway.SSE.DefaultFormat = def.SSE.DefaultFormat
			}
		}
	}

	return userConfig.Gateway, nil
}

// ParseGatewayDefaults parses gateway defaults bytes into a GatewayConfig (SSE subset used)
func ParseGatewayDefaults(data []byte) (*GatewayConfig, error) {
	var cfg GatewayConfig
	if err := loadYAMLIntoStruct(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validateGatewayConfigAgainstDefaults validates user config against default specifications
func validateGatewayConfigAgainstDefaults(userConfig GatewayConfig, defaultModelsConfig ModelsConfig) error {
	var errors []string

	// Model whitelist validation is intentionally skipped here.
	// The catalog service (model-catalog/) is the source of truth for allowed models
	// and validates at runtime via bootstrapFromDatabase / catalog sync.
	// The embedded defaults/models.yaml is only a fallback for offline operation.

	// Validate rate limiting configuration
	if userConfig.RateLimit.Enabled {
		if userConfig.RateLimit.RequestsPerMinute <= 0 {
			errors = append(errors, "gateway rate_limit.requests_per_minute must be > 0")
		}
		if userConfig.RateLimit.Burst <= 0 {
			errors = append(errors, "gateway rate_limit.burst must be > 0")
		}
		if userConfig.RateLimit.KeySource == "" {
			errors = append(errors, "gateway rate_limit.key_source is required when rate limiting is enabled")
		}
	}

	// Validate load balancer configuration
	if userConfig.LoadBalancer.Enabled {
		if userConfig.LoadBalancer.Strategy == "" {
			errors = append(errors, "load_balancer.strategy is required when load balancer is enabled")
		}
		// Only validate provider/model references if models are configured
		if len(userConfig.Models) > 0 {
			// Weights providers must exist in models
			if len(userConfig.LoadBalancer.Weights) > 0 {
				providers := make(map[string]struct{})
				for _, mc := range userConfig.Models {
					providers[strings.ToLower(mc.Provider)] = struct{}{}
				}
				for p := range userConfig.LoadBalancer.Weights {
					if _, ok := providers[strings.ToLower(p)]; !ok {
						errors = append(errors, fmt.Sprintf("load_balancer.weights references unknown provider '%s'", p))
					}
				}
			}
			// Validate fallback aliases exist in models
			if userConfig.LoadBalancer.Fallback.Enabled {
				aliases := make(map[string]struct{})
				for _, mc := range userConfig.Models {
					for _, a := range mc.Model {
						aliases[strings.ToLower(a)] = struct{}{}
					}
				}
				if userConfig.LoadBalancer.Fallback.Default.Enabled {
					al := strings.ToLower(userConfig.LoadBalancer.Fallback.Default.Model)
					if al != "" {
						if _, ok := aliases[al]; !ok {
							errors = append(errors, fmt.Sprintf("fallback.default.model '%s' is not in gateway.models", al))
						}
					}
				}
				for i, f := range userConfig.LoadBalancer.Fallback.Factors {
					for _, m := range f.Models {
						al := strings.ToLower(m.Model)
						if al != "" {
							if _, ok := aliases[al]; !ok {
								errors = append(errors, fmt.Sprintf("fallback.factors[%d].model '%s' is not in gateway.models", i, al))
							}
						}
					}
				}
			}
		}
	}

	// Validate memory configuration
	if userConfig.Memory.Enabled {
		if userConfig.Memory.Type == "" {
			errors = append(errors, "memory.type is required when memory is enabled")
		}
		if userConfig.Memory.MaxTokens <= 0 {
			errors = append(errors, "memory.max_tokens must be > 0")
		}
		if userConfig.Memory.TTL == "" {
			errors = append(errors, "memory.ttl is required when memory is enabled")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("validation errors: %s", strings.Join(errors, "; "))
	}

	return nil
}

// Validate validates the GatewayConfig
func (g *GatewayConfig) Validate() error {
	var errors []string

	// Validate models - allow zero models/providers for flexible configuration
	// Only validate model constraints if there are models configured
	if len(g.Models) > 0 {
		for i, model := range g.Models {
			if err := model.Validate(); err != nil {
				errors = append(errors, fmt.Sprintf("model[%d]: %s", i, err.Error()))
			}
		}

		// Enforce unique model aliases across all providers
		// Note: Default model validation removed - defaults are now managed via database
		aliasOwner := make(map[string]string)
		for _, mc := range g.Models {
			for _, alias := range mc.Model {
				key := strings.ToLower(alias)
				if owner, exists := aliasOwner[key]; exists {
					errors = append(errors, fmt.Sprintf("duplicate model alias '%s' across providers: %s and %s", alias, owner, mc.Provider))
				} else {
					aliasOwner[key] = mc.Provider
				}
			}
		}
	}

	// Validate rate limit
	if g.RateLimit.Enabled {
		if g.RateLimit.RequestsPerMinute <= 0 {
			errors = append(errors, "rate_limit.requests_per_minute must be > 0")
		}
		if g.RateLimit.Burst <= 0 {
			errors = append(errors, "rate_limit.burst must be > 0")
		}
		if g.RateLimit.KeySource == "" {
			errors = append(errors, "rate_limit.key_source is required when rate limiting is enabled")
		}
	}

	// Validate load balancer
	if g.LoadBalancer.Enabled {
		if g.LoadBalancer.Strategy == "" {
			errors = append(errors, "load_balancer.strategy is required when load balancer is enabled")
		}
		if g.LoadBalancer.KeySource == "" {
			errors = append(errors, "load_balancer.key_source is required when load balancer is enabled")
		}
	}

	// Validate memory
	if g.Memory.Enabled {
		if g.Memory.Type == "" {
			errors = append(errors, "memory.type is required when memory is enabled")
		}
		if g.Memory.MaxTokens <= 0 {
			errors = append(errors, "memory.max_tokens must be > 0")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("gateway validation errors: %s", strings.Join(errors, "; "))
	}

	return nil
}

// McpGatewayConfig represents MCP gateway configuration
type McpGatewayConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	HealthCheckInterval string `mapstructure:"health_check_interval"`
	HealthCheckTimeout  string `mapstructure:"health_check_timeout"`
}

// CatalogConfig represents catalog sync configuration
type CatalogConfig struct {
	RemoteURL      string `mapstructure:"remote_url"`
	EnableAutoSync bool   `mapstructure:"enable_auto_sync"`
	SyncInterval   string `mapstructure:"sync_interval"`
}
