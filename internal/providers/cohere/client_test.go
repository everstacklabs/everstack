package cohere

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestChatRequestSerializesReasoningAndConfiguredZero(t *testing.T) {
	t.Parallel()

	zero := 0.0
	budget := 500
	payload, err := json.Marshal(cohereChatReq{
		Model:       "command-a-reasoning-08-2025",
		Temperature: &zero,
		Thinking:    cohereThinkingFor(gw.SamplingParams{ReasoningBudget: &budget}),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, want := range []string{`"temperature":0`, `"thinking":{"token_budget":500}`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("payload = %s, want %s", payload, want)
		}
	}
}

func TestChatParsesCohereV2ContentAndBilledUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chat-test",
			"finish_reason": "COMPLETE",
			"message": {
				"role": "assistant",
				"content": [{"type": "text", "text": "pong"}]
			},
			"usage": {
				"billed_units": {"input_tokens": 7, "output_tokens": 3}
			}
		}`))
	}))
	defer server.Close()

	provider := NewProvider(Config{APIKey: "test", BaseURL: server.URL})
	resp, err := provider.Chat(context.Background(), gw.ChatCompletionRequest{
		Model:    "command-a-03-2025",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("Reply with exactly: pong"))},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if resp.ID != "chat-test" {
		t.Fatalf("expected response id chat-test, got %q", resp.ID)
	}
	if resp.Created.IsZero() {
		t.Fatal("expected response created timestamp to be set")
	}
	if len(resp.Choices) != 1 || len(resp.Choices[0].Message.Content) != 1 || resp.Choices[0].Message.Content[0].Text == nil {
		t.Fatalf("expected one text content part, got %+v", resp.Choices)
	}
	if got := *resp.Choices[0].Message.Content[0].Text; got != "pong" {
		t.Fatalf("expected content pong, got %q", got)
	}
	if got := resp.Choices[0].FinishReason; got != "stop" {
		t.Fatalf("expected finish reason stop, got %q", got)
	}
	if resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 3 || resp.Usage.TotalTokens != 10 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestChatRejectsEmptyCohereResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chat-empty",
			"message": {"role": "assistant", "content": []}
		}`))
	}))
	defer server.Close()

	provider := NewProvider(Config{APIKey: "test", BaseURL: server.URL})
	_, err := provider.Chat(context.Background(), gw.ChatCompletionRequest{
		Model:    "command-a-03-2025",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("Reply with exactly: pong"))},
	})
	if err == nil {
		t.Fatal("expected empty Cohere response to fail")
	}
	if !strings.Contains(err.Error(), "no assistant content") {
		t.Fatalf("unexpected error: %v", err)
	}
}
