package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	"github.com/gorilla/mux"
)

func TestNewAPIMessagesRouter(t *testing.T) {
	router := mux.NewRouter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := NewAPI(ctx, 0, "localhost", nil, nil, router)
	if err != nil {
		t.Fatal(err)
	}
	if a.grpcGateway == nil {
		t.Fatal("REST gateway not constructed")
	}
	cases := []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"POST", "/v1/messages", `{"model":"m","max_tokens":2,"messages":[{"role":"user","content":"hi"}]}`, 401, `"authentication_error"`},
		{"POST", "/v1/messages/count_tokens", `{}`, 404, `"not_found_error"`},
		{"GET", "/v1/messages", "", 405, `"invalid_request_error"`},
		{"POST", "/v1/chat/completions", `{}`, 401, `API key is required`},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
func TestMessagesUsesChatAuthenticationExactlyOnce(t *testing.T) {
	h := newHarness()
	router := mux.NewRouter()
	a := &API{router: router}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("x-api-key") != "" {
			t.Fatal("wrong dispatch")
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["sampling"]; !ok {
			t.Fatal("request bypassed REST transform")
		}
		fmt.Fprint(w, `{"result":{"id":"c","model":"m","choices":[{"message":{"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}}`)
	})
	chain := httpmw.NewAPIKeyInterceptor(false).WithAPIKeyValidation(withOpenAICompatTransform(inner))
	a.mountMessages(chain)
	a.RegisterHandlerPrefixes(withV1OpenAICompat(chain), "/v1")
	w := h.do(t, router, "POST", "/v1/messages", `{"model":"m","max_tokens":2,"messages":[{"role":"user","content":"hi"}]}`, map[string]string{"x-api-key": liveKey})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tool_use"`) || h.store.calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), h.store.calls.Load())
	}
}

func TestMessagesStreamingAcrossRESTChain(t *testing.T) {
	const frames = `{"result":{"id":"c","model":"m","choices":[{"message":{"content":[{"text":"before"}]}}]}}
{"result":{"choices":[{"message":{"tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"g","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":5}}}
`
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/openai/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			h := newHarness()
			router := mux.NewRouter()
			a := &API{router: router}
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, frames)
			})
			authed := httpmw.NewAPIKeyInterceptor(false).WithAPIKeyValidation(withOpenAICompatTransform(inner))
			chain := httpmw.WrapWithSSENegotiationWith(authed, func(r *http.Request) string {
				if format := r.Header.Get("X-SSE-Format"); format != "" {
					return format
				}
				return "proto"
			})
			a.mountMessages(chain)
			a.RegisterHandlerPrefixes(withV1OpenAICompat(chain), "/v1")
			a.RegisterHandlerPrefixes(withOpenAIRoundTrip(chain), "/openai/v1")
			raw := `{"model":"m","max_tokens":20,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
			w := h.do(t, router, "POST", path, raw, bearer(liveKey))
			if w.Code != 200 || h.store.calls.Load() != 1 || w.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatal(w.Code, w.Body.String(), h.store.calls.Load())
			}
			if path == "/v1/messages" {
				if strings.Count(w.Body.String(), `"type":"tool_use"`) != 2 || !strings.Contains(w.Body.String(), "event: message_stop") || !strings.Contains(w.Body.String(), `"output_tokens":5`) {
					t.Fatal(w.Body.String())
				}
			} else {
				if !strings.Contains(w.Body.String(), `"tool_calls":[{"function"`) || !strings.Contains(w.Body.String(), `"index":1`) || !strings.Contains(w.Body.String(), `"finish_reason":"tool_calls"`) {
					t.Fatal(w.Body.String())
				}
			}
		})
	}
}
