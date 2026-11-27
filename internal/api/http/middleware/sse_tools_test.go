package middleware

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSSECompleteToolCalls(t *testing.T) {
	for _, count := range []int{1, 2} {
		for _, text := range []string{"", "before"} {
			for _, finish := range []string{"tool_calls", "stop", "length", "content_filter"} {
				s := &sseWriter{format: "openai"}
				if text != "" {
					decodeChunk(t, s.transformToOpenAIChunk(`{"result":{"choices":[{"message":{"content":[{"text":"before"}]}}]}}`))
				}
				for i := 0; i < count; i++ {
					raw := fmt.Sprintf(`{"result":{"choices":[{"message":{"tool_calls":[{"id":"call_%d","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}}`, i)
					out := decodeChunk(t, s.transformToOpenAIChunk(raw))
					d := out["choices"].([]interface{})[0].(map[string]interface{})["delta"].(map[string]interface{})
					calls := d["tool_calls"].([]interface{})
					call := calls[0].(map[string]interface{})
					if int(call["index"].(float64)) != i || call["function"].(map[string]interface{})["arguments"] != "{}" {
						t.Fatal(out)
					}
				}
				raw, _ := json.Marshal(map[string]interface{}{"result": map[string]interface{}{"choices": []interface{}{map[string]interface{}{"finish_reason": finish}}}})
				out := decodeChunk(t, s.transformToOpenAIChunk(string(raw)))
				fr := out["choices"].([]interface{})[0].(map[string]interface{})["finish_reason"]
				want := finish
				if finish == "stop" {
					want = "tool_calls"
				}
				if fr != want {
					t.Fatal(out)
				}
			}
		}
	}
}

func TestSSEToolWithEmptyArguments(t *testing.T) {
	s := &sseWriter{format: "openai"}
	chunk := decodeChunk(t, s.transformToOpenAIChunk(`{"result":{"choices":[{"message":{"tool_calls":[{"id":"a","function":{"name":"f"}}]},"finish_reason":"tool_calls"}]}}`))
	call := chunk["choices"].([]interface{})[0].(map[string]interface{})["delta"].(map[string]interface{})["tool_calls"].([]interface{})[0].(map[string]interface{})
	if call["function"].(map[string]interface{})["arguments"] != "" {
		t.Fatal(call)
	}
}

func TestReviewSSEMultiChoiceIndexes(t *testing.T) {
	for _, withTools := range []bool{false, true} {
		s := &sseWriter{format: "openai"}
		first := decodeChunk(t, s.transformToOpenAIChunk(`{"result":{"choices":[{"index":1,"message":{"content":[{"text":"one"}]}},{"message":{"content":[{"text":"zero"}]}}]}}`))
		choices := first["choices"].([]any)
		for n, want := range []int{1, 0} {
			choice := choices[n].(map[string]any)
			if choice["index"] != float64(want) {
				t.Fatal(choices)
			}
		}
		frame := `{"result":{"choices":[{"index":1,"message":{"content":[{"text":"!"}]},"finish_reason":"stop"}]}}`
		if withTools {
			frame = `{"result":{"choices":[{"index":1,"message":{"tool_calls":[{"id":"a","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}}`
		}
		out := decodeChunk(t, s.transformToOpenAIChunk(frame))
		choice := out["choices"].([]any)[0].(map[string]any)
		if choice["index"] != float64(1) {
			t.Fatal(choice)
		}
		if withTools {
			if choice["finish_reason"] != "tool_calls" || choice["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["index"] != float64(0) {
				t.Fatal(choice)
			}
		} else if choice["finish_reason"] != "stop" {
			t.Fatal(choice)
		}
		out = decodeChunk(t, s.transformToOpenAIChunk(`{"result":{"choices":[{"index":0,"message":{},"finish_reason":"stop"}]}}`))
		if out["choices"].([]any)[0].(map[string]any)["finish_reason"] != "stop" {
			t.Fatal("choice 1 tools contaminated choice 0", out)
		}
	}
}
