package azure_openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/modelparams"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

func embeddedCatalog(t *testing.T) *catalog.Cache {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "cmd", "config", "gateway", "defaults")
	modelsYAML, err := os.ReadFile(filepath.Join(dir, "models.yaml"))
	if err != nil {
		t.Fatalf("read embedded models.yaml: %v", err)
	}
	providersYAML, err := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	if err != nil {
		t.Fatalf("read embedded providers.yaml: %v", err)
	}
	cache := catalog.NewCache()
	if err := cache.Load(modelsYAML, providersYAML); err != nil {
		t.Fatalf("load embedded catalog: %v", err)
	}
	return cache
}

// End to end over the real catalog entries: the factory's lookup reads
// api_type from the azure-openai entry, the parameter filter clears what the
// model rejects, and the request reaches the v1 Responses path without it.
func TestEmbeddedCatalogRoutesAzureGPT6ToResponses(t *testing.T) {
	cache := embeddedCatalog(t)

	var seen seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(t, &seen, r)
		fmt.Fprint(w, `{"id":"resp_1","created_at":1700000000,"model":"gpt-6.2-sol","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer ts.Close()

	p := NewProvider(Config{
		APIKey:          "secret",
		BaseURL:         ts.URL,
		HTTPClient:      ts.Client(),
		APITypeForModel: catalogAPITypeLookup(cache),
	})

	req := gw.ChatCompletionRequest{Model: "gpt-6.2-sol", Messages: userMessage("hi"), Sampling: gw.SamplingParams{Temperature: 0.7, TopP: 0.9}}
	modelparams.DropUnsupported(cache, p.Name(), &req)
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	assertAzureResponsesRequest(t, seen, "gpt-6.2-sol")
	for _, key := range []string{"temperature", "top_p"} {
		if value, ok := seen.body[key]; ok {
			t.Errorf("body carries %s = %v, which GPT-6.2 rejects", key, value)
		}
	}

	// A model whose Azure entry has no api_type keeps the deployments path,
	// even though the first-party entry of a GPT-6 model says "responses".
	lookup := catalogAPITypeLookup(cache)
	if got := lookup("gpt-4o"); got != "" {
		t.Errorf("api_type for azure gpt-4o = %q, want empty", got)
	}
	if got := lookup("gpt-6-astra"); got != "" {
		t.Errorf("api_type for a model with no Azure entry = %q, want empty", got)
	}
	if catalogAPITypeLookup(nil) != nil {
		t.Error("catalogAPITypeLookup(nil) should be nil so routing falls back to the deployments path")
	}
}

func TestClaudeModelStreamsFromFoundryMessagesPath(t *testing.T) {
	var seen seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(t, &seen, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_s","model":"claude-haiku-5-5","usage":{"input_tokens":7}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n\n")))
	}))
	defer ts.Close()

	p := NewProvider(Config{
		APIKey:           "secret",
		BaseURL:          "https://example.openai.azure.com",
		AnthropicBaseURL: ts.URL + "/anthropic",
	})

	var text strings.Builder
	err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "claude-haiku-5-5", Stream: true, Messages: userMessage("hi"), Sampling: gw.SamplingParams{MaxTokens: 64}}, func(chunk gw.ChatResponseChunk) error {
		for _, choice := range chunk.Choices {
			for _, part := range choice.Delta.Content {
				if part.Text != nil {
					text.WriteString(*part.Text)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if seen.path != "/anthropic/v1/messages" {
		t.Fatalf("path = %q, want /anthropic/v1/messages", seen.path)
	}
	if got := seen.header.Get("x-api-key"); got != "secret" {
		t.Fatalf("x-api-key = %q, want the resource key", got)
	}
	if seen.body["stream"] != true {
		t.Fatalf("body stream = %v, want true", seen.body["stream"])
	}
	if text.String() != "Hello world" {
		t.Fatalf("streamed text = %q, want %q", text.String(), "Hello world")
	}
}
