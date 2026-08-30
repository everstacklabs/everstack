package ollama

// Config holds Ollama provider configuration
type Config struct {
	BaseURL         string
	APIKey          string // Optional: only needed for Ollama Cloud
	SupportedModels []string
}
