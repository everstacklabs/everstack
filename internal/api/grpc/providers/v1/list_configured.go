package v1

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/services/provider_config"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1"
)

// ListConfiguredProviders returns all providers with their status (configured and unconfigured)
func (s *Server) ListConfiguredProviders(ctx context.Context, req *connect.Request[providerspb.ListConfiguredProvidersRequest]) (*connect.Response[providerspb.ListConfiguredProvidersResponse], error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	var statuses []provider_config.ProviderStatus
	var err error

	if req.Msg.ActiveOnly {
		statuses, err = s.configService.ListActiveProvidersForOrg(ctx, tenantID)
	} else {
		statuses, err = s.configService.ListAllForOrg(ctx, tenantID)
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert all providers (both configured and unconfigured)
	providers := make([]*providerspb.ProviderStatus, 0, len(statuses))
	for _, status := range statuses {
		providers = append(providers, convertProviderStatus(&status))
	}

	resp := &providerspb.ListConfiguredProvidersResponse{
		Providers: providers,
	}

	return connect.NewResponse(resp), nil
}

// convertProviderStatus converts internal ProviderStatus to proto format
func convertProviderStatus(status *provider_config.ProviderStatus) *providerspb.ProviderStatus {
	protoStatus := &providerspb.ProviderStatus{
		IsConfigured:          status.IsConfigured,
		IsActive:              status.IsActive,
		ConfiguredModelsCount: int32(status.ConfiguredModelsCount),
		AvailableModelsCount:  int32(status.AvailableModelsCount),
		CatalogStatus:         status.CatalogStatus,
		IsFromCatalog:         status.IsFromCatalog,
	}

	// Convert catalog entry
	if status.Catalog != nil {
		protoStatus.Catalog = &providerspb.ProviderCatalogEntry{
			Name:        status.Catalog.Name,
			DisplayName: status.Catalog.DisplayName,
			BaseUrl:     status.Catalog.BaseURL,
			ApiVersion:  status.Catalog.APIVersion,
			Capabilities: &providerspb.ProviderCapabilities{
				Chat:            status.Catalog.Capabilities.Chat,
				Completions:     status.Catalog.Capabilities.Completions,
				Embeddings:      status.Catalog.Capabilities.Embeddings,
				FunctionCalling: status.Catalog.Capabilities.FunctionCalling,
				Vision:          status.Catalog.Capabilities.Vision,
				Streaming:       status.Catalog.Capabilities.Streaming,
				FineTuning:      status.Catalog.Capabilities.FineTuning,
				Assistants:      status.Catalog.Capabilities.Assistants,
			},
			ModelFamilies: convertModelFamilies(status.Catalog.ModelFamilies),
			Models:        convertModels(status.Catalog.Models),
			RateLimits: &providerspb.RateLimits{
				RequestsPerMinute:  status.Catalog.RateLimits.RequestsPerMinute,
				TokensPerMinute:    status.Catalog.RateLimits.TokensPerMinute,
				ConcurrentRequests: status.Catalog.RateLimits.ConcurrentRequests,
			},
			Description:            status.Catalog.Description,
			ProviderType:           status.Catalog.ProviderType,
			SupportsModelDiscovery: status.Catalog.SupportsModelDiscovery,
			DiscoveryApiEndpoint:   status.Catalog.DiscoveryAPIEndpoint,
		}
	}

	// Convert configuration
	if status.Configuration != nil {
		customSettings := make(map[string]string)
		if status.Configuration.CustomSettings != nil {
			customSettings = status.Configuration.CustomSettings
		}

		baseURL := ""
		if status.Configuration.CustomBaseURL != nil {
			baseURL = *status.Configuration.CustomBaseURL
		}

		syncedAt := ""
		if status.Configuration.SyncedToYAMLAt != nil {
			syncedAt = status.Configuration.SyncedToYAMLAt.Format("2006-01-02T15:04:05Z07:00")
		}

		lastUsedAt := ""
		if status.Configuration.LastUsedAt != nil {
			lastUsedAt = status.Configuration.LastUsedAt.Format("2006-01-02T15:04:05Z07:00")
		}

		protoStatus.Configuration = &providerspb.ProviderConfiguration{
			Id:           status.Configuration.ID,
			ProviderName: status.Configuration.ProviderName,
			// Never return raw provider API keys to the UI.
			ApiKey:         maskAPIKey(status.Configuration.APIKeyEncrypted),
			EnabledModels:  status.Configuration.EnabledModels,
			CustomBaseUrl:  baseURL,
			CustomSettings: customSettings,
			IsActive:       status.Configuration.IsActive,
			CreatedAt:      status.Configuration.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt:      status.Configuration.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			SyncedToYamlAt: syncedAt,
			LastUsedAt:     lastUsedAt,
		}
	}

	return protoStatus
}
