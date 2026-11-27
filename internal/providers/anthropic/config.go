package anthropic

// Config holds Anthropic provider configuration.
type Config struct {
	APIKey  string   `json:"api_key" mapstructure:"api_key"`
	BaseURL string   `json:"base_url" mapstructure:"base_url"`
	Version string   `json:"version" mapstructure:"version"`
	Models  []string `json:"models" mapstructure:"models"`

	// ProviderName overrides the name this client reports in logs, traces and
	// rate-limit tracking. Empty means "anthropic". A hosting provider that
	// reuses this client (Claude on Azure) sets it so its traffic is not
	// counted against first-party Anthropic keys.
	ProviderName string `json:"-" mapstructure:"-"`
}
