package tracing

import (
	"context"
	"math"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Prices match the production gateway catalog row for claude-opus-4-7.
const anthropicCatalogYAML = `
providers:
  anthropic:
    name: anthropic
    models:
      - name: claude-opus-4-7
        display_name: Claude Opus 4.7
        publisher: anthropic
        canonical_model_id: anthropic/claude-opus-4-7
        input_cost_per_1k: 0.005
        output_cost_per_1k: 0.025
        capabilities: [chat]
        status: available
`

// fakeAnthropicProvider mirrors the shape of a real non-streaming Anthropic
// reply, using the token counts observed on a production span.
type fakeAnthropicProvider struct{}

func (fakeAnthropicProvider) Name() string                    { return "anthropic" }
func (fakeAnthropicProvider) SupportsModel(model string) bool { return true }

func (fakeAnthropicProvider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{
		ID:    "msg_test",
		Model: "claude-opus-4-7",
		Usage: gw.Usage{PromptTokens: 65, CompletionTokens: 226, TotalTokens: 291},
	}, nil
}

func (fakeAnthropicProvider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}

func (fakeAnthropicProvider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	return nil
}

func anthropicCatalogCache(t *testing.T) *catalog.Cache {
	t.Helper()
	cache := catalog.NewCache()
	if err := cache.Load([]byte(anthropicCatalogYAML), []byte("providers: {}\n")); err != nil {
		t.Fatalf("loading catalog: %v", err)
	}
	if _, ok := cache.GetModel("anthropic", "claude-opus-4-7"); !ok {
		t.Fatal("precondition: catalog must resolve claude-opus-4-7")
	}
	return cache
}

func chatSpanAttributes(t *testing.T, catalogCache *catalog.Cache) map[string]any {
	t.Helper()

	prevCfg := telemetry.GetGlobalTracingConfig()
	telemetry.SetGlobalTracingConfig(&telemetry.TracingConfig{TraceProviderCalls: true})
	defer telemetry.SetGlobalTracingConfig(prevCfg)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevTP := telemetry.GetGlobalTracerProvider()
	telemetry.SetGlobalTracerProvider(tp)
	defer telemetry.SetGlobalTracerProvider(prevTP)

	mw := NewMiddlewareWithKey(fakeAnthropicProvider{}, catalogCache, "key-id", "test", "manual")
	if _, err := mw.Chat(context.Background(), gw.ChatCompletionRequest{Model: "claude-opus-4-7"}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	attributes := map[string]any{}
	for _, s := range exporter.GetSpans() {
		for _, a := range s.Attributes {
			attributes[string(a.Key)] = a.Value.AsInterface()
		}
	}
	if len(attributes) == 0 {
		t.Fatal("no span attributes recorded")
	}
	return attributes
}

// With a catalog the Anthropic span must carry llm.cost.total, which is the
// attribute the public model-metrics view sums into total_cost_usd. Every
// production Anthropic span was missing it, so the catalog's Cost series read
// a flat zero for the whole publisher.
func TestChatStampsCostForAnthropicWhenCatalogPresent(t *testing.T) {
	attributes := chatSpanAttributes(t, anthropicCatalogCache(t))

	total, ok := attributes["llm.cost.total"].(float64)
	if !ok {
		t.Fatalf("llm.cost.total missing from Anthropic span; attributes: %v", attributes)
	}

	// 65 input @ $0.005/1k + 226 output @ $0.025/1k
	want := 65*0.005/1000 + 226*0.025/1000
	if math.Abs(total-want) > 1e-9 {
		t.Errorf("llm.cost.total = %v, want %v", total, want)
	}
	if _, ok := attributes["llm.usage_details"]; !ok {
		t.Error("llm.usage_details must accompany a priced span")
	}
}

// Characterises the exact production symptom this fix removes: with no
// catalog the middleware falls through to RecordLLMMetrics, which stamps a
// legacy `llm.cost` of 0 and never writes llm.cost.total. Finding `llm.cost`
// and no `llm.cost.total` on a span is the fingerprint of a bundle that was
// built without a catalog cache.
func TestChatWithoutCatalogRecordsNoTotalCost(t *testing.T) {
	attributes := chatSpanAttributes(t, nil)

	if _, ok := attributes["llm.cost.total"]; ok {
		t.Error("llm.cost.total must not appear when no catalog is available")
	}
	legacy, ok := attributes["llm.cost"].(float64)
	if !ok {
		t.Fatalf("expected the legacy llm.cost fallback; attributes: %v", attributes)
	}
	if legacy != 0 {
		t.Errorf("legacy llm.cost = %v, want 0", legacy)
	}
}
