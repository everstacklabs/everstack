package mistral

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestChatRequestSerializesReasoningEffortAndConfiguredZero(t *testing.T) {
	t.Parallel()

	zero := 0.0
	payload, err := json.Marshal(mChatReq{
		Model:           "mistral-small-latest",
		Temperature:     &zero,
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, want := range []string{`"temperature":0`, `"reasoning_effort":"high"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("payload = %s, want %s", payload, want)
		}
	}
}

func TestReasoningResponseReplaysNativeChunksOnNextTurn(t *testing.T) {
	t.Parallel()

	nativeContent := json.RawMessage(`[
		{"type":"thinking","thinking":[{"type":"text","text":"private trace"}]},
		{"type":"text","text":"final answer"}
	]`)
	var requestBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		requestBodies = append(requestBodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-mistral",
			"model":"mistral-small-latest",
			"choices":[{"message":{"content":` + string(nativeContent) + `}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	provider := NewProvider(Config{APIKey: "test", BaseURL: server.URL})
	first, err := provider.Chat(context.Background(), gw.ChatCompletionRequest{
		Model: "mistral-small-latest",
		Messages: []gw.Message{
			gw.NewMessage(gw.RoleUser, gw.Text("first question")),
		},
		Sampling: gw.SamplingParams{ReasoningEffort: "high"},
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if len(first.Choices) != 1 ||
		mistralFinalText(mistralOutboundContent(first.Choices[0].Message.Content)) != "final answer" {
		t.Fatalf("first response did not retain display text and native chunks: %#v", first.Choices)
	}

	_, err = provider.Chat(context.Background(), gw.ChatCompletionRequest{
		Model: "mistral-small-latest",
		Messages: []gw.Message{
			gw.NewMessage(gw.RoleUser, gw.Text("first question")),
			first.Choices[0].Message,
			gw.NewMessage(gw.RoleUser, gw.Text("follow-up")),
		},
		Sampling: gw.SamplingParams{ReasoningEffort: "high"},
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(requestBodies))
	}

	var replay struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(requestBodies[1], &replay); err != nil {
		t.Fatalf("second request decode error = %v", err)
	}
	if len(replay.Messages) != 3 {
		t.Fatalf("second request messages = %d, want 3", len(replay.Messages))
	}
	var gotContent, wantContent any
	if err := json.Unmarshal(replay.Messages[1].Content, &gotContent); err != nil {
		t.Fatalf("replayed content decode error = %v", err)
	}
	if err := json.Unmarshal(nativeContent, &wantContent); err != nil {
		t.Fatalf("native content decode error = %v", err)
	}
	if !reflect.DeepEqual(gotContent, wantContent) {
		t.Fatalf("replayed content = %s, want %s", replay.Messages[1].Content, nativeContent)
	}
}

func TestMistralFinalTextHandlesStringAndReasoningChunks(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		content string
		want    string
	}{
		"plain": {
			content: `"answer"`,
			want:    "answer",
		},
		"reasoning chunks": {
			content: `[
				{"type":"thinking","thinking":[{"type":"text","text":"private trace"}]},
				{"type":"text","text":"final "},
				{"type":"text","text":"answer"}
			]`,
			want: "final answer",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mistralFinalText(json.RawMessage(test.content)); got != test.want {
				t.Fatalf("mistralFinalText() = %q, want %q", got, test.want)
			}
		})
	}
}
