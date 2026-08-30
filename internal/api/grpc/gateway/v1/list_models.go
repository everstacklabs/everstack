package v1

import (
	"context"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// ListModels (Connect) – unary
func (s *Server) ListModels(ctx context.Context, _ *connect.Request[gatewaypb.ListModelsRequest]) (*connect.Response[gatewaypb.ListModelsResponse], error) {
	resp := &gatewaypb.ListModelsResponse{}

	// Try database first (source of truth when DB is available). Scope to
	// the request's tenant — without this the admin UI's "available
	// models" list would surface every tenant's enabled models, which
	// reveals which providers each tenant has configured.
	if s.ctx != nil {
		if providerRepoAny := s.ctx.Value(contextkeys.ProviderRepo); providerRepoAny != nil {
			providerRepo := providerRepoAny.(*provider_config.Repository)
			configs, err := listProviderConfigs(ctx, providerRepo)
			if err == nil && len(configs) > 0 {
				byProvider := make(map[string][]string)
				for _, config := range configs {
					if !config.IsActive || len(config.EnabledModels) == 0 {
						continue
					}
					p := strings.ToLower(config.ProviderName)
					byProvider[p] = append(byProvider[p], config.EnabledModels...)
				}
				for prov, models := range byProvider {
					resp.Providers = append(resp.Providers, &gatewaypb.ProviderModels{Provider: prov, Models: models})
				}

				sort.Slice(resp.Providers, func(i, j int) bool {
					return resp.Providers[i].Provider < resp.Providers[j].Provider
				})

				return connect.NewResponse(resp), nil
			}
		}
	}

	// Fallback to YAML config
	if s.cfg != nil {
		byProvider := make(map[string][]string)
		for _, mc := range s.cfg.Models {
			p := strings.ToLower(mc.Provider)
			byProvider[p] = append(byProvider[p], mc.Model...)
		}
		for prov, models := range byProvider {
			resp.Providers = append(resp.Providers, &gatewaypb.ProviderModels{Provider: prov, Models: models})
		}
	}

	sort.Slice(resp.Providers, func(i, j int) bool {
		return resp.Providers[i].Provider < resp.Providers[j].Provider
	})

	return connect.NewResponse(resp), nil
}
