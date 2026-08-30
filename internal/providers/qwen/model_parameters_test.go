package qwen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestApplyQwenSamplingSerializesReasoningAndConfiguredZero(t *testing.T) {
	t.Parallel()

	budget := 512
	request := qwenChatReq{Model: "qwen3.7-plus"}
	applyQwenSampling(&request, gw.SamplingParams{
		Temperature:           0,
		TemperatureConfigured: true,
		TopP:                  0,
		TopPConfigured:        true,
		ReasoningBudget:       &budget,
	})

	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, want := range []string{
		`"temperature":0`,
		`"top_p":0`,
		`"enable_thinking":true`,
		`"thinking_budget":512`,
	} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("payload = %s, want %s", payload, want)
		}
	}
}

func TestChatHandlesModelSpecificSynchronousThinking(t *testing.T) {
	t.Parallel()

	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-qwen",
			"model":"qwen",
			"choices":[{"message":{"content":"ok"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	provider := NewProvider(Config{APIKey: "test", BaseURL: server.URL})
	request := gw.ChatCompletionRequest{
		Model:    "qwen3-32b",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hello"))},
	}
	if _, err := provider.Chat(context.Background(), request); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(bodies) != 1 || !strings.Contains(string(bodies[0]), `"enable_thinking":false`) {
		t.Fatalf("stream-only model payload = %q, want explicit reasoning disable", bodies)
	}

	request.Model = "qwen3.7-plus"
	if _, err := provider.Chat(context.Background(), request); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(bodies) != 2 || strings.Contains(string(bodies[1]), `"enable_thinking"`) {
		t.Fatalf("commercial model payload = %q, want provider default preserved", bodies)
	}

	enabled := true
	request.Model = "qwen3-32b"
	request.Sampling.ReasoningEnabled = &enabled
	if _, err := provider.Chat(context.Background(), request); err == nil ||
		!strings.Contains(err.Error(), "only with streaming") {
		t.Fatalf("Chat() error = %v, want streaming requirement", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("provider received incompatible request; calls = %d, want 2", len(bodies))
	}
}
