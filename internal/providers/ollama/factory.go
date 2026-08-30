package ollama

import (
	"fmt"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/logging"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
)

func init() {
	factory.Default.Register("ollama", func(in factory.AggregatedInput) (gw.Provider, error) {
		// Ollama supports both local and cloud models through the same endpoint:
		// - Local models: Run locally (e.g., llama2, mistral)
		// - Cloud models: Automatically routed to cloud (e.g., gpt-oss:120b-cloud)
		//
		// The local Ollama instance acts as a router - cloud models are automatically
		// offloaded to Ollama's cloud service when accessed through local Ollama.
		//
		// Direct cloud access (https://ollama.com) is also supported for API-only usage.

		baseURL := in.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}

		// API key handling:
		// - For local Ollama: Optional (only needed if you want to use cloud models)
		// - For direct cloud API: Required
		var apiKey string
		isDirectCloud := baseURL == "https://ollama.com" || baseURL == "https://ollama.com/"

		// Get API key if available
		if len(in.APIKeys) > 0 {
			for _, k := range in.APIKeys {
				if k.IsActive {
					apiKey = k.KeyEncrypted
					break
				}
			}
		} else {
			apiKey = in.APIKey
		}

		// Only enforce API key requirement for direct cloud access
		if isDirectCloud && apiKey == "" {
			return nil, fmt.Errorf("direct ollama cloud access requires an API key. Please configure an API key or use local Ollama with cloud models")
		}

		baseProvider := NewProvider(Config{
			BaseURL:         baseURL,
			APIKey:          apiKey, // Pass API key even for local (needed for cloud models)
			SupportedModels: normalizeModels(in.Models),
		})

		// Wrap with tracing middleware (innermost) - with cost calculation if catalog available
		tracedProvider := tracing.NewMiddlewareWithKey(baseProvider, in.CatalogCache, "", "", "")

		// Wrap with logging middleware (outermost)
		return logging.NewMiddleware(
			tracedProvider,
			logging.SelectExtractor(baseProvider.Name()),
		), nil
	})
}

func normalizeModels(ms []string) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, strings.ToLower(m))
	}
	return out
}
