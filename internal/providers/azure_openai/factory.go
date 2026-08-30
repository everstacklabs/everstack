package azure_openai

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

func init() {
	factory.Default.Register("azure-openai", func(in factory.AggregatedInput) (gw.Provider, error) {
		var apiKey string
		var selectedKeyID, selectedKeyName, selectedKeySource string

		if len(in.APIKeys) > 0 {
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
				"azure-openai",
			)
			if err != nil {
				return nil, fmt.Errorf("no available Azure OpenAI API keys: %w", err)
			}
			selectedKeyID = selectedKey.ID
			selectedKeyName = selectedKey.KeyName
			selectedKeySource = selectedKey.Source
			apiKey = selectedKey.KeyEncrypted
		} else {
			apiKey = in.APIKey
		}

		if strings.TrimSpace(apiKey) == "" {
			return nil, fmt.Errorf("azure-openai api key not provided")
		}

		baseURL := strings.TrimSpace(in.BaseURL)
		if baseURL == "" {
			baseURL = DefaultSpec().BaseURL
		}

		baseProvider := NewProvider(Config{
			APIKey:          apiKey,
			BaseURL:         baseURL,
			APIVersion:      DefaultSpec().APIVersion,
			SupportedModels: normalizeModels(in.Models),
		})

		tracedProvider := tracing.NewMiddlewareWithKey(baseProvider, in.CatalogCache, selectedKeyID, selectedKeyName, selectedKeySource)

		// Wrap with compact middleware (between tracing and logging).
		compacted := compact.NewMiddleware(tracedProvider, baseProvider.Name(), in.CompactConfig, compact.SummarizerResolver(in.SummarizerResolve))

		return logging.NewMiddleware(
			compacted,
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
