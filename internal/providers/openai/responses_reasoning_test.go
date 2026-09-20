package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

const reasoningToolResponseJSON = `{"id":"resp_reasoning","created_at":1700000000,"model":"gpt-6-astra","status":"completed","output":[{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"encrypted-reasoning"},{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Dublin\"}"}],"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28}}`

const reasoningFinalResponseJSON = `{"id":"resp_final","created_at":1700000001,"model":"gpt-6-astra","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"It is raining."}]}],"usage":{"input_tokens":30,"output_tokens":5,"total_tokens":35}}`

func TestResponsesReasoningSequenceIsReplayedWithToolOutput(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "unary"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			server, calls := newReasoningContinuationServer(t, streaming)
			defer server.Close()

			provider := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
			request := reasoningToolRequest()
			var assistant gw.Message
			if streaming {
				assistant = collectResponsesAssistant(t, provider, request)
			} else {
				response, err := provider.Chat(t.Context(), request)
				if err != nil {
					t.Fatalf("first Chat() error = %v", err)
				}
				assistant = response.Choices[0].Message
			}

			if _, ok := responsesOutputSequenceFromContent(assistant.Content); !ok {
				t.Fatal("first response did not preserve its native output sequence")
			}
			if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call_weather" {
				t.Fatalf("tool calls = %+v, want call_weather", assistant.ToolCalls)
			}

			request.Messages = append(request.Messages,
				assistant,
				gw.Message{
					Role:       gw.RoleTool,
					ToolCallID: "call_weather",
					Content:    []gw.ContentPart{gw.Text(`{"temperature_c":12,"condition":"rain"}`)},
				},
			)

			if streaming {
				final := collectResponsesAssistant(t, provider, request)
				if got := messageTextForResponses(final.Content); got != "It is raining." {
					t.Fatalf("final streamed text = %q", got)
				}
			} else {
				response, err := provider.Chat(t.Context(), request)
				if err != nil {
					t.Fatalf("second Chat() error = %v", err)
				}
				if got := messageTextForResponses(response.Choices[0].Message.Content); got != "It is raining." {
					t.Fatalf("final unary text = %q", got)
				}
			}

			if got := calls.Load(); got != 2 {
				t.Fatalf("provider requests = %d, want 2", got)
			}
		})
	}
}

func reasoningToolRequest() gw.ChatCompletionRequest {
	return gw.ChatCompletionRequest{
		Model:           "gpt-6-astra",
		UseResponsesAPI: true,
		Messages: []gw.Message{
			gw.NewMessage(gw.RoleUser, gw.Text("What is the weather?")),
		},
		Tools: []gw.ToolDefinition{{
			Type: "function",
			Function: gw.ToolFunctionDef{
				Name:       "get_weather",
				Parameters: map[string]interface{}{"type": "object"},
			},
		}},
		Sampling: gw.SamplingParams{ReasoningEffort: "high"},
	}
}

func newReasoningContinuationServer(t *testing.T, streaming bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		requestNumber := calls.Add(1)
		var body struct {
			Input   []json.RawMessage `json:"input"`
			Include []string          `json:"include"`
			Stream  bool              `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request %d: %v", requestNumber, err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.Stream != streaming {
			t.Errorf("request %d stream = %v, want %v", requestNumber, body.Stream, streaming)
		}
		if len(body.Include) != 1 || body.Include[0] != "reasoning.encrypted_content" {
			t.Errorf("request %d include = %v, want reasoning.encrypted_content", requestNumber, body.Include)
		}
		if requestNumber == 2 {
			assertReasoningContinuationInput(t, body.Input)
		}

		responseJSON := reasoningToolResponseJSON
		if requestNumber == 2 {
			responseJSON = reasoningFinalResponseJSON
		}
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", responseJSON)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseJSON))
	}))
	return server, &calls
}

func assertReasoningContinuationInput(t *testing.T, input []json.RawMessage) {
	t.Helper()
	if len(input) != 4 {
		t.Errorf("continuation input items = %d, want user + reasoning + function call + output", len(input))
		return
	}
	wantTypes := []string{"message", "reasoning", "function_call", "function_call_output"}
	for i, want := range wantTypes {
		var item map[string]interface{}
		if err := json.Unmarshal(input[i], &item); err != nil {
			t.Errorf("input[%d] is invalid JSON: %v", i, err)
			continue
		}
		if got, _ := item["type"].(string); got != want {
			t.Errorf("input[%d].type = %q, want %q", i, got, want)
		}
		if want == "reasoning" && item["encrypted_content"] != "encrypted-reasoning" {
			t.Errorf("reasoning item lost encrypted content: %s", input[i])
		}
		if want == "function_call_output" {
			if item["call_id"] != "call_weather" || !strings.Contains(fmt.Sprint(item["output"]), "rain") {
				t.Errorf("function output = %s, want call_weather rain result", input[i])
			}
		}
	}
}

func collectResponsesAssistant(t *testing.T, provider *Provider, request gw.ChatCompletionRequest) gw.Message {
	t.Helper()
	assistant := gw.Message{Role: gw.RoleAssistant}
	err := provider.ChatStream(t.Context(), request, func(chunk gw.ChatResponseChunk) error {
		for _, choice := range chunk.Choices {
			assistant.Content = append(assistant.Content, choice.Delta.Content...)
			assistant.ToolCalls = append(assistant.ToolCalls, choice.Delta.ToolCalls...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	return assistant
}
