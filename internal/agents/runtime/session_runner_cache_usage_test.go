package runtime

import (
	"context"
	"sync"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

type cacheUsageProvider struct {
	model   string
	details gw.TokenDetails
}

func (cacheUsageProvider) Name() string                      { return "openai" }
func (p cacheUsageProvider) SupportsModel(model string) bool { return model == p.model }
func (cacheUsageProvider) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}
func (cacheUsageProvider) ChatStream(context.Context, gw.ChatCompletionRequest, func(gw.ChatResponseChunk) error) error {
	return nil
}
func (p cacheUsageProvider) Chat(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{
		Model: p.model,
		Choices: []gw.Choice{{
			Message:      gw.NewMessage(gw.RoleAssistant, gw.Text("done")),
			FinishReason: "stop",
		}},
		Usage: gw.Usage{
			PromptTokens:     100,
			CompletionTokens: 10,
			TotalTokens:      110,
			PromptDetails:    &p.details,
		},
	}, nil
}

func TestSessionEndCarriesCumulativeCacheBuckets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		model     string
		details   gw.TokenDetails
		wantRead  int
		wantWrite int
	}{
		{
			name:  "explicit read and write buckets",
			model: "gpt-cache-explicit",
			details: gw.TokenDetails{
				CachedTokens:     50,
				CacheReadTokens:  30,
				CacheWriteTokens: 20,
			},
			wantRead: 30, wantWrite: 20,
		},
		{
			name:      "legacy aggregate falls back to cache reads",
			model:     "gpt-cache-legacy",
			details:   gw.TokenDetails{CachedTokens: 50},
			wantRead:  50,
			wantWrite: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			registry := gw.NewRegistry()
			registry.Register(cacheUsageProvider{model: tc.model, details: tc.details})
			engine := NewEngine(registry, gw.NewRouter(registry, nil), nil)
			emitter := NewEmitter()
			var mu sync.Mutex
			var events []Event
			emitter.AddSink(EventSinkFunc(func(event Event) error {
				mu.Lock()
				events = append(events, event)
				mu.Unlock()
				return nil
			}))

			loopConfig := DefaultLoopConfig()
			loopConfig.EnableStreaming = false
			loopConfig.MaxIterations = 1
			runner := NewSessionRunner(
				"session-cache",
				"agent-cache",
				"tenant-cache",
				NewLoop(engine, nil, emitter, loopConfig),
				emitter,
				nil,
				SessionRunnerConfig{LoopConfig: loopConfig},
			)
			runner.Start(context.Background(), &LoopInput{
				SessionID: "session-cache",
				AgentID:   "agent-cache",
				TenantID:  "tenant-cache",
				Model:     "@openai/" + tc.model,
				UserInput: "hello",
			})
			runner.Wait()

			mu.Lock()
			defer mu.Unlock()
			for _, event := range events {
				if event.Type != EventSessionEnd {
					continue
				}
				if event.Usage == nil {
					t.Fatal("session end usage is nil")
				}
				if event.Usage.PromptTokens != 100 || event.Usage.CacheReadTokens != tc.wantRead || event.Usage.CacheWriteTokens != tc.wantWrite {
					t.Fatalf("session end usage = %+v, want prompt/read/write 100/%d/%d", event.Usage, tc.wantRead, tc.wantWrite)
				}
				return
			}
			t.Fatalf("session end event not emitted: %+v", events)
		})
	}
}
