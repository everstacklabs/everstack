package anthropic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveDoneWithoutFinish(t *testing.T) {
	for _, tc := range []struct{ name, chunk, want string }{{"text", textChunk, "end_turn"}, {"tool", toolChunk, "tool_use"}} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				rw.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(rw, "data: %s\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n", tc.chunk)
			})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.TrimSuffix(validRequest, "}")+`,"stream":true}`)))
			evs := events(t, w.Body.String())
			if evs[len(evs)-1].kind != "message_stop" || evs[len(evs)-2].kind != "message_delta" {
				t.Fatal(w.Body.String())
			}
			delta := evs[len(evs)-2].data
			if delta["delta"].(map[string]any)["stop_reason"] != tc.want || delta["usage"].(map[string]any)["output_tokens"] != float64(7) || strings.Contains(w.Body.String(), "event: error") {
				t.Fatal(w.Body.String())
			}
		})
	}
}
func TestLiveMissingDoneOrContentErrors(t *testing.T) {
	for _, body := range []string{"data: " + textChunk + "\n\n", "data: [DONE]\n\n", `data: {"id":"c","choices":[{"delta":{"role":"assistant"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"} {
		w := httptest.NewRecorder()
		New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(rw, body)
		})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.TrimSuffix(validRequest, "}")+`,"stream":true}`)))
		if !strings.Contains(w.Body.String(), `"type":"api_error"`) || strings.Contains(w.Body.String(), "event: message_stop") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
