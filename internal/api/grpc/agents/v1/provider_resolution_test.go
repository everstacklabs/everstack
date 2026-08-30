package v1

import (
	"context"
	"fmt"
	"testing"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// tenantProviderSource models what the gateway does after a provider is
// configured at runtime: it exposes a new tenant-scoped registry/router rather
// than mutating the startup pointers captured by the agents server.
type staticTenantProviderSource struct {
	registry *gw.Registry
	router   *gw.Router
}

func (s *staticTenantProviderSource) ProviderBundleForRequest(context.Context) (*gw.Registry, *gw.Router, error) {
	return s.registry, s.router, nil
}

type resolvingTestProvider struct {
	name  string
	model string
}

func (p *resolvingTestProvider) Name() string { return p.name }

func (p *resolvingTestProvider) SupportsModel(model string) bool {
	return model == p.model
}

func (p *resolvingTestProvider) Chat(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{}, nil
}

func (p *resolvingTestProvider) ChatStream(context.Context, gw.ChatCompletionRequest, func(gw.ChatResponseChunk) error) error {
	return nil
}

func (p *resolvingTestProvider) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}

type tenantProviderBundle struct {
	registry *gw.Registry
	router   *gw.Router
}

type perTenantProviderSource struct {
	bundles map[string]tenantProviderBundle
}

func (s *perTenantProviderSource) ProviderBundleForRequest(ctx context.Context) (*gw.Registry, *gw.Router, error) {
	bundle, ok := s.bundles[contextkeys.GetTenantID(ctx)]
	if !ok {
		return nil, nil, fmt.Errorf("provider bundle unavailable for tenant")
	}
	return bundle.registry, bundle.router, nil
}

func newTestProviderBundle(providerName, model string) tenantProviderBundle {
	registry := gw.NewRegistry()
	registry.Register(&resolvingTestProvider{name: providerName, model: model})
	return tenantProviderBundle{
		registry: registry,
		router:   gw.NewRouter(registry, map[string]string{model: providerName}),
	}
}

func TestSetProviderRefresherMakesTenantProvidersAvailableToAgentEngine(t *testing.T) {
	startupRegistry := gw.NewRegistry()
	startupRouter := gw.NewRouter(startupRegistry, nil)
	server := &Server{
		engine: agentrt.NewEngine(startupRegistry, startupRouter, nil),
	}

	tenantRegistry := gw.NewRegistry()
	tenantRegistry.Register(&resolvingTestProvider{name: "openai", model: "gpt-5.5"})
	tenantRouter := gw.NewRouter(tenantRegistry, map[string]string{"gpt-5.5": "openai"})
	providerSource := &staticTenantProviderSource{
		registry: tenantRegistry,
		router:   tenantRouter,
	}
	server.SetProviderRefresher(providerSource)

	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
	if _, _, err := tenantRouter.ResolveWithContext(ctx, "gpt-5.5"); err != nil {
		t.Fatalf("test control: refreshed tenant router cannot resolve model: %v", err)
	}

	provider, resolvedModel, err := server.engine.ResolveProvider(ctx, "gpt-5.5")
	if err != nil {
		t.Fatalf("agent engine did not see the tenant's configured provider: %v", err)
	}
	if provider == nil {
		t.Fatal("agent engine resolved a nil provider")
	}
	if resolvedModel != "gpt-5.5" {
		t.Fatalf("resolved model = %q, want %q", resolvedModel, "gpt-5.5")
	}
}

func TestAgentEngineProviderResolutionIsTenantScoped(t *testing.T) {
	// Deliberately make the startup router capable of resolving both models.
	// Once a request-scoped source is configured, falling back to this router
	// would be a cross-tenant credential leak.
	startupRegistry := gw.NewRegistry()
	startupRegistry.Register(&resolvingTestProvider{name: "startup", model: "tenant-a-model"})
	startupRouter := gw.NewRouter(startupRegistry, map[string]string{
		"tenant-a-model": "startup",
		"tenant-b-model": "startup",
	})
	server := &Server{
		engine: agentrt.NewEngine(startupRegistry, startupRouter, nil),
	}
	server.SetProviderRefresher(&perTenantProviderSource{
		bundles: map[string]tenantProviderBundle{
			"tenant-a": newTestProviderBundle("openai-a", "tenant-a-model"),
			"tenant-b": newTestProviderBundle("openai-b", "tenant-b-model"),
		},
	})

	tenantA := contextkeys.WithTenantID(context.Background(), "tenant-a")
	tenantB := contextkeys.WithTenantID(context.Background(), "tenant-b")
	if _, _, err := server.engine.ResolveProvider(tenantA, "tenant-a-model"); err != nil {
		t.Fatalf("tenant A could not resolve its model: %v", err)
	}
	if _, _, err := server.engine.ResolveProvider(tenantB, "tenant-b-model"); err != nil {
		t.Fatalf("tenant B could not resolve its model: %v", err)
	}
	if _, _, err := server.engine.ResolveProvider(tenantA, "tenant-b-model"); err == nil {
		t.Fatal("tenant A resolved tenant B's model")
	}
	if _, _, err := server.engine.ResolveProvider(context.Background(), "tenant-a-model"); err == nil {
		t.Fatal("provider resolution without tenant context did not fail closed")
	}
}
