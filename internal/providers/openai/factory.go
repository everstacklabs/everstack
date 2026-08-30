package openai

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

// Register the OpenAI provider factory into the global registry.
func init() {
	factory.Default.Register("openai", func(in factory.AggregatedInput) (gw.Provider, error) {
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
				"openai",
			)
			if err != nil {
				// All keys rate-limited or unavailable, trigger provider fallback
				return nil, fmt.Errorf("no available OpenAI API keys: %w", err)
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
			return nil, fmt.Errorf("openai api key not provided")
		}

		base := in.BaseURL
		if base == "" {
			base = DefaultSpec().BaseURL
		}

		baseProvider := NewProvider(Config{
			APIKey:          apiKey,
			BaseURL:         base,
			SupportedModels: dedupeLower(in.Models),
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
	})
}

func dedupeLower(models []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(models))
	for _, m := range models {
		mm := strings.ToLower(m)
		if _, ok := seen[mm]; ok {
			continue
		}
		seen[mm] = struct{}{}
		out = append(out, m)
	}
	return out
}
