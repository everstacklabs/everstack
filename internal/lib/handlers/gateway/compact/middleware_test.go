package compact

import (
	"context"
	"errors"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// fakeProvider is a stub gw.Provider implementation that records the
// last request it saw so tests can assert on what the middleware
// forwarded.
type fakeProvider struct {
	name        string
	lastRequest gw.ChatCompletionRequest
	chatErr     error
	chatResp    gw.ChatCompletionResponse
}

func (f *fakeProvider) Name() string                   { return f.name }
func (f *fakeProvider) SupportsModel(_ string) bool    { return true }

func (f *fakeProvider) Chat(_ context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	f.lastRequest = req
	return f.chatResp, f.chatErr
}

func (f *fakeProvider) ChatStream(_ context.Context, req gw.ChatCompletionRequest, _ func(gw.ChatResponseChunk) error) error {
	f.lastRequest = req
	return nil
}

func (f *fakeProvider) Embed(_ context.Context, _ gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, gw.ErrNotSupported{Operation: "embed", Provider: f.name}
}

// fakeSummariserProvider returns a canned summary string so tests
// don't need a real LLM call.
type fakeSummariserProvider struct {
	output string
	err    error
}

func (f *fakeSummariserProvider) Chat(_ context.Context, _ gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	if f.err != nil {
		return gw.ChatCompletionResponse{}, f.err
	}
	return gw.ChatCompletionResponse{
		Choices: []gw.Choice{
			{
				Message: gw.Message{
					Role: gw.RoleAssistant,
					Content: []gw.ContentPart{{
						Type: "text",
						Text: strPtrTest(f.output),
					}},
				},
			},
		},
	}, nil
}

func (f *fakeSummariserProvider) ChatStream(_ context.Context, _ gw.ChatCompletionRequest, _ func(gw.ChatResponseChunk) error) error {
	return errors.New("not implemented")
}

func makeFakeResolver(s *fakeSummariserProvider) SummarizerResolver {
	return func(_ context.Context, _ string) (gw.ChatProvider, error) {
		return s, nil
	}
}

func TestMiddleware_Disabled_PassesThrough(t *testing.T) {
	cfg := DefaultConfig() // Enabled=false
	inner := &fakeProvider{name: "anthropic"}
	mw := NewMiddleware(inner, "anthropic", cfg, nil)

	req := gw.ChatCompletionRequest{Messages: makeChat(50)}
	originalLen := len(req.Messages)
	_, _ = mw.Chat(context.Background(), req)

	if len(inner.lastRequest.Messages) != originalLen {
		t.Fatalf("disabled middleware should pass through messages unchanged: got %d, want %d", len(inner.lastRequest.Messages), originalLen)
	}
}

func TestMiddleware_NotInProviderAllowList_PassesThrough(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 100 // tiny — anything would otherwise trigger
	cfg.EnabledForProviders = []string{"anthropic"}
	inner := &fakeProvider{name: "google"}
	mw := NewMiddleware(inner, "google", cfg, nil)

	req := gw.ChatCompletionRequest{Messages: makeChat(50)}
	original := len(req.Messages)
	_, _ = mw.Chat(context.Background(), req)
	if len(inner.lastRequest.Messages) != original {
		t.Fatalf("non-allowed provider: messages should be unchanged")
	}
}

func TestMiddleware_PerCallOptOut_PassesThrough(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 100
	inner := &fakeProvider{name: "anthropic"}
	mw := NewMiddleware(inner, "anthropic", cfg, nil)

	req := gw.ChatCompletionRequest{
		Messages: makeChat(50),
		Metadata: map[string]interface{}{"compact": "off"},
	}
	original := len(req.Messages)
	_, _ = mw.Chat(context.Background(), req)
	if len(inner.lastRequest.Messages) != original {
		t.Fatalf("metadata compact=off: messages should be unchanged")
	}
}

func TestMiddleware_Emergency_TruncatesWithoutSummariser(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	// Force emergency on a moderate transcript by keeping max tiny.
	cfg.MaxContextTokens = 200
	inner := &fakeProvider{name: "anthropic"}
	// No resolver — emergency tier doesn't need one.
	mw := NewMiddleware(inner, "anthropic", cfg, nil)

	req := gw.ChatCompletionRequest{Messages: makeChat(50)}
	originalLen := len(req.Messages)
	_, _ = mw.Chat(context.Background(), req)

	if len(inner.lastRequest.Messages) >= originalLen {
		t.Fatalf("emergency tier should reduce messages: got %d, want < %d", len(inner.lastRequest.Messages), originalLen)
	}

	// Look for the truncate sentinel in the spliced position.
	found := false
	for _, m := range inner.lastRequest.Messages {
		for _, p := range m.Content {
			if p.Text != nil && strings.Contains(*p.Text, "Context compacted") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected truncate sentinel in forwarded messages")
	}
}

func TestMiddleware_Background_SummarisesWhenResolverPresent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	// Set MaxContextTokens so the makeChat(50) transcript lands in the
	// background tier (≥0.80, <0.85). 51 messages × 76 chars ÷ 4 = 969
	// tokens; with max=1200 that's ratio=0.8075.
	cfg.MaxContextTokens = 1200
	inner := &fakeProvider{name: "anthropic"}
	summ := &fakeSummariserProvider{output: "summary text goes here"}
	mw := NewMiddleware(inner, "anthropic", cfg, makeFakeResolver(summ))

	msgs := makeChat(50)
	for i := range msgs {
		text := strings.Repeat("x", 60)
		msgs[i].Content = []gw.ContentPart{{Type: "text", Text: strPtrTest(text)}}
	}
	req := gw.ChatCompletionRequest{Messages: msgs}
	originalLen := len(req.Messages)

	_, _ = mw.Chat(context.Background(), req)

	if len(inner.lastRequest.Messages) >= originalLen {
		t.Fatalf("summarise tier should reduce messages: got %d, want < %d", len(inner.lastRequest.Messages), originalLen)
	}

	found := false
	for _, m := range inner.lastRequest.Messages {
		for _, p := range m.Content {
			if p.Text != nil && strings.Contains(*p.Text, "Context summary") &&
				strings.Contains(*p.Text, "summary text goes here") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected summary sentinel with summariser output in forwarded messages")
	}
}

func TestMiddleware_SummariserError_PassesThroughOriginal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	// Same sizing as the success test — background tier.
	cfg.MaxContextTokens = 1200
	inner := &fakeProvider{name: "anthropic"}
	summ := &fakeSummariserProvider{err: errors.New("upstream 503")}
	mw := NewMiddleware(inner, "anthropic", cfg, makeFakeResolver(summ))

	msgs := makeChat(50)
	for i := range msgs {
		msgs[i].Content = []gw.ContentPart{{Type: "text", Text: strPtrTest(strings.Repeat("x", 60))}}
	}
	req := gw.ChatCompletionRequest{Messages: msgs}
	original := len(req.Messages)

	_, _ = mw.Chat(context.Background(), req)

	if len(inner.lastRequest.Messages) != original {
		t.Fatalf("summariser error should fall through with original messages: got %d, want %d", len(inner.lastRequest.Messages), original)
	}
}

func TestMiddleware_StreamingPathAppliesCompaction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 200
	inner := &fakeProvider{name: "anthropic"}
	mw := NewMiddleware(inner, "anthropic", cfg, nil)

	req := gw.ChatCompletionRequest{Messages: makeChat(50)}
	originalLen := len(req.Messages)
	_ = mw.ChatStream(context.Background(), req, func(_ gw.ChatResponseChunk) error { return nil })

	if len(inner.lastRequest.Messages) >= originalLen {
		t.Fatalf("streaming should also compact: got %d, want < %d", len(inner.lastRequest.Messages), originalLen)
	}
}

func TestMiddleware_EmptyMessages_NoOp(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.MaxContextTokens = 100
	inner := &fakeProvider{name: "anthropic"}
	mw := NewMiddleware(inner, "anthropic", cfg, nil)

	req := gw.ChatCompletionRequest{}
	_, _ = mw.Chat(context.Background(), req)
	if len(inner.lastRequest.Messages) != 0 {
		t.Fatalf("empty input should pass through empty: got %d", len(inner.lastRequest.Messages))
	}
}
