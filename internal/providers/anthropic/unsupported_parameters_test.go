package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// constrainedCatalog describes one model that rejects every filterable
// control and one model that declares nothing, mirroring how Claude 5.x
// entries are expected to be written.
func constrainedCatalog(t *testing.T) *catalog.Cache {
	t.Helper()
	models := []byte(`providers:
  anthropic:
    name: anthropic
    models:
      - name: claude-fable-5-2
        unsupported_parameters: [temperature, top_p, top_k, thinking_toggle, thinking_budget]
      - name: claude-opus-4-8
`)
	providers := []byte("providers:\n  anthropic:\n    name: anthropic\n")
	cache := catalog.NewCache()
	if err := cache.Load(models, providers); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cache
}

func samplingWithEveryControl() gw.SamplingParams {
	topK := 40
	budget := 2048
	enabled := false
	return gw.SamplingParams{
		Temperature:           0.7,
		TemperatureConfigured: true,
		TopP:                  0.9,
		TopPConfigured:        true,
		TopK:                  &topK,
		ReasoningBudget:       &budget,
		ReasoningEnabled:      &enabled,
		ReasoningEffort:       "high",
		MaxTokens:             256,
	}
}

// captureClaudeBody serves one Anthropic Messages call (unary or streaming)
// and records the decoded upstream body.
func captureClaudeBody(t *testing.T, stream bool, got *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(strings.Join([]string{
				`data: {"type":"message_start","message":{"id":"msg_s","model":"m","usage":{"input_tokens":1}}}`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
				`data: {"type":"message_stop"}`,
				"",
			}, "\n")))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"m",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
}

// wrappedProvider builds the adapter behind the same tracing middleware every
// factory wraps it in, which is where unsupported parameters are dropped.
func wrappedProvider(server *httptest.Server, cache *catalog.Cache) gw.Provider {
	p := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	p.client = server.Client()
	return tracing.NewMiddlewareWithKey(p, cache, "", "", "")
}

func TestChatDropsCatalogUnsupportedParameters(t *testing.T) {
	cache := constrainedCatalog(t)
	for _, stream := range []bool{false, true} {
		var body map[string]interface{}
		server := captureClaudeBody(t, stream, &body)
		provider := wrappedProvider(server, cache)
		req := gw.ChatCompletionRequest{
			Model:    "claude-fable-5-2",
			Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hi")}}},
			Sampling: samplingWithEveryControl(),
		}
		var err error
		if stream {
			req.Stream = true
			err = provider.ChatStream(context.Background(), req, func(gw.ChatResponseChunk) error { return nil })
		} else {
			_, err = provider.Chat(context.Background(), req)
		}
		server.Close()
		if err != nil {
			t.Fatalf("stream=%v: call error = %v", stream, err)
		}
		for _, field := range []string{"temperature", "top_p", "top_k", "thinking"} {
			if _, present := body[field]; present {
				t.Errorf("stream=%v: upstream body still has %q: %v", stream, field, body)
			}
		}
		// reasoning_effort is not filtered: it changes meaning, so it must
		// still reach the provider.
		if config, _ := body["output_config"].(map[string]interface{}); config["effort"] != "high" {
			t.Errorf("stream=%v: output_config = %v, want effort kept", stream, body["output_config"])
		}
		if body["max_tokens"] != float64(256) {
			t.Errorf("stream=%v: max_tokens = %v, want 256", stream, body["max_tokens"])
		}
	}
}

func TestChatKeepsParametersWhenCatalogDeclaresNone(t *testing.T) {
	cache := constrainedCatalog(t)
	var body map[string]interface{}
	server := captureClaudeBody(t, false, &body)
	defer server.Close()

	req := gw.ChatCompletionRequest{
		Model:    "claude-opus-4-8",
		Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hi")}}},
		Sampling: samplingWithEveryControl(),
	}
	if _, err := wrappedProvider(server, cache).Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// The body must be exactly what the adapter produced before this change.
	want, err := (&Provider{apiKey: "test-key"}).toClaude(req)
	if err != nil {
		t.Fatalf("toClaude() error = %v", err)
	}
	encoded, _ := json.Marshal(want)
	var wantBody map[string]interface{}
	_ = json.Unmarshal(encoded, &wantBody)
	gotJSON, _ := json.Marshal(body)
	wantJSON, _ := json.Marshal(wantBody)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("upstream body changed:\n got  %s\n want %s", gotJSON, wantJSON)
	}
	for _, field := range []string{"temperature", "top_p", "top_k", "thinking"} {
		if _, present := body[field]; !present {
			t.Errorf("upstream body lost %q: %v", field, body)
		}
	}
}
