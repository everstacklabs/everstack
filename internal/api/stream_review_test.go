package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func reviewRESTStream(t *testing.T, runs ...func(func(gw.ChatResponseChunk) error) error) []map[string]any {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		send := func(c gw.ChatResponseChunk) error {
			choices := []map[string]any{}
			for _, d := range c.Choices {
				choices = append(choices, map[string]any{"index": d.Index, "message": d.Delta, "finish_reason": d.FinishReason})
			}
			return json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": c.ID, "model": "m", "created": 1, "choices": choices}})
		}
		var err error
		for _, run := range runs {
			err = gw.StreamToolAttempt(run, send)
			if err == nil {
				break
			}
		}
		if err != nil {
			t.Error(err)
		}
	})
	chain := httpmw.WrapWithSSENegotiationWith(inner, func(*http.Request) string { return "openai" })
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	r.Header.Set("Authorization", "Bearer evs_live_test")
	w := httptest.NewRecorder()
	withV1OpenAICompat(chain).ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatal(w.Code, w.Body.String())
	}
	chunks := []map[string]any{}
	for _, frame := range strings.Split(w.Body.String(), "\n\n") {
		if frame == "" || frame == "data: [DONE]" {
			continue
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &out); err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, out)
	}
	return chunks
}
func TestReviewRESTFailedAttemptIsolation(t *testing.T) {
	for _, mode := range []string{"complete", "indexed complete", "partial"} {
		t.Run(mode, func(t *testing.T) {
			idx := 0
			var index *int
			args := "{}"
			if mode != "complete" {
				index = &idx
			}
			if mode == "partial" {
				args = `{"bad":`
			}
			a := func(on func(gw.ChatResponseChunk) error) error {
				if err := on(gw.ChatResponseChunk{ID: "failed", Choices: []gw.ChoiceDelta{{Delta: gw.Message{ToolCalls: []gw.ToolCall{{Index: index, ID: "failed", Function: gw.ToolCallFunction{Name: "failed", Arguments: args}}}}, FinishReason: "tool_calls"}}}); err != nil {
					return err
				}
				return errors.New("upstream 503")
			}
			b := func(on func(gw.ChatResponseChunk) error) error {
				d := gw.ChoiceDelta{Delta: gw.NewMessage(gw.RoleAssistant, gw.Text("B")), FinishReason: "stop"}
				if mode == "partial" {
					d.Delta = gw.Message{ToolCalls: []gw.ToolCall{{Index: &idx, ID: "served", Function: gw.ToolCallFunction{Name: "served", Arguments: "{}"}}}}
					d.FinishReason = "tool_calls"
				}
				return on(gw.ChatResponseChunk{ID: "served", Choices: []gw.ChoiceDelta{d}})
			}
			chunks := reviewRESTStream(t, a, b)
			var text strings.Builder
			var calls []any
			finish := ""
			for _, c := range chunks {
				for _, raw := range c["choices"].([]any) {
					choice := raw.(map[string]any)
					delta := choice["delta"].(map[string]any)
					if content, ok := delta["content"].(string); ok {
						text.WriteString(content)
					}
					if v, ok := delta["tool_calls"].([]any); ok {
						calls = append(calls, v...)
					}
					if v, ok := choice["finish_reason"].(string); ok {
						finish = v
					}
				}
			}
			if mode == "partial" {
				if len(calls) != 1 || calls[0].(map[string]any)["id"] != "served" || finish != "tool_calls" {
					t.Fatal(calls, finish)
				}
			} else if text.String() != "B" || len(calls) != 0 || finish != "stop" {
				t.Fatal(text.String(), calls, finish)
			}
		})
	}
}
func TestReviewRESTEOFReleasesValidCalls(t *testing.T) {
	for _, args := range []string{`{"ok":true}`, "", `{"bad":`} {
		t.Run(fmt.Sprintf("args=%s", args), func(t *testing.T) {
			idx := 0
			chunks := reviewRESTStream(t, func(on func(gw.ChatResponseChunk) error) error {
				return on(gw.ChatResponseChunk{ID: "c", Choices: []gw.ChoiceDelta{{Delta: gw.Message{ToolCalls: []gw.ToolCall{{Index: &idx, ID: "a", Function: gw.ToolCallFunction{Name: "f", Arguments: args}}}}}}})
			})
			count := 0
			finish := ""
			for _, c := range chunks {
				for _, raw := range c["choices"].([]any) {
					choice := raw.(map[string]any)
					delta := choice["delta"].(map[string]any)
					if calls, ok := delta["tool_calls"].([]any); ok {
						count += len(calls)
						if args == "" && calls[0].(map[string]any)["function"].(map[string]any)["arguments"] != "{}" {
							t.Fatal(calls)
						}
					}
					if value, ok := choice["finish_reason"].(string); ok {
						finish = value
					}
				}
			}
			if args == `{"bad":` {
				if count != 0 || finish != "stop" {
					t.Fatal(count, finish)
				}
			} else if count != 1 || finish != "tool_calls" {
				t.Fatal(count, finish)
			}
		})
	}
}
