package runtime

import (
	"context"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

type modelCapturingProvider struct {
	request gw.ChatCompletionRequest
}

func (*modelCapturingProvider) Name() string { return "openai" }

func (*modelCapturingProvider) SupportsModel(model string) bool {
	return model == "gpt-5.5"
}

func (p *modelCapturingProvider) Chat(_ context.Context, request gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.request = request
	text := "done"
	return gw.ChatCompletionResponse{
		Model: "gpt-5.5",
		Choices: []gw.Choice{{
			Message: gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: &text}},
			},
			FinishReason: "stop",
		}},
	}, nil
}

func (*modelCapturingProvider) ChatStream(context.Context, gw.ChatCompletionRequest, func(gw.ChatResponseChunk) error) error {
	return nil
}

func (*modelCapturingProvider) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}

func TestSamplingParamsFromConfigPreservesNumericOverrides(t *testing.T) {
	sampling := samplingParamsFromConfig(map[string]interface{}{
		"temperature": float32(0.25),
		"max_tokens": int64(512),
		"top_p":      float64(0.75),
	})

	if sampling.MaxTokens != 512 {
		t.Fatalf("MaxTokens = %d, want 512", sampling.MaxTokens)
	}
	if sampling.Temperature != 0.25 {
		t.Fatalf("Temperature = %v, want 0.25", sampling.Temperature)
	}
	if sampling.TopP != 0.75 {
		t.Fatalf("TopP = %v, want 0.75", sampling.TopP)
	}
}

func TestLoopSendsResolvedProviderModel(t *testing.T) {
	provider := &modelCapturingProvider{}
	registry := gw.NewRegistry()
	registry.Register(provider)
	engine := NewEngine(registry, gw.NewRouter(registry, nil), nil)
	loop := NewLoop(engine, nil, NewEmitter(), LoopConfig{MaxIterations: 1})

	_, err := loop.Run(context.Background(), &LoopState{TurnNumber: 1}, &LoopInput{
		SessionID: "session-1",
		Model:     "@openai/gpt-5.5",
		UserInput: "hello",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if provider.request.Model != "gpt-5.5" {
		t.Fatalf("provider request model = %q, want gpt-5.5", provider.request.Model)
	}
}

func TestEngineSendsResolvedProviderModel(t *testing.T) {
	provider := &modelCapturingProvider{}
	registry := gw.NewRegistry()
	registry.Register(provider)
	engine := NewEngine(registry, gw.NewRouter(registry, nil), nil)

	result, err := engine.ExecuteTurn(context.Background(), &TurnInput{
		SessionID: "session-1",
		Model:     "@openai/gpt-5.5",
		UserInput: "hello",
	})
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if result.Error != "" {
		t.Fatalf("ExecuteTurn() result error = %q", result.Error)
	}
	if provider.request.Model != "gpt-5.5" {
		t.Fatalf("provider request model = %q, want gpt-5.5", provider.request.Model)
	}
}
