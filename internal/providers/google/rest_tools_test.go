package google

import (
	"context"
	"encoding/json"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"io"
	"net/http"
	"strings"
	"testing"
)

type toolsTransport func(*http.Request) (*http.Response, error)

func (f toolsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRESTToolStreamingTransport(t *testing.T) {
	var wire map[string]interface{}
	cli := &http.Client{Transport: toolsTransport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{}}},{"functionCall":{"name":"f","args":{"x":1}}}]}}]}

`))}, nil
	})}
	p := NewProvider(Config{BaseURL: "http://upstream.invalid", APIKey: "test"})
	p.client = cli
	noParallel := false
	req := gw.ChatCompletionRequest{Model: "gpt-4o", Sampling: gw.SamplingParams{MaxTokens: 32}, Tools: []gw.ToolDefinition{{Type: "function", Function: gw.ToolFunctionDef{Name: "f", Parameters: map[string]interface{}{"type": "object"}}}}, ToolChoice: "required", ParallelToolCalls: &noParallel, Messages: []gw.Message{{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{{ID: "old", Type: "function", Function: gw.ToolCallFunction{Name: "f", Arguments: "{}"}}}}, {Role: gw.RoleTool, ToolCallID: "old", Content: []gw.ContentPart{gw.Text("ok")}}}}
	var calls []gw.ToolCall
	err := gw.StreamToolAttempt(func(on func(gw.ChatResponseChunk) error) error { return p.ChatStream(context.Background(), req, on) }, func(c gw.ChatResponseChunk) error {
		for _, d := range c.Choices {
			calls = append(calls, d.Delta.ToolCalls...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Function.Name != "f" || calls[0].Function.Arguments != "{}" || calls[1].Function.Arguments != `{"x":1}` || calls[0].ID == calls[1].ID {
		t.Fatalf("calls=%+v", calls)
	}
	if wire["toolConfig"].(map[string]interface{})["functionCallingConfig"].(map[string]interface{})["mode"] != "ANY" || len(wire["tools"].([]interface{})) != 1 {
		t.Fatal(wire)
	}
}

func TestGeminiToolChoice(t *testing.T) {
	for _, mode := range []string{"auto", "none", "required"} {
		if geminiToolConfig(mode) == nil {
			t.Fatal(mode)
		}
	}
	config := geminiToolConfig(map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "f"}})
	if config["functionCallingConfig"].(map[string]interface{})["allowedFunctionNames"].([]string)[0] != "f" {
		t.Fatal(config)
	}
}
