package gateway

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
)

// routeProbe is a provider that only records which instance was resolved.
type routeProbe struct {
	name   string
	owner  string
	models map[string]bool
}

func (p *routeProbe) Name() string                    { return p.name }
func (p *routeProbe) SupportsModel(model string) bool { return p.models[model] }
func (p *routeProbe) Chat(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return ChatCompletionResponse{}, nil
}
func (p *routeProbe) ChatStream(context.Context, ChatCompletionRequest, func(ChatResponseChunk) error) error {
	return nil
}
func (p *routeProbe) Embed(context.Context, EmbeddingsRequest) (EmbeddingsResponse, error) {
	return EmbeddingsResponse{}, nil
}

// A router must only ever hand out providers from its own registry. Routers
// are built per tenant (and rebuilt when keys change), each with provider
// instances that hold that tenant's credentials. A process-wide cache of
// provider instances consulted ahead of the registry would serve one
// bundle's instance, and so its API key, to every other router.
func TestRouterNeverResolvesToAProviderOutsideItsRegistry(t *testing.T) {
	engine, err := fastpath.NewEngine(fastpath.DefaultEngineConfig())
	if err != nil {
		t.Fatal(err)
	}
	prev := fastpath.GetGlobalEngine()
	fastpath.SetGlobalEngine(engine)
	t.Cleanup(func() { fastpath.SetGlobalEngine(prev) })

	foreign := &routeProbe{name: "openai", owner: "boot", models: map[string]bool{"gpt-4o": true}}
	engine.WarmRouterCache(func() map[string]fastpath.RouteInfo {
		return map[string]fastpath.RouteInfo{
			"openai": {ProviderName: "openai", ModelName: "openai", Provider: foreign},
			"gpt-4o": {ProviderName: "openai", ModelName: "gpt-4o", Provider: foreign},
		}
	})
	engine.RouterCache().MarkWarmed()

	own := &routeProbe{name: "openai", owner: "tenant", models: map[string]bool{"gpt-4o": true}}
	registry := NewRegistry()
	registry.Register(own)
	router := NewRouter(registry, map[string]string{"gpt-4o": "openai"})

	got, _, err := router.ResolveWithContext(context.Background(), "gpt-4o")
	if err != nil {
		t.Fatalf("resolve gpt-4o: %v", err)
	}
	if got != Provider(own) {
		t.Fatalf("gpt-4o resolved to the %q instance, want the router's own", got.(*routeProbe).owner)
	}

	// A model string equal to a provider name is what the boot-time warm-up
	// used to key the cache by.
	if got, _, err := router.ResolveWithContext(context.Background(), "openai"); err == nil && got != Provider(own) {
		t.Fatalf("model %q resolved to the %q instance, outside the router's registry", "openai", got.(*routeProbe).owner)
	}
}
