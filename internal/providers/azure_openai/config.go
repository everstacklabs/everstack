package azure_openai

import "net/http"

type Config struct {
	APIKey          string
	BaseURL         string
	APIVersion      string
	SupportedModels []string
	HTTPClient      *http.Client

	// APITypeForModel returns the catalog api_type for a model ("responses",
	// "anthropic" or empty), which selects the wire format. Optional.
	APITypeForModel func(string) string
	// AnthropicBaseURL overrides the base URL Claude requests are sent to.
	// Empty derives it from BaseURL. Optional.
	AnthropicBaseURL string
}
