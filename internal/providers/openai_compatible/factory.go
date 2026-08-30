package openai_compatible

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/compact"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/logging"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
	"github.com/everstacklabs/everstack/internal/services/api_key_selector"
)

// CreateOpenAICompatibleFactory creates a factory function for OpenAI-compatible providers
func CreateOpenAICompatibleFactory(
	providerName string,
	defaultBaseURL string,
	authHeaderName string,
	authHeaderFormat string,
) factory.ProviderFactory {
	return func(in factory.AggregatedInput) (gw.Provider, error) {
		var apiKey string
		var selectedKeyID, selectedKeyName, selectedKeySource string

		// New path: Select from multiple keys
		if len(in.APIKeys) > 0 {
			// Convert factory.APIKeyInput to domain.ProviderAPIKey
			domainKeys := make([]*provider_api_keys.ProviderAPIKey, len(in.APIKeys))
			for i, k := range in.APIKeys {
				domainKeys[i] = &provider_api_keys.ProviderAPIKey{
					ID:           k.ID,
					KeyName:      k.KeyName,
					KeyEncrypted: k.KeyEncrypted,
					Weight:       k.Weight,
					IsActive:     k.IsActive,
					Source:       k.Source,
				}
			}

			selector := api_key_selector.New(ratelimit.GlobalMonitor)
			selectedKey, err := selector.SelectAPIKey(
				context.Background(),
				domainKeys,
				in.StickyKey,
				providerName,
			)
			if err != nil {
				// All keys rate-limited or unavailable, trigger provider fallback
				return nil, fmt.Errorf("no available %s API keys: %w", providerName, err)
			}
			selectedKeyID = selectedKey.ID
			selectedKeyName = selectedKey.KeyName
			selectedKeySource = selectedKey.Source
			apiKey = selectedKey.KeyEncrypted
		} else {
			// Legacy path: Use single key
			apiKey = in.APIKey
		}

		if strings.TrimSpace(apiKey) == "" {
			return nil, fmt.Errorf("%s api key not provided", providerName)
		}

		base := in.BaseURL
		if base == "" {
			base = defaultBaseURL
		}

		baseProvider := NewProvider(Config{
			ProviderName:    providerName,
			APIKey:          apiKey,
			BaseURL:         base,
			AuthHeaderName:  authHeaderName,
			AuthHeaderValue: authHeaderFormat,
			SupportedModels: normalizeModels(in.Models),
		})

		// Wrap with tracing middleware (innermost) - with cost calculation if catalog available
		tracedProvider := tracing.NewMiddlewareWithKey(baseProvider, in.CatalogCache, selectedKeyID, selectedKeyName, selectedKeySource)

		// Wrap with compact middleware (between tracing and logging).
		compacted := compact.NewMiddleware(tracedProvider, baseProvider.Name(), in.CompactConfig, compact.SummarizerResolver(in.SummarizerResolve))

		// Wrap with logging middleware (outermost)
		return logging.NewMiddleware(
			compacted,
			logging.SelectExtractor(baseProvider.Name()),
		), nil
	}
}

func normalizeModels(ms []string) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, strings.ToLower(m))
	}
	return out
}
