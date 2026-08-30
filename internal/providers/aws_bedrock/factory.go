package aws_bedrock

import (
	"os"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/logging"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
)

func init() {
	factory.Default.Register("aws-bedrock", func(in factory.AggregatedInput) (gw.Provider, error) {
		baseProvider, err := NewProvider(Config{
			Region:          resolveRegion(in.BaseURL),
			BaseURL:         strings.TrimSpace(in.BaseURL),
			SupportedModels: normalizeModels(in.Models),
		})
		if err != nil {
			return nil, err
		}

		var tracedProvider gw.Provider
		if in.CatalogCache != nil {
			tracedProvider = tracing.NewMiddlewareWithCatalog(baseProvider, in.CatalogCache)
		} else {
			tracedProvider = tracing.NewMiddleware(baseProvider)
		}

		return logging.NewMiddleware(
			tracedProvider,
			logging.SelectExtractor(baseProvider.Name()),
		), nil
	})
}

func resolveRegion(baseURL string) string {
	if region := strings.TrimSpace(os.Getenv("AWS_REGION")); region != "" {
		return region
	}
	if region := strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")); region != "" {
		return region
	}
	host := strings.TrimSpace(baseURL)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.Split(host, "/")[0]
	parts := strings.Split(host, ".")
	for i, part := range parts {
		if part == "bedrock-runtime" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return "us-east-1"
}

func normalizeModels(ms []string) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, strings.ToLower(m))
	}
	return out
}
