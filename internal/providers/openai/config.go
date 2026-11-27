package openai

import "net/http"

// Config contains OpenAI provider configuration.
type Config struct {
	APIKey          string
	BaseURL         string
	Organization    string // optional
	Project         string // optional
	SupportedModels []string
	ResponsesModels []string            // models that must use /v1/responses instead of /v1/chat/completions (e.g. codex)
	APITypeForModel func(string) string // live catalog lookup for endpoint-specific models
	HTTPClient      *http.Client        // optional; default http.DefaultClient

	// ProviderName overrides the name this client reports in logs, traces and
	// rate-limit tracking. Empty means "openai". A hosting provider that reuses
	// this client (Azure OpenAI) sets it so its traffic is not counted against
	// first-party OpenAI keys.
	ProviderName string
	// SetAuth, when set, writes the credential headers instead of the default
	// "Authorization: Bearer <key>".
	SetAuth func(http.Header)
}
