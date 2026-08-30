package catalog

import "time"

// ModelDefinition represents a model in the catalog
type ModelDefinition struct {
	Name             string  `yaml:"name"`
	DisplayName      string  `yaml:"display_name"`
	Publisher        string  `yaml:"publisher,omitempty"`
	CanonicalModelID string  `yaml:"canonical_model_id,omitempty"`
	MaxTokens        int     `yaml:"max_tokens"`
	InputCostPer1k   float64 `yaml:"input_cost_per_1k"`
	OutputCostPer1k  float64 `yaml:"output_cost_per_1k"`
	// Zero means the catalog does not price cache reads/writes for this model.
	CacheReadCostPer1k  float64  `yaml:"cache_read_cost_per_1k,omitempty"`
	CacheWriteCostPer1k float64  `yaml:"cache_write_cost_per_1k,omitempty"`
	Capabilities        []string `yaml:"capabilities"`
	Status              string   `yaml:"status"`
	Family              string   `yaml:"family,omitempty"`
	ContextLength       int      `yaml:"context_length,omitempty"`
	// Parameters are the request controls this model accepts. The gateway
	// uses them to decide whether a provider-wide default may be applied to
	// this model, so a control the model rejects is never sent to it.
	Parameters []ModelParameterDefinition `yaml:"parameters,omitempty"`
}

// ModelParameterDefinition names one request control a model accepts. Only the
// key matters to the gateway; the display metadata is for the Admin UI, which
// reads the catalog over the providers API rather than through this cache.
type ModelParameterDefinition struct {
	Key string `yaml:"key"`
}

// SupportsParameter reports whether the model declares the named request
// control. A model with no declared parameters returns false for everything;
// callers decide what an unknown model means for them.
func (m *ModelDefinition) SupportsParameter(key string) bool {
	for _, parameter := range m.Parameters {
		if parameter.Key == key {
			return true
		}
	}
	return false
}

// ProviderDefinition represents a provider in the catalog
type ProviderDefinition struct {
	Name        string            `yaml:"name"`
	DisplayName string            `yaml:"display_name"`
	BaseURL     string            `yaml:"base_url"`
	APIVersion  string            `yaml:"api_version,omitempty"`
	Auth        AuthConfig        `yaml:"auth,omitempty"`
	RateLimits  RateLimitConfig   `yaml:"rate_limits,omitempty"`
	Models      []ModelDefinition `yaml:"-"` // Populated separately
}

// AuthConfig represents authentication configuration
type AuthConfig struct {
	Type         string `yaml:"type"`
	HeaderName   string `yaml:"header_name,omitempty"`
	HeaderFormat string `yaml:"header_format,omitempty"`
	EnvVar       string `yaml:"env_var,omitempty"`
}

// RateLimitConfig represents rate limit configuration
type RateLimitConfig struct {
	RequestsPerMinute  int `yaml:"requests_per_minute,omitempty"`
	TokensPerMinute    int `yaml:"tokens_per_minute,omitempty"`
	ConcurrentRequests int `yaml:"concurrent_requests,omitempty"`
}

// ModelsYAML represents the structure of models.yaml
type ModelsYAML struct {
	Providers  map[string]ProviderModels `yaml:"providers"`
	Categories map[string][]string       `yaml:"categories,omitempty"`
}

// ProviderModels represents models section for a provider
type ProviderModels struct {
	Name    string            `yaml:"name"`
	BaseURL string            `yaml:"base_url"`
	Models  []ModelDefinition `yaml:"models"`
}

// ProvidersYAML represents the structure of providers.yaml
type ProvidersYAML struct {
	Providers map[string]ProviderDefinition `yaml:"providers"`
}

// CatalogMetadata represents catalog version and metadata
type CatalogMetadata struct {
	Version     string    `yaml:"version"`
	LastUpdated time.Time `yaml:"last_updated"`
}
