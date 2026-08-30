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

// fakeStreamProvider streams two text deltas plus a final finish/usage chunk.
type fakeStreamProvider struct {
	chatKeySource   string
	streamKeySource string
}

func (fakeStreamProvider) Name() string                    { return "openai" }
func (fakeStreamProvider) SupportsModel(model string) bool { return true }

func (p fakeStreamProvider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{KeySource: p.chatKeySource}, nil
}

func (fakeStreamProvider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}

func (p fakeStreamProvider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	for _, p := range []string{"Hello", " world"} {
		text := p
		if err := onChunk(gw.ChatResponseChunk{
			Model: "gpt-4o",
			Choices: []gw.ChoiceDelta{{
				Index: 0,
				Delta: gw.Message{Content: []gw.ContentPart{{Type: "text", Text: &text}}},
			}},
		}); err != nil {
			return err
		}
	}
	// Final chunk carries the finish reason and usage, as most providers do.
	return onChunk(gw.ChatResponseChunk{
		Choices: []gw.ChoiceDelta{{Index: 0, FinishReason: "stop"}},
		Usage: &gw.Usage{
			PromptTokens:     5,
			CompletionTokens: 2,
			TotalTokens:      7,
			KeySource:        p.streamKeySource,
			PromptDetails: &gw.TokenDetails{
				CachedTokens:    2,
				CacheReadTokens: 2,
			},
			CompletionDetails: &gw.TokenDetails{
				ReasoningTokens: 1,
				TextTokens:      1,
			},
		},
	})
}

// TestChatStreamCapturesIOPayload guards the fix for blank I/O on streaming
// provider spans: the streaming path must attach the full request messages and
// the assembled response choices, exactly like the non-streaming Chat path.
func TestChatStreamCapturesIOPayload(t *testing.T) {
	prevCfg := telemetry.GetGlobalTracingConfig()
	telemetry.SetGlobalTracingConfig(&telemetry.TracingConfig{TraceProviderCalls: true})
	defer telemetry.SetGlobalTracingConfig(prevCfg)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevTP := telemetry.GetGlobalTracerProvider()
	telemetry.SetGlobalTracerProvider(tp)
	defer telemetry.SetGlobalTracerProvider(prevTP)

	mw := NewMiddlewareWithKey(fakeStreamProvider{}, nil, "key-id", "platform", "config")

	prompt := "What is 2+2?"
	req := gw.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []gw.Message{{
			Role:    gw.RoleUser,
			Content: []gw.ContentPart{{Type: "text", Text: &prompt}},
		}},
	}

	var received strings.Builder
	var finalKeySource string
	err := mw.ChatStream(context.Background(), req, func(chunk gw.ChatResponseChunk) error {
		if chunk.Usage != nil {
			finalKeySource = chunk.Usage.KeySource
		}
		for _, c := range chunk.Choices {
			for _, part := range c.Delta.Content {
				if part.Text != nil {
					received.WriteString(*part.Text)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	// The wrapped callback must still deliver every chunk to the caller.
	if received.String() != "Hello world" {
		t.Fatalf("caller callback got %q, want %q", received.String(), "Hello world")
	}
	if finalKeySource != "config" {
		t.Fatalf("final stream key source = %q, want %q", finalKeySource, "config")
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	// Find the provider span and read its I/O payload attributes.
	var reqMessages, respChoices string
	attributes := map[string]any{}
	var found bool
	for _, s := range spans {
		for _, a := range s.Attributes {
			attributes[string(a.Key)] = a.Value.AsInterface()
			switch string(a.Key) {
			case "llm.request.messages":
				reqMessages = a.Value.AsString()
				found = true
			case "llm.response.choices":
				respChoices = a.Value.AsString()
			}
		}
		if found {
			break
		}
	}

	if !strings.Contains(reqMessages, "What is 2+2?") {
		t.Errorf("llm.request.messages missing prompt; got %q", reqMessages)
	}
	if !strings.Contains(respChoices, "Hello world") {
		t.Errorf("llm.response.choices missing assembled output; got %q", respChoices)
	}
	if !strings.Contains(respChoices, "stop") {
		t.Errorf("llm.response.choices missing finish reason; got %q", respChoices)
	}
	if got := attributes["everstack.traffic.kind"]; got != "customer" {
		t.Errorf("everstack.traffic.kind = %#v, want customer", got)
	}
	if got := attributes["model.publisher"]; got != "openai" {
		t.Errorf("model.publisher = %#v, want openai", got)
	}
	if got := attributes["model.canonical_id"]; got != "openai/gpt-4o" {
		t.Errorf("model.canonical_id = %#v, want openai/gpt-4o", got)
	}
	if got := attributes["llm.tokens.cache_read"]; got != int64(2) {
		t.Errorf("llm.tokens.cache_read = %#v, want 2", got)
	}
	if got := attributes["llm.tokens.reasoning"]; got != int64(1) {
		t.Errorf("llm.tokens.reasoning = %#v, want 1", got)
	}
	if got := attributes["provider.api_key_source"]; got != "config" {
		t.Errorf("provider.api_key_source = %#v, want config", got)
	}
}

func TestKeySourceThreadingWhenProviderTracingDisabled(t *testing.T) {
	prevCfg := telemetry.GetGlobalTracingConfig()
	telemetry.SetGlobalTracingConfig(nil)
	defer telemetry.SetGlobalTracingConfig(prevCfg)

	mw := NewMiddlewareWithKey(fakeStreamProvider{}, nil, "key-id", "tenant", "manual")
	resp, err := mw.Chat(context.Background(), gw.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.KeySource != "manual" {
		t.Errorf("Chat key source = %q, want manual", resp.KeySource)
	}

	var finalKeySource string
	err = mw.ChatStream(context.Background(), gw.ChatCompletionRequest{}, func(chunk gw.ChatResponseChunk) error {
		if chunk.Usage != nil {
			finalKeySource = chunk.Usage.KeySource
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	if finalKeySource != "manual" {
		t.Errorf("final stream key source = %q, want manual", finalKeySource)
	}
}

func TestEmptySelectedKeySourceDoesNotOverwriteProviderSource(t *testing.T) {
	prevCfg := telemetry.GetGlobalTracingConfig()
	telemetry.SetGlobalTracingConfig(nil)
	defer telemetry.SetGlobalTracingConfig(prevCfg)

	provider := fakeStreamProvider{chatKeySource: "manual", streamKeySource: "manual"}
	mw := NewMiddlewareWithKey(provider, nil, "key-id", "tenant", "")
	resp, err := mw.Chat(context.Background(), gw.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.KeySource != "manual" {
		t.Errorf("Chat key source = %q, want preserved manual", resp.KeySource)
	}

	var finalKeySource string
	err = mw.ChatStream(context.Background(), gw.ChatCompletionRequest{}, func(chunk gw.ChatResponseChunk) error {
		if chunk.Usage != nil {
			finalKeySource = chunk.Usage.KeySource
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	if finalKeySource != "manual" {
		t.Errorf("final stream key source = %q, want preserved manual", finalKeySource)
	}
}
