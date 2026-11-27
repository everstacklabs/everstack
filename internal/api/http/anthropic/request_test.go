package anthropic

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, raw []byte) object {
	t.Helper()
	var out object
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return out
}
func TestRequestMapping(t *testing.T) {
	tests := []struct{ name, extra, messages, want string }{
		{"defaults", ``, `[{"role":"user","content":"hello"}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`},
		{"sampling metadata ignored", `,"temperature":0,"top_p":0.5,"stop_sequences":["END"],"stream":true,"metadata":{"user_id":"u"},"top_k":20,"thinking":{"type":"enabled"},"cache_control":{},"service_tier":"auto","container":"c","mcp_servers":[]`, `[{"role":"user","content":"hello"}]`, `{"model":"model","max_tokens":50,"stream":true,"temperature":0,"top_p":0.5,"stop":["END"],"user":"u","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`},
		{"system string", `,"system":"rules"`, `[{"role":"user","content":"hello"}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"system","content":"rules"},{"role":"user","content":[{"type":"text","text":"hello"}]}]}`},
		{"system blocks", `,"system":[{"type":"text","text":"a","cache_control":{}},{"type":"text","text":"b"}]`, `[{"role":"user","content":"hello"}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"system","content":"a\n\nb"},{"role":"user","content":[{"type":"text","text":"hello"}]}]}`},
		{"images", ``, `[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}},{"type":"image","source":{"type":"url","url":"https://img"}}]}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}},{"type":"image_url","image_url":{"url":"https://img"}}]}]}`},
		{"assistant history", ``, `[{"role":"assistant","content":[{"type":"thinking","thinking":"secret"},{"type":"redacted_thinking","data":"secret"},{"type":"text","text":"calling"},{"type":"tool_use","id":"a","name":"f","input":{"x":1}}]}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"assistant","content":[{"type":"text","text":"calling"}],"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{\"x\":1}"}}]}]}`},
		{"ordered results before text", ``, `[{"role":"user","content":[{"type":"text","text":"first"},{"type":"tool_result","tool_use_id":"b","content":[{"type":"text","text":"r1"},{"type":"text","text":"r2"}],"is_error":true},{"type":"tool_result","tool_use_id":"a","content":"ok"},{"type":"text","text":"last"}]}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"tool","tool_call_id":"b","content":"Error: r1\nr2"},{"role":"tool","tool_call_id":"a","content":"ok"},{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"last"}]}]}`},
		{"only results", ``, `[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"ok"}]}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"tool","tool_call_id":"a","content":"ok"}]}`},
		{"tools", `,"tools":[{"name":"f","description":"desc","input_schema":{"type":"object"}},{"type":"custom","name":"g","input_schema":{}}],"tool_choice":{"type":"tool","name":"f","disable_parallel_tool_use":true}`, `[{"role":"user","content":"hello"}]`, `{"model":"model","max_tokens":50,"stream":false,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"type":"function","function":{"name":"f","description":"desc","parameters":{"type":"object"}}},{"type":"function","function":{"name":"g","description":"","parameters":{}}}],"tool_choice":{"type":"function","function":{"name":"f"}},"parallel_tool_calls":false}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"model":"model","max_tokens":50,"messages":` + tc.messages + tc.extra + `}`
			out, stream, err := translateRequest([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			got := decode(t, out)
			want := decode(t, []byte(tc.want))
			g, _ := json.Marshal(got)
			w, _ := json.Marshal(want)
			if string(g) != string(w) {
				t.Fatalf("got %s\nwant %s", g, w)
			}
			if stream != got["stream"] {
				t.Fatal("stream mismatch")
			}
		})
	}
	for _, choice := range []struct{ in, out string }{{"auto", "auto"}, {"any", "required"}, {"none", "none"}} {
		raw := `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"` + choice.in + `"}}`
		out, _, err := translateRequest([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if decode(t, out)["tool_choice"] != choice.out {
			t.Fatal(string(out))
		}
	}
}
func TestRequestRejections(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{`, "invalid JSON"},
		{`{"model":"m","messages":[]}`, "max_tokens"},
		{`{"model":"m","max_tokens":0}`, "max_tokens"},
		{`{"max_tokens":1}`, "model and messages"},
		{`{"max_tokens":1,"model":"m","messages":[{"role":"user","content":[{"type":"document"}]}]}`, "document content is not supported"},
		{`{"max_tokens":1,"model":"m","messages":[{"role":"user","content":[{"type":"alien"}]}]}`, "alien"},
		{`{"max_tokens":1,"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{"type":"file"}}]}]}`, "file"},
	} {
		_, _, err := translateRequest([]byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.body, err)
		}
	}
	for _, typ := range []string{"web_search_20250305", "bash_20250124"} {
		raw := `{"max_tokens":1,"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"` + typ + `"}]}`
		_, _, err := translateRequest([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), typ) {
			t.Fatal(err)
		}
	}
}
