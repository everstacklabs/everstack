package openai

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

// fidelityGatewayRequest is a gateway chat request carrying every OpenAI
// chat parameter the gateway threads, in the gateway's own JSON encoding.
const fidelityGatewayRequest = `{
	"model": "gpt-4o",
	"messages": [
		{"role": "system", "content": [{"type": "text", "text": "be brief"}], "name": "ops"},
		{"role": "user", "content": [{"type": "image_url", "image_url": "https://img", "image_detail": "low"}], "name": "alice"}
	],
	"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}}],
	"parallel_tool_calls": false,
	"sampling": {"seed": 7, "logprobs": true, "top_logprobs": 3, "logit_bias": {"50256": -100}},
	"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "description": "d", "schema": {"type": "object"}, "strict": true}},
	"service_tier": "flex"
}`

// captureChatBody runs one unary Chat against a stub upstream and returns the
// JSON body the adapter sent.
func captureChatBody(t *testing.T, req gw.ChatCompletionRequest) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("upstream body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()
	p := NewProvider(Config{BaseURL: srv.URL, APIKey: "sk-test"})
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return body
}

func TestChatSendsOpenAIRequestFieldsUpstream(t *testing.T) {
	var req gw.ChatCompletionRequest
	if err := json.Unmarshal([]byte(fidelityGatewayRequest), &req); err != nil {
		t.Fatal(err)
	}
	body := captureChatBody(t, req)

	var want map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "name": "ops", "content": [{"type": "text", "text": "be brief"}]},
			{"role": "user", "name": "alice", "content": [{"type": "image_url", "image_url": {"url": "https://img", "detail": "low"}}]}
		],
		"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}}],
		"parallel_tool_calls": false,
		"seed": 7,
		"logprobs": true,
		"top_logprobs": 3,
		"logit_bias": {"50256": -100},
		"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "description": "d", "schema": {"type": "object"}, "strict": true}},
		"service_tier": "flex"
	}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, want) {
		got, _ := json.Marshal(body)
		exp, _ := json.Marshal(want)
		t.Fatalf("upstream body mismatch\n got: %s\nwant: %s", got, exp)
	}
}

func TestChatSendsJSONObjectResponseFormat(t *testing.T) {
	var req gw.ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"response_format":{"type":"json_object"}}`), &req); err != nil {
		t.Fatal(err)
	}
	body := captureChatBody(t, req)
	if !reflect.DeepEqual(body["response_format"], map[string]interface{}{"type": "json_object"}) {
		t.Fatalf("response_format = %#v, want json_object", body["response_format"])
	}
}

// OpenAI: max_tokens "is not compatible with o-series models"
// (https://developers.openai.com/api/reference/resources/chat).
func TestMaxTokensBecomesMaxCompletionTokensForOSeries(t *testing.T) {
	cases := map[string]bool{
		"o1": true, "o1-mini": true, "o3": true, "o3-pro": true, "o3-mini": true,
		"o4-mini": true, "openai/o3": true, "gpt-5": true, "gpt-5.1-mini": true,
		"gpt-4o": false, "gpt-4.1": false, "omni-moderation-latest": false,
	}
	for model, wantCompletion := range cases {
		req := toOAChatRequest(gw.ChatCompletionRequest{Model: model, Sampling: gw.SamplingParams{MaxTokens: 100}})
		if wantCompletion && (req.MaxTokens != 0 || req.MaxCompletionTokens != 100) {
			t.Errorf("%s: max_tokens=%d max_completion_tokens=%d, want 0/100", model, req.MaxTokens, req.MaxCompletionTokens)
		}
		if !wantCompletion && (req.MaxTokens != 100 || req.MaxCompletionTokens != 0) {
			t.Errorf("%s: max_tokens=%d max_completion_tokens=%d, want 100/0", model, req.MaxTokens, req.MaxCompletionTokens)
		}
	}
}

// OpenAI documents name on system, user and assistant messages, not on tool
// messages, so a tool message's name is not sent.
func TestChatOmitsNameOnToolMessages(t *testing.T) {
	var req gw.ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-4o","messages":[
		{"role":"assistant","name":"bot","tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","name":"f","tool_call_id":"a","content":[{"type":"text","text":"ok"}]}
	]}`), &req); err != nil {
		t.Fatal(err)
	}
	messages := captureChatBody(t, req)["messages"].([]interface{})
	if name := messages[0].(map[string]interface{})["name"]; name != "bot" {
		t.Errorf("assistant name = %v, want bot", name)
	}
	if name, ok := messages[1].(map[string]interface{})["name"]; ok {
		t.Errorf("tool message carries name %v", name)
	}
}

// Models served through /v1/responses get the same structured output,
// service tier and strict tools, in the Responses API's shapes.
func TestChatViaResponsesSendsStructuredOutput(t *testing.T) {
	var req gw.ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{
		"model": "gpt-5-codex",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}],
		"tools": [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}, "strict": true}}],
		"sampling": {"verbosity": "low"},
		"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "schema": {"type": "object"}, "strict": true}},
		"service_tier": "flex"
	}`), &req); err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r","created_at":1,"model":"gpt-5-codex","status":"completed","output":[]}`))
	}))
	defer srv.Close()
	p := NewProvider(Config{BaseURL: srv.URL, APIKey: "sk-test"})
	_, _ = p.Chat(context.Background(), req)
	if path != "/responses" {
		t.Fatalf("upstream path = %q, want /responses", path)
	}
	wantText := map[string]interface{}{
		"verbosity": "low",
		"format":    map[string]interface{}{"type": "json_schema", "name": "answer", "schema": map[string]interface{}{"type": "object"}, "strict": true},
	}
	if !reflect.DeepEqual(body["text"], wantText) {
		t.Errorf("text = %#v, want %#v", body["text"], wantText)
	}
	if body["service_tier"] != "flex" {
		t.Errorf("service_tier = %v", body["service_tier"])
	}
	tool := body["tools"].([]interface{})[0].(map[string]interface{})
	if tool["strict"] != true {
		t.Errorf("tool = %v, want strict", tool)
	}
}
