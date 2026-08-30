package openai

import "net/http"

// Config contains OpenAI provider configuration.
type Config struct {
	APIKey            string
	BaseURL           string
	Organization      string // optional
	Project           string // optional
	SupportedModels   []string
	ResponsesModels   []string     // models that must use /v1/responses instead of /v1/chat/completions (e.g. codex)
	HTTPClient        *http.Client // optional; default http.DefaultClient
}
