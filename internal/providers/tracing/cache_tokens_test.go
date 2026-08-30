package tracing

import (
	"context"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// cachingProvider returns the usage shape internal/providers/anthropic/client.go
// produces for a prompt-cached call: cache reads and cache writes are counted
// separately from fresh input, and PromptTokens is normalised to the total.
type cachingProvider struct{}

func (cachingProvider) Name() string                    { return "anthropic" }
func (cachingProvider) SupportsModel(model string) bool { return true }

func (cachingProvider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{
		ID:    "msg_cached",
		Model: "claude-opus-4-7",
		Usage: gw.Usage{
			PromptTokens:     1000,
			CompletionTokens: 50,
			TotalTokens:      1050,
			PromptDetails: &gw.TokenDetails{
				CachedTokens:     900,
				CacheReadTokens:  700,
				CacheWriteTokens: 200,
			},
			// Cache counts on the completion side are rare in practice, but
			// they are part of the gw.TokenDetails contract and the shared
			// helper carries them. The Chat path's old hand-rolled copy did
			// not, which is the drift this pins.
			CompletionDetails: &gw.TokenDetails{
				ReasoningTokens: 10,
				TextTokens:      40,
				CacheReadTokens: 5,
			},
		},
	}, nil
}

func (cachingProvider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}

func (cachingProvider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	return nil
}

// Guards the cache-token path end to end on the NON-streaming Chat span: the
// adapter's gw.Usage.PromptDetails must reach llm.tokens.cache_read and
// llm.tokens.cache_write, which are the attributes the public model-metrics
// view sums into cache_read_tokens / cache_write_tokens.
//
// Production shows zero cache tokens on Anthropic spans, but that reflects
// traffic that never set cache_control, not a break in this plumbing.
func TestChatPropagatesCacheTokenBreakdown(t *testing.T) {
	prevCfg := telemetry.GetGlobalTracingConfig()
	telemetry.SetGlobalTracingConfig(&telemetry.TracingConfig{TraceProviderCalls: true})
	defer telemetry.SetGlobalTracingConfig(prevCfg)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevTP := telemetry.GetGlobalTracerProvider()
	telemetry.SetGlobalTracerProvider(tp)
	defer telemetry.SetGlobalTracerProvider(prevTP)

	mw := NewMiddlewareWithKey(cachingProvider{}, anthropicCatalogCache(t), "", "", "")
	if _, err := mw.Chat(context.Background(), gw.ChatCompletionRequest{Model: "claude-opus-4-7"}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	attributes := map[string]any{}
	for _, s := range exporter.GetSpans() {
		for _, a := range s.Attributes {
			attributes[string(a.Key)] = a.Value.AsInterface()
		}
	}

	for attr, want := range map[string]int64{
		"llm.tokens.cached":      900,
		"llm.tokens.cache_read":  700,
		"llm.tokens.cache_write": 200,
	} {
		got, ok := attributes[attr].(int64)
		if !ok {
			t.Errorf("%s missing from span; attributes: %v", attr, attributes)
			continue
		}
		if got != want {
			t.Errorf("%s = %d, want %d", attr, got, want)
		}
	}

	// The completion details JSON is produced by the shared tokenBreakdowns
	// helper. The Chat path used to hand-roll its own copy that omitted the
	// cache fields, so the two paths could drift apart unnoticed.
	details, ok := attributes["llm.tokens.completion_details"].(string)
	if !ok {
		t.Fatalf("llm.tokens.completion_details missing; attributes: %v", attributes)
	}
	if !strings.Contains(details, "cache_read_tokens") {
		t.Errorf("completion details dropped the cache breakdown, got %q", details)
	}
}
