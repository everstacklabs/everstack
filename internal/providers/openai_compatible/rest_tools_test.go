package openai_compatible

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
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"b","type":"function","function":{"name":"f","arguments":"{\"x\":"}},{"index":0,"id":"a","type":"function","function":{"name":"f","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}},{"index":1,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`))}, nil
	})}
	p := NewProvider(Config{BaseURL: "http://upstream.invalid/v1", APIKey: "sk-test"})
	p.client = cli
	p.unaryClient = cli
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
	if wire["parallel_tool_calls"] != false || wire["tool_choice"] != "required" || len(wire["tools"].([]interface{})) != 1 {
		t.Fatal(wire)
	}
	msgs := wire["messages"].([]interface{})
	if msgs[1].(map[string]interface{})["tool_call_id"] != "old" {
		t.Fatal(wire)
	}
}
