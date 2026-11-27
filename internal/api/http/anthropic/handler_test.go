package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validRequest = `{"model":"m","max_tokens":20,"messages":[{"role":"user","content":"hi"}]}`
const unary = `{"id":"c","model":"actual","choices":[{"message":{"content":"hello","tool_calls":[{"id":"a","function":{"name":"f","arguments":"{\"x\":1}"}},{"id":"b","function":{"name":"g","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":3}}}`

func TestUnaryResponse(t *testing.T) {
	for _, finish := range []struct{ in, out string }{{"stop", "end_turn"}, {"length", "max_tokens"}, {"tool_calls", "tool_use"}, {"function_call", "tool_use"}, {"content_filter", "refusal"}} {
		w := httptest.NewRecorder()
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-Id", "req")
			w.Header().Set("Traceparent", "trace")
			w.Header().Set("Content-Length", "999")
			fmt.Fprint(w, strings.Replace(unary, `"finish_reason":"tool_calls"`, `"finish_reason":"`+finish.in+`"`, 1))
		})
		New(inner).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(validRequest)))
		out := decode(t, w.Body.Bytes())
		content := out["content"].([]any)
		u := out["usage"].(map[string]any)
		if w.Code != 200 || out["id"] != "msg_c" || out["type"] != "message" || out["role"] != "assistant" || out["model"] != "actual" || out["stop_reason"] != finish.out || out["stop_sequence"] != nil || len(content) != 3 || u["input_tokens"] != float64(5) || u["output_tokens"] != float64(4) || u["cache_read_input_tokens"] != float64(3) {
			t.Fatal(w.Body.String())
		}
		if content[1].(map[string]any)["input"].(map[string]any)["x"] != float64(1) || len(content[2].(map[string]any)["input"].(map[string]any)) != 0 {
			t.Fatal(content)
		}
		if w.Header().Get("X-Request-Id") != "req" || w.Header().Get("Traceparent") != "trace" || w.Header().Get("Content-Length") != "" {
			t.Fatal(w.Header())
		}
	}
}
func TestCredentialsAndRequestIdentity(t *testing.T) {
	for _, tc := range []struct{ name, xkey, bearer, explicit, want string }{{"x key", "evs_live_x", "", "", "evs_live_x"}, {"bearer", "", "evs_live_b", "", "evs_live_b"}, {"both", "evs_live_x", "evs_live_b", "", "evs_live_x"}, {"explicit", "evs_live_x", "evs_live_b", "evs_live_c", "evs_live_c"}, {"none", "", "", "", ""}, {"unissued bearer", "", "session-token", "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			type identity struct{}
			ctx := context.WithValue(context.Background(), identity{}, "marker")
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("x-api-key") != "" || r.Header.Get("x-evs-api-key") != tc.want {
					t.Fatal(r.Header)
				}
				if tc.want != "" && r.Header.Get("Authorization") != "" {
					t.Fatal("bearer forwarded")
				}
				if r.Context().Value(identity{}) != "marker" || r.RemoteAddr != "192.0.2.1:42" || r.URL.Path != "/v1/chat/completions" || r.RequestURI != "/v1/chat/completions" || r.Header.Get("Traceparent") != "trace" {
					t.Fatal("request identity changed")
				}
				if tc.want == "" {
					w.WriteHeader(401)
					fmt.Fprint(w, `{"error":{"message":"API key is required"}}`)
					return
				}
				fmt.Fprint(w, unary)
			})
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(validRequest)).WithContext(ctx)
			r.RemoteAddr = "192.0.2.1:42"
			r.Header.Set("Traceparent", "trace")
			r.Header.Set("x-api-key", tc.xkey)
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.explicit != "" {
				r.Header.Set("x-evs-api-key", tc.explicit)
			}
			w := httptest.NewRecorder()
			New(inner).ServeHTTP(w, r)
			if calls != 1 {
				t.Fatal(calls)
			}
			if tc.want == "" && (w.Code != 401 || !strings.Contains(w.Body.String(), `"authentication_error"`)) {
				t.Fatal(w.Body.String())
			}
			if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != tc.xkey {
				t.Fatal("original request mutated")
			}
		})
	}
}
func TestErrorsAndMethods(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 413, 429, 500, 502, 503, 529} {
		for _, stream := range []bool{false, true} {
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-ID", "r")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"safe gateway message"}}`)
			})
			raw := validRequest
			if stream {
				raw = strings.TrimSuffix(raw, "}") + `,"stream":true}`
			}
			w := httptest.NewRecorder()
			New(inner).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(raw)))
			out := decode(t, w.Body.Bytes())
			err := out["error"].(map[string]any)
			if w.Code != status || out["type"] != "error" || err["type"] != errorType(status) || err["message"] != "safe gateway message" || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("X-Request-ID") != "r" {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/v1/messages", 405}, {"PUT", "/v1/messages", 405}, {"POST", "/v1/messages/count_tokens", 404}} {
		w := httptest.NewRecorder()
		New(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("inner called") })).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"type":"error"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.status == 405 && w.Header().Get("Allow") != "POST" {
			t.Fatal(w.Header())
		}
	}
	if msg := safeMessage([]byte(`<html>provider secret</html>`), 502); strings.Contains(msg, "secret") {
		t.Fatal(msg)
	}
}
func TestBodyCap(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			called := false
			body := validRequest + strings.Repeat(" ", int(MaxBodyBytes)-len(validRequest)+extra)
			w := httptest.NewRecorder()
			New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; fmt.Fprint(w, unary) })).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
			if extra == 0 && (!called || w.Code != 200) {
				t.Fatal(w.Code, w.Body.String())
			}
			if extra == 1 && (called || w.Code != 413 || !strings.Contains(w.Body.String(), `"request_too_large"`)) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
