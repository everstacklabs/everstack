package v1

// A Responses client that runs its own tools sends the results back as
// function_call_output input items, usually chained to the response that
// asked for them with previous_response_id. These tests drive that round
// trip through the chat-backed fallback (the streamHarness: real Connect
// handler behind the production REST gateway) and check what the model is
// sent on the second request.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

const toolResultOutput = `{"temp_c":21,"sky":"sunny"}`

// toolCallTurn is a unary turn that asks for one get_weather call. Its
// finish reason is "stop" so the turn ends the response whether or not the
// fallback loop treats a "tool_calls" finish as the end of a response.
func toolCallTurn() gw.ChatCompletionResponse {
	return gw.ChatCompletionResponse{
		ID: "x", Model: streamModelName,
		Choices: []gw.Choice{{
			Message: gw.Message{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{{ID: "call_1", Type: "function",
				Function: gw.ToolCallFunction{Name: "get_weather", Arguments: `{"city":"Paris"}`}}}},
			FinishReason: "stop",
		}},
		Usage: gw.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
	}
}

func sunnyTurn() gw.ChatCompletionResponse {
	return gw.ChatCompletionResponse{
		ID: "y", Model: streamModelName,
		Choices: []gw.Choice{{Message: gw.NewMessage(gw.RoleAssistant, gw.Text("Sunny, 21C")), FinishReason: "stop"}},
		Usage:   gw.Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12},
	}
}

// toolResultBody is the second request as an OpenAI SDK sends it: only the
// tool output, chained to the response that asked for it.
func toolResultBody(previousID string, stream bool) string {
	return fmt.Sprintf(`{"model":%q,"stream":%t,"previous_response_id":%q,"input":[{"type":"function_call_output","call_id":"call_1","output":%q}]}`,
		streamModelName, stream, previousID, toolResultOutput)
}

// postUnary posts a non-streaming request and returns the final response
// object (the last NDJSON frame's result).
func (h *streamHarness) postUnary(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	resp, err := http.Post(h.url+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unary: %d %s", resp.StatusCode, raw)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var last struct {
		Result map[string]interface{} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("last frame: %v: %s", err, raw)
	}
	return last.Result
}

func functionCallOf(resp map[string]interface{}) map[string]interface{} {
	o, _ := resp["output"].([]interface{})
	for _, raw := range o {
		if it, _ := raw.(map[string]interface{}); it["type"] == "function_call" {
			return it
		}
	}
	return nil
}

func textOf(m gw.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		if c.Text != nil {
			b.WriteString(*c.Text)
		}
	}
	return b.String()
}

// assertToolResultConversation checks the messages the model is sent once
// the tool result is back: the user's question, the assistant turn that
// called the tool, and the tool's output answering that call, in order.
func assertToolResultConversation(t *testing.T, msgs []gw.Message) {
	t.Helper()
	dump := func() string {
		var b strings.Builder
		for i, m := range msgs {
			fmt.Fprintf(&b, "\n  [%d] role=%s text=%q tool_calls=%+v tool_call_id=%q", i, m.Role, textOf(m), m.ToolCalls, m.ToolCallID)
		}
		return b.String()
	}
	if len(msgs) != 3 {
		t.Fatalf("model was sent %d messages, want user, assistant tool call, tool output:%s", len(msgs), dump())
	}
	if msgs[0].Role != gw.RoleUser || textOf(msgs[0]) != "hi" {
		t.Fatalf("message 0 is not the user's question:%s", dump())
	}
	if msgs[1].Role != gw.RoleAssistant || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("message 1 is not the assistant tool call:%s", dump())
	}
	tc := msgs[1].ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if msgs[2].Role != gw.RoleTool || msgs[2].ToolCallID != "call_1" || textOf(msgs[2]) != toolResultOutput {
		t.Fatalf("message 2 is not the tool output for call_1:%s", dump())
	}
}

// Unary: the response asks for a tool call, the client runs it and sends
// the output back chained with previous_response_id, and the model gets the
// whole exchange, the original question included.
func TestResponsesFallbackUnaryToolResultRoundTrip(t *testing.T) {
	p := &scriptedStream{unary: []gw.ChatCompletionResponse{toolCallTurn(), sunnyTurn()}}
	h := newStreamHarness(t, p)

	first := h.postUnary(t, responsesBody(false))
	if first["status"] != "completed" {
		t.Fatalf("first response status = %v: %v", first["status"], first)
	}
	fc := functionCallOf(first)
	if fc == nil || fc["call_id"] != "call_1" {
		t.Fatalf("first response function_call = %v", fc)
	}
	id, _ := first["id"].(string)
	if id == "" {
		t.Fatalf("first response has no id: %v", first)
	}

	second := h.postUnary(t, toolResultBody(id, false))
	if second["status"] != "completed" {
		t.Fatalf("second response status = %v: %v", second["status"], second)
	}
	if got := outputText(second); got != "Sunny, 21C" {
		t.Fatalf("second response text = %q", got)
	}
	if len(p.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(p.requests))
	}
	assertToolResultConversation(t, p.requests[1].Messages)
}

// Streaming: the same round trip with stream:true on both requests.
func TestResponsesFallbackStreamToolResultRoundTrip(t *testing.T) {
	p := &scriptedStream{
		turns: [][]gw.ChatResponseChunk{
			{toolChunk("call_1", "get_weather", ""), toolChunk("", "", `{"city":`), toolChunk("", "", `"Paris"}`), finishChunk("stop", 5, 2)},
			{textChunk("Sunny, "), textChunk("21C"), finishChunk("stop", 9, 3)},
		},
	}
	h := newStreamHarness(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	run := func(body string) map[string]interface{} {
		t.Helper()
		all := drain(t, h.postStreamBody(t, ctx, body))
		acc := &sdkAccumulator{t: t}
		for _, ev := range all {
			acc.apply(ev)
		}
		if acc.terminal != "response.completed" {
			t.Fatalf("terminal = %s, events %v", acc.terminal, names(all))
		}
		return acc.final
	}

	first := run(responsesBody(true))
	if fc := functionCallOf(first); fc == nil || fc["call_id"] != "call_1" {
		t.Fatalf("first response function_call = %v", fc)
	}
	id, _ := first["id"].(string)
	if id == "" {
		t.Fatalf("first response has no id: %v", first)
	}

	second := run(toolResultBody(id, true))
	if got := outputText(second); got != "Sunny, 21C" {
		t.Fatalf("second response text = %q", got)
	}
	if len(p.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(p.requests))
	}
	assertToolResultConversation(t, p.requests[1].Messages)
}

// A client that keeps its own history replays the whole exchange as input
// items, with no previous_response_id: the function_call item becomes the
// assistant tool call and the function_call_output its tool message. The
// replayed items carry the extra fields an SDK echoes back from the output
// (id, status), which are ignored.
func TestResponsesFallbackInputReplayOfToolExchange(t *testing.T) {
	p := &scriptedStream{unary: []gw.ChatCompletionResponse{sunnyTurn()}}
	h := newStreamHarness(t, p)

	body := fmt.Sprintf(`{"model":%q,"stream":false,"input":[`+
		`{"type":"message","role":"ROLE_USER","content":[{"type":"text","text":"hi"}]},`+
		`{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},`+
		`{"type":"function_call_output","call_id":"call_1","output":%q}]}`, streamModelName, toolResultOutput)
	resp := h.postUnary(t, body)
	if resp["status"] != "completed" {
		t.Fatalf("status = %v: %v", resp["status"], resp)
	}
	if len(p.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(p.requests))
	}
	assertToolResultConversation(t, p.requests[0].Messages)
}

// Consecutive function_call items are one assistant turn with parallel tool
// calls, and a text message followed by function_call items is one assistant
// turn with text and tool calls, as Chat Completions represents them.
func TestConvertInputsMergesConsecutiveFunctionCalls(t *testing.T) {
	s := &Server{}
	msgs := s.convertInputsToMessages(gw.CreateResponseRequest{Input: []gw.ResponseInput{
		{Type: "message", Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("weather in Paris and Rome?")}},
		{Type: "message", Role: gw.RoleAssistant, Content: []gw.ContentPart{gw.Text("Checking both.")}},
		{Type: "function_call", CallID: "call_a", Name: "get_weather", Arguments: `{"city":"Paris"}`},
		{Type: "function_call", CallID: "call_b", Name: "get_weather", Arguments: `{"city":"Rome"}`},
		{Type: "function_call_output", CallID: "call_a", Output: "sunny"},
		{Type: "function_call_output", CallID: "call_b", Output: "rain"},
	}})
	if len(msgs) != 4 {
		t.Fatalf("messages = %+v, want user, assistant, tool, tool", msgs)
	}
	a := msgs[1]
	if a.Role != gw.RoleAssistant || textOf(a) != "Checking both." || len(a.ToolCalls) != 2 ||
		a.ToolCalls[0].ID != "call_a" || a.ToolCalls[1].ID != "call_b" || a.ToolCalls[1].Function.Arguments != `{"city":"Rome"}` {
		t.Fatalf("assistant message = %+v", a)
	}
	if msgs[2].Role != gw.RoleTool || msgs[2].ToolCallID != "call_a" || textOf(msgs[2]) != "sunny" ||
		msgs[3].Role != gw.RoleTool || msgs[3].ToolCallID != "call_b" || textOf(msgs[3]) != "rain" {
		t.Fatalf("tool messages = %+v %+v", msgs[2], msgs[3])
	}
}
