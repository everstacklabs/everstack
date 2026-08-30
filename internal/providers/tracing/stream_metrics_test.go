package tracing

import (
	"context"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// instantStreamProvider answers immediately, so the first chunk lands inside
// the same millisecond as the request and firstChunkLatencyMs rounds to 0.
// A cached or local model behaves this way routinely.
type instantStreamProvider struct{}

func (instantStreamProvider) Name() string                    { return "openai" }
func (instantStreamProvider) SupportsModel(model string) bool { return true }

func (instantStreamProvider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{}, nil
}

func (instantStreamProvider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}

func (instantStreamProvider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	text := "hi"
	if err := onChunk(gw.ChatResponseChunk{
		Model: "gpt-4o",
		Choices: []gw.ChoiceDelta{{
			Index: 0,
			Delta: gw.Message{Content: []gw.ContentPart{{Type: "text", Text: &text}}},
		}},
	}); err != nil {
		return err
	}
	return onChunk(gw.ChatResponseChunk{
		Choices: []gw.ChoiceDelta{{Index: 0, FinishReason: "stop"}},
		Usage:   &gw.Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
	})
}

// TTFT is the only latency signal the public catalog has for streaming, and
// the model-metrics view counts a sample purely by the attribute's presence.
// The old `firstChunkLatencyMs > 0` guard threw away every sub-millisecond
// first chunk, so a genuine 0 ms TTFT was recorded as "not a stream at all".
func TestStreamRecordsZeroTTFTWhenFirstChunkIsImmediate(t *testing.T) {
	prevCfg := telemetry.GetGlobalTracingConfig()
	telemetry.SetGlobalTracingConfig(&telemetry.TracingConfig{TraceProviderCalls: true})
	defer telemetry.SetGlobalTracingConfig(prevCfg)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevTP := telemetry.GetGlobalTracerProvider()
	telemetry.SetGlobalTracerProvider(tp)
	defer telemetry.SetGlobalTracerProvider(prevTP)

	mw := NewMiddlewareWithKey(instantStreamProvider{}, nil, "", "", "")
	err := mw.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "gpt-4o"},
		func(gw.ChatResponseChunk) error { return nil })
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	attributes := map[string]any{}
	for _, s := range exporter.GetSpans() {
		for _, a := range s.Attributes {
			attributes[string(a.Key)] = a.Value.AsInterface()
		}
	}

	chunks, ok := attributes["llm.stream.chunk_count"].(int64)
	if !ok || chunks == 0 {
		t.Fatalf("precondition: the stream must have produced chunks, got %v", attributes["llm.stream.chunk_count"])
	}
	if _, ok := attributes["llm.stream.time_to_first_token_ms"]; !ok {
		t.Fatalf("a stream that produced chunks must record TTFT even at 0 ms; attributes: %v", attributes)
	}
}
