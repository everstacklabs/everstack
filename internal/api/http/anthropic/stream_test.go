package anthropic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type event struct {
	kind string
	data object
}

func events(t *testing.T, body string) []event {
	t.Helper()
	out := []event{}
	for _, frame := range strings.Split(strings.TrimSpace(body), "\n\n") {
		lines := strings.Split(frame, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("invalid event %q", frame)
		}
		kind := strings.TrimPrefix(lines[0], "event: ")
		obj := decode(t, []byte(strings.TrimPrefix(lines[1], "data: ")))
		if obj["type"] != kind {
			t.Fatal(obj)
		}
		out = append(out, event{kind, obj})
	}
	return out
}

const textChunk = `{"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"hello"}}]}`
const toolChunk = `{"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`
const toolChunk2 = `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"b","type":"function","function":{"name":"g","arguments":"{\"x\":1}"}}]}}]}`
const finishChunk = `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`

func TestStreaming(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   string
	}{{"text", []string{textChunk, `{"choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`}, "message_start,ping,content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop"}, {"tool", []string{toolChunk, finishChunk}, "message_start,ping,content_block_start,content_block_delta,content_block_stop,message_delta,message_stop"}, {"text then parallel tools", []string{textChunk, toolChunk, toolChunk2, finishChunk}, "message_start,ping,content_block_start,content_block_delta,content_block_stop,content_block_start,content_block_delta,content_block_stop,content_block_start,content_block_delta,content_block_stop,message_delta,message_stop"}} {
		for _, withUsage := range []bool{false, true} {
			t.Run(tc.name+fmt.Sprint(withUsage), func(t *testing.T) {
				w := httptest.NewRecorder()
				inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					rw.Header().Set("Content-Type", "text/event-stream")
					rw.Header().Set("X-Request-ID", "trace")
					rw.WriteHeader(200)
					for i, chunk := range tc.chunks {
						frame := "data: " + chunk + "\n\n"
						for _, b := range []byte(frame) {
							if _, err := rw.Write([]byte{b}); err != nil {
								t.Fatal(err)
							}
						}
						rw.(http.Flusher).Flush()
						if i == 0 && (!w.Flushed || !strings.Contains(w.Body.String(), "message_start") || !strings.Contains(w.Body.String(), "content_block_delta")) {
							t.Fatal("stream buffered")
						}
					}
					if withUsage {
						fmt.Fprint(rw, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":7}}\n\n")
					}
					fmt.Fprint(rw, "data: [DONE]\n\n")
				})
				raw := strings.TrimSuffix(validRequest, "}") + `,"stream":true}`
				New(inner).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(raw)))
				evs := events(t, w.Body.String())
				names := []string{}
				lastIndex := -1
				for _, e := range evs {
					names = append(names, e.kind)
					if e.kind == "content_block_start" {
						index := int(e.data["index"].(float64))
						if index != lastIndex+1 {
							t.Fatal("nonsequential", evs)
						}
						lastIndex = index
					}
					if e.kind == "message_delta" {
						u := e.data["usage"].(map[string]any)
						want := float64(0)
						if withUsage {
							want = 7
						}
						if u["output_tokens"] != want {
							t.Fatal(u)
						}
						if withUsage && u["input_tokens"] != float64(12) {
							t.Fatal(u)
						}
					}
				}
				if strings.Join(names, ",") != tc.want || w.Header().Get("Content-Type") != "text/event-stream" || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("X-Request-ID") != "trace" {
					t.Fatal(w.Body.String())
				}
				if tc.name != "text" && !strings.Contains(w.Body.String(), `"partial_json":"{}"`) {
					t.Fatal(w.Body.String())
				}
			})
		}
	}
}
func TestStreamErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		started    bool
	}{{"before first chunk", `data: {"error":{"type":"rate_limit_error","message":"retry later"}}` + "\n\n", 429, false}, {"mid stream", "data: " + textChunk + "\n\ndata: {\"error\":{\"message\":\"safe failure\"}}\n\n", 200, true}, {"invalid first chunk", "data: invalid\n\n", 502, false}} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				rw.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(rw, tc.body)
				fmt.Fprint(rw, "data: [DONE]\n\n")
			})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.TrimSuffix(validRequest, "}")+`,"stream":true}`)))
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.started {
				evs := events(t, w.Body.String())
				if evs[len(evs)-1].kind != "error" || strings.Contains(w.Body.String(), "message_stop") {
					t.Fatal(w.Body.String())
				}
			} else {
				out := decode(t, w.Body.Bytes())
				if out["type"] != "error" || w.Header().Get("Content-Type") != "application/json" {
					t.Fatal(out)
				}
			}
		})
	}
	t.Run("EOF after finish", func(t *testing.T) {
		w := httptest.NewRecorder()
		New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(rw, "data: "+textChunk+"\n\ndata: "+finishChunk+"\n\n")
		})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.TrimSuffix(validRequest, "}")+`,"stream":true}`)))
		if !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") {
			t.Fatal(w.Body.String())
		}
	})
}
func TestStreamDegradesFromUnaryWhenSSEDisabled(t *testing.T) {
	w := httptest.NewRecorder()
	New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { fmt.Fprint(rw, unary) })).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.TrimSuffix(validRequest, "}")+`,"stream":true}`)))
	evs := events(t, w.Body.String())
	if evs[len(evs)-1].kind != "message_stop" {
		t.Fatal(w.Body.String())
	}
}

func TestStreamInitialUsageAndFinishReasons(t *testing.T) {
	for _, tc := range []struct{ finish, want string }{{"stop", "end_turn"}, {"length", "max_tokens"}, {"content_filter", "refusal"}, {"tool_calls", "tool_use"}, {"function_call", "tool_use"}} {
		w := httptest.NewRecorder()
		New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(rw, "data: {\"id\":\"c\",\"model\":\"m\",\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":0},\"choices\":[]}\n\n")
			fmt.Fprintf(rw, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n", tc.finish)
		})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.TrimSuffix(validRequest, "}")+`,"stream":true}`)))
		evs := events(t, w.Body.String())
		start := evs[0].data["message"].(map[string]any)["usage"].(map[string]any)
		if start["input_tokens"] != float64(11) || start["output_tokens"] != float64(0) {
			t.Fatal(start)
		}
		delta := evs[len(evs)-2].data
		if delta["delta"].(map[string]any)["stop_reason"] != tc.want {
			t.Fatal(delta)
		}
		u := delta["usage"].(map[string]any)
		if _, ok := u["input_tokens"]; ok {
			t.Fatal("input usage repeated", u)
		}
		if u["output_tokens"] != float64(3) {
			t.Fatal(u)
		}
	}
}
