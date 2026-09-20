package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/anthropic"
)

func TestAnthropicThinkingStreamPreservesNativeBlockAndVisibleText(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("request path = %q, want %q", r.URL.Path, "/v1/messages")
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_stream","model":"claude-fable-5-1","usage":{"input_tokens":10}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private reasoning"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-stream"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"final "}}`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
			`data: {"type":"content_block_stop","index":1}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	provider := anthropic.NewProvider(anthropic.Config{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	engine := NewEngine(nil, nil, nil)
	var streamed strings.Builder

	response, err := engine.ExecuteTurnStream(
		context.Background(),
		provider,
		gw.ChatCompletionRequest{
			Model: "claude-fable-5-1",
			Messages: []gw.Message{
				gw.NewMessage(gw.RoleUser, gw.Text("reason carefully")),
			},
			Sampling: gw.SamplingParams{ReasoningEffort: "high"},
		},
		func(delta string) error {
			streamed.WriteString(delta)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ExecuteTurnStream() error = %v", err)
	}
	if got := streamed.String(); got != "final answer" {
		t.Fatalf("streamed text = %q, want %q", got, "final answer")
	}
	if len(response.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(response.Choices))
	}
	message := response.Choices[0].Message
	if got := messageText(message); got != "final answer" {
		t.Fatalf("accumulated text = %q, want %q", got, "final answer")
	}
	if len(message.Content) != 3 {
		t.Fatalf("content parts = %d, want thinking plus two text deltas", len(message.Content))
	}
	if message.Content[0].ProviderJSON == nil {
		t.Fatal("thinking block was not preserved for replay")
	}
	var thinking struct {
		Type      string `json:"type"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(*message.Content[0].ProviderJSON, &thinking); err != nil {
		t.Fatalf("unmarshal thinking block: %v", err)
	}
	if thinking.Type != "thinking" || thinking.Thinking != "private reasoning" || thinking.Signature != "signed-stream" {
		t.Fatalf("thinking block = %#v", thinking)
	}
}
