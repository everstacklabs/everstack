package v1

import (
	"context"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/services/provider_catalog"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1"
)

// ListProviderCatalog returns all available providers from the catalog (defaults)
func (s *Server) ListProviderCatalog(ctx context.Context, req *connect.Request[providerspb.ListProviderCatalogRequest]) (*connect.Response[providerspb.ListProviderCatalogResponse], error) {
	// Get all providers from catalog
	catalog := s.catalog.GetCatalog()

	// Convert to proto format
	providers := make([]*providerspb.ProviderCatalogEntry, 0, len(catalog))
	for _, entry := range catalog {
		providers = append(providers, &providerspb.ProviderCatalogEntry{
			Name:        entry.Name,
			DisplayName: entry.DisplayName,
			BaseUrl:     entry.BaseURL,
			ApiVersion:  entry.APIVersion,
			Capabilities: &providerspb.ProviderCapabilities{
				Chat:            entry.Capabilities.Chat,
				Completions:     entry.Capabilities.Completions,
				Embeddings:      entry.Capabilities.Embeddings,
				FunctionCalling: entry.Capabilities.FunctionCalling,
				Vision:          entry.Capabilities.Vision,
				Streaming:       entry.Capabilities.Streaming,
				FineTuning:      entry.Capabilities.FineTuning,
				Assistants:      entry.Capabilities.Assistants,
			},
			ModelFamilies: convertModelFamilies(entry.ModelFamilies),
			Models:        convertModels(entry.Models),
			RateLimits: &providerspb.RateLimits{
				RequestsPerMinute:  entry.RateLimits.RequestsPerMinute,
				TokensPerMinute:    entry.RateLimits.TokensPerMinute,
				ConcurrentRequests: entry.RateLimits.ConcurrentRequests,
			},
			Description:            entry.Description,
			ProviderType:           entry.ProviderType,
			SupportsModelDiscovery: entry.SupportsModelDiscovery,
			DiscoveryApiEndpoint:   entry.DiscoveryAPIEndpoint,
		})
	}

	resp := &providerspb.ListProviderCatalogResponse{
		Providers: providers,
	}

	return connect.NewResponse(resp), nil
}

// Helper functions to convert internal types to proto types

func convertModelFamilies(families []provider_catalog.ModelFamily) []*providerspb.ModelFamily {
	result := make([]*providerspb.ModelFamily, 0, len(families))
	for _, family := range families {
		result = append(result, &providerspb.ModelFamily{
			Name:         family.Name,
			Description:  family.Description,
			Capabilities: family.Capabilities,
			MaxTokens:    family.MaxTokens,
		})
	}
	return result
}

func convertModels(models []provider_catalog.ModelMetadata) []*providerspb.ModelMetadata {
	result := make([]*providerspb.ModelMetadata, 0, len(models))
	for _, model := range models {
		result = append(result, &providerspb.ModelMetadata{
			Name:             model.Name,
			DisplayName:      model.DisplayName,
			MaxTokens:        model.MaxTokens,
			Capabilities:     model.Capabilities,
			InputCostPer_1K:  model.InputCostPer1K,
			OutputCostPer_1K: model.OutputCostPer1K,
			Status:           model.Status,
			Freshness:        modelFreshness(model.AddedInVersion),
			AddedInVersion:   model.AddedInVersion,
			MaxOutputTokens:  model.MaxOutputTokens,
			InputModalities:  model.InputModalities,
			OutputModalities: model.OutputModalities,
			StructuredOutput: model.StructuredOutput,
			Parameters:       convertModelParameters(model.Parameters),
			Variants:         convertModelVariants(model.Variants),
		})
	}
	return result
}

func convertModelParameters(parameters []validator.ModelParameter) []*providerspb.ModelParameter {
	result := make([]*providerspb.ModelParameter, 0, len(parameters))
	for _, parameter := range parameters {
		result = append(result, &providerspb.ModelParameter{
			Key:               parameter.Key,
			DisplayName:       parameter.DisplayName,
			Type:              parameter.Type,
			Options:           parameter.Options,
			MinValue:          parameter.MinValue,
			MaxValue:          parameter.MaxValue,
			HasMinValue:       parameter.HasMinValue,
			HasMaxValue:       parameter.HasMaxValue,
			RequiresStreaming: parameter.RequiresStreaming,
		})
	}
	return result
}

func convertModelVariants(variants []validator.ModelVariant) []*providerspb.ModelVariant {
	result := make([]*providerspb.ModelVariant, 0, len(variants))
	for _, variant := range variants {
		result = append(result, &providerspb.ModelVariant{
			Id:          variant.ID,
			DisplayName: variant.DisplayName,
			Description: variant.Description,
			Parameters:  variant.Parameters,
		})
	}
	return result
}

func modelFreshness(addedInVersion string) string {
	if addedInVersion != "" {
		return "new"
	}
	return "stable"
}
