package azure_openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

type seenRequest struct {
	path   string
	query  string
	header http.Header
	body   map[string]interface{}
}

func capture(t *testing.T, seen *seenRequest, r *http.Request) {
	t.Helper()
	seen.path = r.URL.Path
	seen.query = r.URL.RawQuery
	seen.header = r.Header.Clone()
	seen.body = map[string]interface{}{}
	if err := json.NewDecoder(r.Body).Decode(&seen.body); err != nil {
		t.Errorf("decode body: %v", err)
	}
}

func apiTypes(types map[string]string) func(string) string {
	return func(model string) string { return types[strings.ToLower(model)] }
}

func responseText(resp gw.ChatCompletionResponse) string {
	var text strings.Builder
	for _, choice := range resp.Choices {
		for _, part := range choice.Message.Content {
			if part.Text != nil {
				text.WriteString(*part.Text)
			}
		}
	}
	return text.String()
}

func userMessage(text string) []gw.Message {
	return []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: &text}}}}
}

// assertAzureResponsesRequest pins the v1 Responses wire shape: the v1 path
// with no api-version, the resource key in api-key and no bearer token.
func assertAzureResponsesRequest(t *testing.T, seen seenRequest, model string) {
	t.Helper()
	if seen.path != "/openai/v1/responses" {
		t.Fatalf("path = %q, want /openai/v1/responses", seen.path)
	}
	if seen.query != "" {
		t.Fatalf("query = %q, want none (the v1 API takes no api-version)", seen.query)
	}
	if got := seen.header.Get("api-key"); got != "secret" {
		t.Fatalf("api-key = %q, want the resource key", got)
	}
	if got := seen.header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want unset for key auth", got)
	}
	if got := seen.body["model"]; got != model {
		t.Fatalf("body model = %v, want the deployment name %q", got, model)
	}
	if _, ok := seen.body["input"]; !ok {
		t.Fatalf("body has no Responses input: %v", seen.body)
	}
	if _, ok := seen.body["messages"]; ok {
		t.Fatalf("body carries chat-completions messages: %v", seen.body)
	}
}

func TestResponsesModelUsesV1ResponsesPath(t *testing.T) {
	var seen seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(t, &seen, r)
		fmt.Fprint(w, `{"id":"resp_1","created_at":1700000000,"model":"gpt-6.2-sol","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello back"}]}],"usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12}}`)
	}))
	defer ts.Close()

	p := NewProvider(Config{
		APIKey:          "secret",
		BaseURL:         ts.URL,
		HTTPClient:      ts.Client(),
		APITypeForModel: apiTypes(map[string]string{"gpt-6.2-sol": "responses"}),
	})

	resp, err := p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "gpt-6.2-sol", Messages: userMessage("hi")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	assertAzureResponsesRequest(t, seen, "gpt-6.2-sol")
	if got := responseText(resp); got != "hello back" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Usage.PromptTokens != 9 || resp.Usage.CompletionTokens != 3 {
		t.Fatalf("usage = %+v, want 9 in / 3 out", resp.Usage)
	}
}

func TestResponsesModelStreamsFromV1ResponsesPath(t *testing.T) {
	var seen seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(t, &seen, r)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"Hello \"}\n\n")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"world\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s\",\"model\":\"gpt-6.2-sol\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n\n")
	}))
	defer ts.Close()

	p := NewProvider(Config{
		APIKey:          "secret",
		BaseURL:         ts.URL,
		HTTPClient:      ts.Client(),
		APITypeForModel: apiTypes(map[string]string{"gpt-6.2-sol": "responses"}),
	})

	var text strings.Builder
	err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "gpt-6.2-sol", Stream: true, Messages: userMessage("hi")}, func(chunk gw.ChatResponseChunk) error {
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
	assertAzureResponsesRequest(t, seen, "gpt-6.2-sol")
	if seen.body["stream"] != true {
		t.Fatalf("body stream = %v, want true", seen.body["stream"])
	}
	if text.String() != "Hello world" {
		t.Fatalf("streamed text = %q, want %q", text.String(), "Hello world")
	}
}

// Azure reports a failure that happens after the stream has opened as an
// error event on an HTTP 200 response. It must surface as an error, not as a
// short but successful completion.
func TestResponsesStreamErrorEventOnHTTP200IsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\n\n")
		fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"code\":\"too_many_requests\",\"message\":\"rate limit reached\"}\n\n")
	}))
	defer ts.Close()

	p := NewProvider(Config{
		APIKey:          "secret",
		BaseURL:         ts.URL,
		HTTPClient:      ts.Client(),
		APITypeForModel: apiTypes(map[string]string{"gpt-6.2-sol": "responses"}),
	})

	err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "gpt-6.2-sol", Stream: true, Messages: userMessage("hi")}, func(gw.ChatResponseChunk) error { return nil })
	if err == nil {
		t.Fatal("ChatStream returned nil for a stream that ended in an error event")
	}
	if !strings.Contains(err.Error(), "rate limit reached") {
		t.Fatalf("error = %v, want the upstream message", err)
	}
}

func TestClaudeModelUsesFoundryMessagesPath(t *testing.T) {
	var seen seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(t, &seen, r)
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"text","text":"hello back"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":3}}`)
	}))
	defer ts.Close()

	p := NewProvider(Config{
		APIKey:           "secret",
		BaseURL:          "https://example.openai.azure.com",
		AnthropicBaseURL: ts.URL + "/anthropic",
	})

	resp, err := p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "claude-haiku-5-5", Messages: userMessage("hi"), Sampling: gw.SamplingParams{MaxTokens: 64}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if seen.path != "/anthropic/v1/messages" {
		t.Fatalf("path = %q, want /anthropic/v1/messages", seen.path)
	}
	if seen.query != "" {
		t.Fatalf("query = %q, want none", seen.query)
	}
	if got := seen.header.Get("x-api-key"); got != "secret" {
		t.Fatalf("x-api-key = %q, want the resource key", got)
	}
	if seen.header.Get("anthropic-version") == "" {
		t.Fatal("anthropic-version header not sent")
	}
	if got := seen.body["model"]; got != "claude-haiku-5-5" {
		t.Fatalf("body model = %v, want the deployment name", got)
	}
	if got := responseText(resp); got != "hello back" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestRouteFor(t *testing.T) {
	p := NewProvider(Config{
		APIKey:  "secret",
		BaseURL: "https://example.openai.azure.com",
		APITypeForModel: apiTypes(map[string]string{
			"gpt-6.2-sol":   "responses",
			"my-claude-dep": "anthropic",
			"gpt-4o":        "",
		}),
	})
	cases := map[string]route{
		"gpt-6.2-sol":      routeResponses,
		"GPT-6.2-Sol":      routeResponses,
		"gpt-4o":           routeDeployments,
		"gpt-5.6-sol":      routeDeployments,
		"unknown-deploy":   routeDeployments,
		"claude-haiku-5-5": routeClaude,
		"Claude-Fable-5-2": routeClaude,
		"my-claude-dep":    routeClaude,
	}
	for model, want := range cases {
		if got := p.routeFor(model); got != want {
			t.Errorf("routeFor(%q) = %d, want %d", model, got, want)
		}
	}

	// Without a catalog every non-Claude model keeps the deployments path.
	bare := NewProvider(Config{APIKey: "secret", BaseURL: "https://example.openai.azure.com"})
	if got := bare.routeFor("gpt-6.2-sol"); got != routeDeployments {
		t.Errorf("routeFor without catalog = %d, want deployments", got)
	}
}

func TestHostedBaseURLs(t *testing.T) {
	cases := []struct {
		base, responses, claude string
	}{
		{"https://acme.openai.azure.com", "https://acme.openai.azure.com/openai/v1", "https://acme.services.ai.azure.com/anthropic"},
		{"https://acme.openai.azure.com/", "https://acme.openai.azure.com/openai/v1", "https://acme.services.ai.azure.com/anthropic"},
		{"https://Acme.OpenAI.Azure.com", "https://Acme.OpenAI.Azure.com/openai/v1", "https://Acme.services.ai.azure.com/anthropic"},
		{"https://acme.services.ai.azure.com", "https://acme.services.ai.azure.com/openai/v1", "https://acme.services.ai.azure.com/anthropic"},
		// Only the hostname suffix is rewritten; a name that merely contains
		// the suffix, or a private host, is left alone.
		{"https://openai.azure.com.internal.example", "https://openai.azure.com.internal.example/openai/v1", "https://openai.azure.com.internal.example/anthropic"},
		{"https://gateway.internal.example:8443", "https://gateway.internal.example:8443/openai/v1", "https://gateway.internal.example:8443/anthropic"},
	}
	for _, tc := range cases {
		p := NewProvider(Config{APIKey: "secret", BaseURL: tc.base})
		if got, err := p.responsesBaseURL(); err != nil || got != tc.responses {
			t.Errorf("responsesBaseURL(%q) = %q, %v; want %q", tc.base, got, err, tc.responses)
		}
		if got, err := p.claudeBaseURL(); err != nil || got != tc.claude {
			t.Errorf("claudeBaseURL(%q) = %q, %v; want %q", tc.base, got, err, tc.claude)
		}
	}

	for _, bad := range []string{"", "https://{resource}.openai.azure.com", "acme.openai.azure.com"} {
		p := NewProvider(Config{APIKey: "secret", BaseURL: bad})
		if _, err := p.responsesBaseURL(); err == nil {
			t.Errorf("responsesBaseURL(%q) = nil error, want one", bad)
		}
		if _, err := p.claudeBaseURL(); err == nil {
			t.Errorf("claudeBaseURL(%q) = nil error, want one", bad)
		}
		if _, err := p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "claude-haiku-5-5", Messages: userMessage("hi")}); err == nil {
			t.Errorf("Chat with base %q = nil error, want one", bad)
		}
	}
}

// An existing chat-completions model must keep the dated deployments URL.
func TestDeploymentModelKeepsDeploymentsPath(t *testing.T) {
	var seen seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(t, &seen, r)
		fmt.Fprint(w, `{"id":"c","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer ts.Close()

	p := NewProvider(Config{
		APIKey:          "secret",
		BaseURL:         ts.URL,
		HTTPClient:      ts.Client(),
		APIVersion:      "2024-10-21",
		APITypeForModel: apiTypes(map[string]string{"gpt-6.2-sol": "responses"}),
	})
	if _, err := p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "gpt-4o", Messages: userMessage("hi")}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if seen.path != "/openai/deployments/gpt-4o/chat/completions" || seen.query != "api-version=2024-10-21" {
		t.Fatalf("request = %s?%s, want the dated deployments URL", seen.path, seen.query)
	}
	if _, ok := seen.body["messages"]; !ok {
		t.Fatalf("body has no chat-completions messages: %v", seen.body)
	}
}
