package azure_openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestChatSendsOpenAIRequestFieldsUpstream(t *testing.T) {
	var req gw.ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{
		"model": "o4-mini",
		"messages": [{"role": "user", "content": [{"type": "image_url", "image_url": "https://img", "image_detail": "high"}], "name": "alice"}],
		"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}}],
		"parallel_tool_calls": false,
		"sampling": {"max_tokens": 64, "logprobs": true, "top_logprobs": 2, "logit_bias": {"7": 5}},
		"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "schema": {"type": "object"}, "strict": true}}
	}`), &req); err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("upstream body is not JSON: %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"c","created":1,"model":"o4-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()
	p := NewProvider(Config{APIKey: "secret", BaseURL: srv.URL, APIVersion: "2024-10-21"})
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	var want map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"messages": [{"role": "user", "name": "alice", "content": [{"type": "image_url", "image_url": {"url": "https://img", "detail": "high"}}]}],
		"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}}],
		"parallel_tool_calls": false,
		"max_completion_tokens": 64,
		"logprobs": true,
		"top_logprobs": 2,
		"logit_bias": {"7": 5},
		"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "schema": {"type": "object"}, "strict": true}}
	}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, want) {
		got, _ := json.Marshal(body)
		exp, _ := json.Marshal(want)
		t.Fatalf("upstream body mismatch\n got: %s\nwant: %s", got, exp)
	}
}

func TestToAzureChatRequestOmitsNameOnToolMessages(t *testing.T) {
	req := toAzureChatRequest(gw.ChatCompletionRequest{Messages: []gw.Message{
		{Role: gw.RoleUser, Name: "alice"},
		{Role: gw.RoleTool, Name: "f", ToolCallID: "a"},
	}})
	if req.Messages[0].Name != "alice" || req.Messages[1].Name != "" {
		t.Fatalf("names = %q, %q; want alice and none", req.Messages[0].Name, req.Messages[1].Name)
	}
}
