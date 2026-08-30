package telemetry

import (
	"strings"
)

// ProviderMapping maps model prefixes and patterns to provider names
// This is used for telemetry and observability to track which provider served a request
type ProviderMapping struct {
	// Exact matches (full model name)
	exact map[string]string
	// Prefix matches (model starts with)
	prefixes map[string]string
	// Contains matches (model contains substring)
	contains map[string]string
}

var defaultProviderMapping = &ProviderMapping{
	exact: map[string]string{
		// Exact model name matches
		"gpt-4":              "openai",
		"gpt-4-turbo":        "openai",
		"gpt-3.5-turbo":      "openai",
		"text-davinci-003":   "openai",
		"text-embedding-ada": "openai",
	},
	prefixes: map[string]string{
		// OpenAI
		"gpt-":     "openai",
		"text-":    "openai",
		"davinci-": "openai",
		"curie-":   "openai",
		"babbage-": "openai",
		"ada-":     "openai",
		"o1-":      "openai",

		// Anthropic
		"claude-":        "anthropic",
		"claude-instant": "anthropic",

		// Google
		"gemini-":     "google",
		"palm-":       "google",
		"bard-":       "google",
		"text-bison-": "google",
		"chat-bison-": "google",

		// Cohere
		"command-":  "cohere",
		"embed-":    "cohere",
		"rerank-":   "cohere",
		"generate-": "cohere",

		// Meta
		"llama-":    "meta",
		"llama2-":   "meta",
		"llama3-":   "meta",
		"codellama-": "meta",

		// Mistral
		"mistral-":     "mistral",
		"mixtral-":     "mistral",
		"open-mistral": "mistral",

		// Together AI
		"togethercomputer/": "together",

		// Replicate
		"replicate/": "replicate",

		// HuggingFace
		"huggingface/": "huggingface",

		// Stability AI
		"stable-diffusion-": "stability",
		"sdxl-":             "stability",

		// OpenRouter
		"openrouter/": "openrouter",

		// Perplexity
		"pplx-":        "perplexity",
		"perplexity-":  "perplexity",
		"sonar-":       "perplexity",

		// Fireworks
		"fireworks/":   "fireworks",
		"accounts/":    "fireworks",

		// AI21
		"j2-":          "ai21",
		"jamba-":       "ai21",

		// Amazon Bedrock
		"amazon.":      "aws-bedrock",
		"anthropic.":   "aws-bedrock",
		"meta.":        "aws-bedrock",
		"cohere.":      "aws-bedrock",

		// Azure OpenAI
		"azure/":       "azure-openai",

		// Deepseek
		"deepseek-":    "deepseek",

		// Qwen
		"qwen":         "qwen",
		"qwq-":         "qwen",
		"qwen-vl-":     "qwen",

		// MiniMax
		"minimax-":     "minimax",

		// Moonshot / Kimi
		"kimi-":        "moonshot",
		"moonshot-":    "moonshot",

		// Groq
		"groq/":        "groq",

		// Ollama (local models)
		"ollama/":      "ollama",
	},
	contains: map[string]string{
		// Fallback for models that contain provider name
		"/gpt-":     "openai",
		"/claude-":  "anthropic",
		"/llama":    "meta",
		"/mistral":  "mistral",
	},
}

// ExtractProvider extracts the provider name from a model string
// Uses exact match first, then prefix match, then contains match
// Returns "unknown" if no match found
func ExtractProvider(model string) string {
	if model == "" {
		return "unknown"
	}

	// Normalize to lowercase for matching
	modelLower := strings.ToLower(model)

	// 1. Try exact match first
	if provider, ok := defaultProviderMapping.exact[modelLower]; ok {
		return provider
	}

	// 2. Try prefix match
	for prefix, provider := range defaultProviderMapping.prefixes {
		if strings.HasPrefix(modelLower, prefix) {
			return provider
		}
	}

	// 3. Try contains match
	for substring, provider := range defaultProviderMapping.contains {
		if strings.Contains(modelLower, substring) {
			return provider
		}
	}

	// 4. No match found
	return "unknown"
}

// RegisterProvider adds a new provider mapping
// Useful for custom providers or extending the default mappings
func RegisterProvider(modelPrefix string, providerName string) {
	defaultProviderMapping.prefixes[strings.ToLower(modelPrefix)] = providerName
}

// RegisterExactProvider adds an exact model name → provider mapping
func RegisterExactProvider(modelName string, providerName string) {
	defaultProviderMapping.exact[strings.ToLower(modelName)] = providerName
}

// GetAllProviders returns all known provider names
func GetAllProviders() []string {
	seen := make(map[string]bool)
	providers := []string{}

	for _, provider := range defaultProviderMapping.exact {
		if !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}

	for _, provider := range defaultProviderMapping.prefixes {
		if !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}

	return providers
}

