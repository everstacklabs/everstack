package anthropic

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
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"type":"message_start","message":{"id":"c","model":"m"}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"f","input":{}}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"b","name":"f","input":{}}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"x\":"}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"1}"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}

data: {"type":"message_stop"}

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
	if wire["tool_choice"].(map[string]interface{})["disable_parallel_tool_use"] != true {
		t.Fatal(wire)
	}
}

func TestInitialToolInput(t *testing.T) {
	for _, tc := range []struct {
		input interface{}
		want  string
	}{{nil, "{}"}, {map[string]interface{}{"x": 1}, `{"x":1}`}} {
		if got := initialToolInput(tc.input); got != tc.want {
			t.Fatalf("%s != %s", got, tc.want)
		}
	}
}
