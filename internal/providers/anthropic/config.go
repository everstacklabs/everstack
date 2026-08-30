package anthropic

// Config holds Anthropic provider configuration.
type Config struct {
	APIKey  string   `json:"api_key" mapstructure:"api_key"`
	BaseURL string   `json:"base_url" mapstructure:"base_url"`
	Version string   `json:"version" mapstructure:"version"`
	Models  []string `json:"models" mapstructure:"models"`
}
