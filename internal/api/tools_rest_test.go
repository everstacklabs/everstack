package api

import (
	"encoding/json"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRESTToolRequestShapes(t *testing.T) {
	for _, content := range []string{`null`, `"text"`, `[{"type":"text","text":"text"},{"type":"image_url","image_url":{"url":"https://image"}}]`} {
		for _, choice := range []string{`"auto"`, `"none"`, `"required"`, `{"type":"function","function":{"name":"f"}}`} {
			body := `{"model":"m","user":"user123","messages":[{"role":"assistant","content":` + content + `,"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","content":"ok","tool_call_id":"a"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"tool_choice":` + choice + `,"parallel_tool_calls":false}`
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			r = transformChatCompletionRequest(r)
			raw, _ := io.ReadAll(r.Body)
			var in gatewaypb.ChatCompletionRequest
			if err := protojson.Unmarshal(raw, &in); err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
			if in.GetMetadata().GetFields()["user"].GetStringValue() != "user123" {
				t.Fatal(&in)
			}
			if len(in.Tools) != 1 || in.ParallelToolCalls == nil || *in.ParallelToolCalls || len(in.Messages[0].ToolCalls) != 1 || in.Messages[1].ToolCallId != "a" {
				t.Fatal(&in)
			}
			if choice[0] == '{' && in.ToolChoice.GetSpecificTool().GetFunction().GetName() != "f" {
				t.Fatal(&in)
			}
		}
	}
}
func TestUnaryToolsOnBothRESTAliases(t *testing.T) {
	for _, prefix := range []string{"/v1", "/openai/v1"} {
		for _, text := range []string{"", "before"} {
			for _, count := range []int{1, 2} {
				calls := []map[string]any{}
				for i := 0; i < count; i++ {
					calls = append(calls, map[string]any{"id": string(rune('a' + i)), "type": "function", "function": map[string]any{"name": "f", "arguments": ""}})
				}
				raw, _ := json.Marshal(map[string]any{"result": map[string]any{"id": "c", "model": "m", "choices": []any{map[string]any{"message": map[string]any{"content": []any{map[string]any{"text": text}}, "tool_calls": calls}, "finish_reason": "tool_calls"}}}})
				inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/chat/completions" {
						t.Fatal(r.URL.Path)
					}
					_, _ = w.Write(raw)
				})
				handler := withV1OpenAICompat(inner)
				if prefix == "/openai/v1" {
					handler = withOpenAIRoundTrip(inner)
				}
				r := httptest.NewRequest("POST", prefix+"/chat/completions", strings.NewReader(`{}`))
				r.Header.Set("Authorization", "Bearer evs_live_test")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				var out struct {
					Choices []struct {
						Message struct {
							Content *string `json:"content"`
							Calls   []any   `json:"tool_calls"`
						} `json:"message"`
						Finish string `json:"finish_reason"`
					} `json:"choices"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Choices) != 1 || len(out.Choices[0].Message.Calls) != count || out.Choices[0].Finish != "tool_calls" || (text == "" && out.Choices[0].Message.Content != nil) {
					t.Fatal(w.Body.String())
				}
			}
		}
	}
}

func TestUnaryToolWithProtoOmittedEmptyArguments(t *testing.T) {
	raw, ok := protoChatCompletionToOpenAI([]byte(`{"result":{"choices":[{"message":{"tool_calls":[{"id":"a","function":{"name":"f"}}]},"finish_reason":"tool_calls"}]}}`))
	if !ok || !strings.Contains(string(raw), `"arguments":""`) || !strings.Contains(string(raw), `"content":null`) {
		t.Fatal(string(raw))
	}
}

func TestReviewUnaryMultiChoiceIndexes(t *testing.T) {
	for _, withTools := range []bool{false, true} {
		frames := `{"result":{"choices":[{"index":1,"message":{"content":[{"text":"one"}]}},{"index":0,"message":{"content":[{"text":"zero"}]}}]}}` + "\n"
		if withTools {
			frames += `{"result":{"choices":[{"index":1,"message":{"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}}` + "\n"
		} else {
			frames += `{"result":{"choices":[{"index":1,"message":{"content":[{"text":"!"}]},"finish_reason":"length"}]}}` + "\n"
		}
		frames += `{"result":{"choices":[{"index":0,"message":{},"finish_reason":"stop"}]}}` + "\n"
		raw, ok := protoChatCompletionToOpenAI([]byte(frames))
		if !ok {
			t.Fatal("conversion failed")
		}
		var out struct {
			Choices []struct {
				Index   int `json:"index"`
				Message struct {
					Content string `json:"content"`
					Calls   []any  `json:"tool_calls"`
				} `json:"message"`
				Finish string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Choices) != 2 || out.Choices[0].Index != 0 || out.Choices[1].Index != 1 || out.Choices[0].Message.Content != "zero" || out.Choices[0].Finish != "stop" || len(out.Choices[0].Message.Calls) != 0 {
			t.Fatal(string(raw))
		}
		if withTools {
			if out.Choices[1].Message.Content != "one" || len(out.Choices[1].Message.Calls) != 1 || out.Choices[1].Finish != "tool_calls" {
				t.Fatal(string(raw))
			}
		} else if out.Choices[1].Message.Content != "one!" || out.Choices[1].Finish != "length" {
			t.Fatal(string(raw))
		}
	}
}
