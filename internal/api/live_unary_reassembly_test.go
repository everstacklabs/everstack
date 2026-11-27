package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/http/anthropic"
)

func TestLiveUnaryReassemblyAcrossSurfaces(t *testing.T) {
	const frames = `{"result":{"id":"c","model":"m","created":"1","choices":[{"index":0,"message":{"content":[{"type":"text","text":"checking"}]}}]}}
{"result":{"id":"c","model":"m","choices":[{"index":0,"message":{"tool_calls":[{"id":"weather","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}}
{"result":{"id":"c","model":"m","usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}}
`
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, frames) })
	for _, messages := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":false}`))
		if messages {
			r = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
			anthropic.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveOpenAICompat(w, r, inner) })).ServeHTTP(w, r)
		} else {
			serveOpenAICompat(w, r, inner)
		}
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "get_weather") || !strings.Contains(body, "Paris") {
			t.Fatal(w.Code, body)
		}
		if messages {
			if !strings.Contains(body, `"stop_reason":"tool_use"`) || !strings.Contains(body, `"input_tokens":11`) || !strings.Contains(body, `"output_tokens":7`) || !strings.Contains(body, `"type":"tool_use"`) {
				t.Fatal(body)
			}
		} else {
			if !strings.Contains(body, `"finish_reason":"tool_calls"`) || !strings.Contains(body, `"prompt_tokens":11`) || !strings.Contains(body, `"completion_tokens":7`) || !strings.Contains(body, `"tool_calls":[`) {
				t.Fatal(body)
			}
		}
	}
}
