package v1

import (
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"testing"
	"time"
)

func TestToolsSurviveProtoAndCacheReplay(t *testing.T) {
	for _, count := range []int{1, 2} {
		for _, text := range []string{"", "before"} {
			calls := []gw.ToolCall{}
			for i := 0; i < count; i++ {
				calls = append(calls, gw.ToolCall{ID: string(rune('a' + i)), Type: "function", Function: gw.ToolCallFunction{Name: "f", Arguments: `{}`}})
			}
			msg := gw.Message{Role: gw.RoleAssistant, ToolCalls: calls}
			if text != "" {
				msg.Content = []gw.ContentPart{gw.Text(text)}
			}
			resp := gw.ChatCompletionResponse{Created: time.Now(), Choices: []gw.Choice{{Message: msg, FinishReason: "tool_calls"}}}
			if !isValidCachedChatResponse(resp) {
				t.Fatal("cache rejected complete response")
			}
			// This is the exact/semantic cache wire representation and replay converter.
			raw, err := fastpath.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			var replay gw.ChatCompletionResponse
			if err = fastpath.Unmarshal(raw, &replay); err != nil {
				t.Fatal(err)
			}
			unary := toProtoResponse(replay)
			chunk := toProtoResponseFromChunk(gw.ChatResponseChunk{Choices: []gw.ChoiceDelta{{Delta: msg, FinishReason: "tool_calls"}}})
			for _, out := range []*gatewaypb.ChatCompletionResponse{unary, chunk} {
				if len(out.Choices[0].Message.ToolCalls) != count || out.Choices[0].Message.ToolCalls[0].Function.Arguments != `{}` || out.Choices[0].FinishReason != "tool_calls" {
					t.Fatal(out)
				}
			}
		}
	}
}
func TestRESTRequestToolsReachGateway(t *testing.T) {
	var in gatewaypb.ChatCompletionRequest
	raw := `{"messages":[{"role":"ROLE_ASSISTANT","tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"ROLE_TOOL","content":[{"type":"text","text":"ok"}],"tool_call_id":"a"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"tool_choice":{"specific_tool":{"type":"function","function":{"name":"f"}}},"parallel_tool_calls":false}`
	if err := protojson.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	out := toGatewayRequest(&in)
	if out.ParallelToolCalls == nil || *out.ParallelToolCalls || len(out.Tools) != 1 || len(out.Messages[0].ToolCalls) != 1 || out.Messages[1].ToolCallID != "a" {
		t.Fatalf("%+v", out)
	}
	if out.ToolChoice.(map[string]interface{})["function"].(map[string]interface{})["name"] != "f" {
		t.Fatal(out.ToolChoice)
	}
}

func TestToolChunkEmptyArgumentsAndFinishReasons(t *testing.T) {
	for _, finish := range []string{"tool_calls", "length", "content_filter", "function_call"} {
		out := toProtoResponseFromChunk(gw.ChatResponseChunk{Choices: []gw.ChoiceDelta{{Delta: gw.Message{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{{ID: "a", Type: "function", Function: gw.ToolCallFunction{Name: "f"}}}}, FinishReason: finish}}})
		if out.Choices[0].FinishReason != finish || out.Choices[0].Message.ToolCalls[0].Function.Arguments != "" {
			t.Fatal(out)
		}
	}
}
