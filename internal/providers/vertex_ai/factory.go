package vertex_ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/logging"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
	"github.com/everstacklabs/everstack/internal/services/api_key_selector"
)

func init() {
	factory.Default.Register("vertex-ai", func(in factory.AggregatedInput) (gw.Provider, error) {
		var credentials string
		var selectedKeyID, selectedKeyName, selectedKeySource string
		if len(in.APIKeys) > 0 {
			domainKeys := make([]*provider_api_keys.ProviderAPIKey, len(in.APIKeys))
			for i, k := range in.APIKeys {
				domainKeys[i] = &provider_api_keys.ProviderAPIKey{ID: k.ID, KeyName: k.KeyName, KeyEncrypted: k.KeyEncrypted, Weight: k.Weight, IsActive: k.IsActive, Source: k.Source}
			}
			selector := api_key_selector.New(ratelimit.GlobalMonitor)
			selectedKey, err := selector.SelectAPIKey(context.Background(), domainKeys, in.StickyKey, "vertex-ai")
			if err != nil {
				return nil, fmt.Errorf("no available Vertex AI credentials: %w", err)
			}
			selectedKeyID = selectedKey.ID
			selectedKeyName = selectedKey.KeyName
			selectedKeySource = selectedKey.Source
			credentials = selectedKey.KeyEncrypted
		} else {
			credentials = in.APIKey
		}

		baseProvider, err := NewProvider(Config{
			Credentials:     credentials,
			BaseURL:         strings.TrimSpace(in.BaseURL),
			SupportedModels: normalizeModels(in.Models),
		})
		if err != nil {
			return nil, err
		}

		tracedProvider := tracing.NewMiddlewareWithKey(baseProvider, in.CatalogCache, selectedKeyID, selectedKeyName, selectedKeySource)

		return logging.NewMiddleware(tracedProvider, logging.SelectExtractor(baseProvider.Name())), nil
	})
}

func normalizeModels(ms []string) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, strings.ToLower(m))
	}
	return out
}
