package openaiapi

import (
	"encoding/json"
	"testing"
)

func TestMatchesToolResultRequiresExactSavedShape(t *testing.T) {
	r := ToolResult{TurnID: "turn", CallID: "call", Success: true, Output: `"marker"`}
	base := map[string]any{"type": "function_call_output", "status": "completed", "turn_id": "turn", "call_id": "call", "output": `"marker"`, "error": nil}
	encode := func(v map[string]any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !matchesToolResult(encode(base), r) {
		t.Fatal("exact saved result did not match")
	}
	for _, tc := range []struct {
		key   string
		value any
	}{{"turn_id", "other"}, {"call_id", "other"}, {"status", "in_progress"}, {"output", map[string]any{"bad": true}}, {"error", map[string]any{"bad": true}}} {
		v := map[string]any{}
		for k, value := range base {
			v[k] = value
		}
		v[tc.key] = tc.value
		if matchesToolResult(encode(v), r) {
			t.Errorf("mismatched %s accepted", tc.key)
		}
	}
	missing := map[string]any{}
	for k, value := range base {
		missing[k] = value
	}
	delete(missing, "error")
	if matchesToolResult(encode(missing), r) {
		t.Fatal("missing error accepted")
	}
	empty := ToolResult{TurnID: "turn", CallID: "call", Success: true, Output: ""}
	if matchesToolResult(encode(map[string]any{"type": "function_call_output", "status": "completed", "turn_id": "turn", "call_id": "call", "output": nil, "error": nil}), empty) {
		t.Fatal("null output accepted as empty string")
	}
	failure := ToolResult{TurnID: "turn", CallID: "call", Success: false, Error: "failed"}
	if !matchesToolResult(encode(map[string]any{"type": "function_call_output", "status": "completed", "turn_id": "turn", "call_id": "call", "output": "", "error": "failed"}), failure) {
		t.Fatal("exact failed result did not match")
	}
}
