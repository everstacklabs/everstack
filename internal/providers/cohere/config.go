package cohere

// Config holds Cohere provider configuration
type Config struct {
	APIKey          string
	BaseURL         string
	SupportedModels []string
}
