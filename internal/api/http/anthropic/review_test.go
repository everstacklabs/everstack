package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewToolResults(t *testing.T) {
	for _, tc := range []struct {
		content string
		isError bool
		want    string
	}{{"", false, ""}, {`,"content":""`, false, ""}, {`,"content":[]`, false, ""}, {`,"content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]`, false, "one\ntwo"}, {`,"content":"failed"`, true, "Error: failed"}, {`,"content":"eRrOr: failed"`, true, "eRrOr: failed"}, {"", true, "Error: "}} {
		raw := fmt.Sprintf(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","is_error":%t%s}]}]}`, tc.isError, tc.content)
		out, _, err := translateRequest([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		msgs := decode(t, out)["messages"].([]any)
		m := msgs[0].(map[string]any)
		if len(msgs) != 1 || m["role"] != "tool" || m["tool_call_id"] != "a" || m["content"] != tc.want {
			t.Fatal(string(out))
		}
	}
	raw := `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":[{"type":"text","text":"caption"},{"type":"image","source":{"type":"url","url":"https://image"}}]}]}]}`
	w := httptest.NewRecorder()
	New(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unsupported image reached inner") })).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(raw)))
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"type":"invalid_request_error"`) || !strings.Contains(w.Body.String(), "images in tool results are not supported") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestReviewMalformedArguments(t *testing.T) {
	for _, args := range []string{"", `{}`, `{"ok":1}`, `{"bad":`, `undefined`} {
		for _, stream := range []bool{false, true} {
			malformed := args != "" && !json.Valid([]byte(args))
			w := httptest.NewRecorder()
			inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				call := object{"id": "a", "index": 0, "function": object{"name": "f", "arguments": args}}
				if stream {
					rw.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(rw, "data: "+textChunk+"\n\n")
					raw, _ := json.Marshal(object{"choices": []object{{"index": 0, "delta": object{"tool_calls": []object{call}}, "finish_reason": "tool_calls"}}})
					fmt.Fprintf(rw, "data: %s\n\ndata: [DONE]\n\n", raw)
				} else {
					raw, _ := json.Marshal(object{"choices": []object{{"message": object{"tool_calls": []object{call}}, "finish_reason": "tool_calls"}}})
					rw.Write(raw)
				}
			})
			body := validRequest
			if stream {
				body = strings.TrimSuffix(body, "}") + `,"stream":true}`
			}
			New(inner).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
			if malformed {
				if !strings.Contains(w.Body.String(), errMalformedToolArguments.Error()) || !strings.Contains(w.Body.String(), `"type":"api_error"`) {
					t.Fatal(w.Code, w.Body.String())
				}
				if stream {
					evs := events(t, w.Body.String())
					if evs[len(evs)-1].kind != "error" || strings.Contains(w.Body.String(), "input_json_delta") || strings.Contains(w.Body.String(), "message_stop") {
						t.Fatal(w.Body.String())
					}
				} else if w.Code != 502 {
					t.Fatal(w.Code, w.Body.String())
				}
			} else {
				if w.Code != 200 || strings.Contains(w.Body.String(), `"api_error"`) {
					t.Fatal(w.Code, w.Body.String())
				}
				if args == "" && stream && !strings.Contains(w.Body.String(), `"partial_json":"{}"`) {
					t.Fatal(w.Body.String())
				}
			}
		}
	}
}
func TestReviewUsageExcludesCached(t *testing.T) {
	for _, cached := range []int{0, 3, 20} {
		for _, initial := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				w := httptest.NewRecorder()
				u := object{"prompt_tokens": 10, "completion_tokens": 4, "prompt_tokens_details": object{"cached_tokens": cached}}
				inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					resp := object{"id": "c", "model": "m", "choices": []object{{"index": 0, "message": object{"content": "ok"}, "delta": object{"content": "ok"}, "finish_reason": "stop"}}, "usage": u}
					raw, _ := json.Marshal(resp)
					if !stream {
						rw.Write(raw)
						return
					}
					rw.Header().Set("Content-Type", "text/event-stream")
					if initial {
						fmt.Fprint(rw, `data: {"id":"c","model":"m","usage":{"prompt_tokens":10},"choices":[]}`+"\n\n")
					}
					fmt.Fprintf(rw, "data: %s\n\ndata: [DONE]\n\n", raw)
				})
				body := validRequest
				if stream {
					body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				}
				New(inner).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
				want := 10 - cached
				if want < 0 {
					want = 0
				}
				var usage map[string]any
				if stream {
					evs := events(t, w.Body.String())
					usage = evs[len(evs)-2].data["usage"].(map[string]any)
					if _, present := usage["input_tokens"]; !present {
						usage["input_tokens"] = evs[0].data["message"].(map[string]any)["usage"].(map[string]any)["input_tokens"]
					}
				} else {
					usage = decode(t, w.Body.Bytes())["usage"].(map[string]any)
				}
				if usage["input_tokens"] != float64(want) || usage["cache_read_input_tokens"] != float64(cached) {
					t.Fatal(usage)
				}
				if _, present := usage["cache_creation_input_tokens"]; present {
					t.Fatal("invented cache creation usage")
				}
			}
		}
	}
}
func TestReviewInnerBodyBounds(t *testing.T) {
	for _, status := range []int{200, 400} {
		for _, excess := range []bool{false, true} {
			w := httptest.NewRecorder()
			tw := &responseWriter{w: w, header: make(http.Header)}
			tw.WriteHeader(status)
			prefix := unary
			if status == 400 {
				prefix = `{"error":{"message":"safe"}}`
			}
			if _, err := tw.Write([]byte(prefix)); err != nil {
				t.Fatal(err)
			}
			remaining := MaxInnerBodyBytes - len(prefix)
			padding := strings.Repeat(" ", 1<<20)
			for remaining > 0 {
				n := min(remaining, len(padding))
				if _, err := io.WriteString(tw, padding[:n]); err != nil {
					t.Fatal(err)
				}
				remaining -= n
			}
			if excess {
				if _, err := tw.Write([]byte(" ")); err == nil {
					t.Fatal("oversized body accepted")
				}
				if tw.buffer.Len() > MaxInnerBodyBytes {
					t.Fatal("buffer exceeded cap")
				}
			}
			tw.finish()
			if excess {
				if w.Code != 502 || !strings.Contains(w.Body.String(), `"api_error"`) || !strings.Contains(w.Body.String(), "64 MiB") {
					t.Fatal(w.Code, w.Body.Len())
				}
			} else if w.Code != status {
				t.Fatal(w.Code, w.Body.Len())
			}
		}
	}
}
func TestReviewSSEEventBounds(t *testing.T) {
	for _, multiline := range []bool{false, true} {
		for _, excess := range []bool{false, true} {
			w := httptest.NewRecorder()
			tw := &responseWriter{w: w, header: http.Header{"Content-Type": []string{"text/event-stream"}}, streamWanted: true}
			io.WriteString(tw, "data: "+textChunk+"\n\n")
			payload := textChunk
			pad := MaxSSEEventBytes - len(payload)
			if multiline {
				pad--
			}
			if excess {
				pad++
			}
			var frame string
			if multiline {
				frame = "data: " + payload + "\ndata: " + strings.Repeat(" ", pad) + "\n\n"
			} else {
				frame = "data: " + payload + strings.Repeat(" ", pad) + "\n\n"
			}
			var err error
			for len(frame) > 0 {
				n := min(len(frame), 65536)
				_, err = io.WriteString(tw, frame[:n])
				frame = frame[n:]
				if err != nil {
					break
				}
			}
			if excess {
				if err == nil {
					t.Fatal("oversized event accepted")
				}
				if !strings.Contains(w.Body.String(), "event: error") || !strings.Contains(w.Body.String(), "8 MiB") || !strings.Contains(w.Body.String(), `"api_error"`) {
					t.Fatal(w.Body.Len())
				}
				if _, err = io.WriteString(tw, "data: [DONE]\n\n"); err == nil {
					t.Fatal("stream not closed")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				io.WriteString(tw, "data: "+finishChunk+"\n\ndata: [DONE]\n\n")
				if !strings.Contains(w.Body.String(), "message_stop") || strings.Contains(w.Body.String(), "event: error") {
					t.Fatal(w.Body.Len())
				}
			}
		}
	}
}
func TestReviewStopSequenceUnavailable(t *testing.T) {
	resp := chatResponse{}
	raw := `{"choices":[{"message":{"content":"done"},"finish_reason":"stop_sequence"}]}`
	json.Unmarshal([]byte(raw), &resp)
	msg, err := message(resp)
	if err != nil || msg["stop_reason"] != "end_turn" || msg["stop_sequence"] != nil {
		t.Fatal(msg, err)
	}
}

func TestReviewSSEPartialEventLimit(t *testing.T) {
	w := httptest.NewRecorder()
	tw := &responseWriter{w: w, header: http.Header{"Content-Type": []string{"text/event-stream"}}, streamWanted: true}
	io.WriteString(tw, "data: "+textChunk+"\n\n")
	if _, err := io.WriteString(tw, "data: "+strings.Repeat(" ", MaxSSEEventBytes/2)+"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, "data: "+strings.Repeat(" ", MaxSSEEventBytes/2-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, " "); err == nil {
		t.Fatal("partial event exceeded limit without a newline")
	}
	if !strings.Contains(w.Body.String(), "8 MiB") || !strings.Contains(w.Body.String(), "event: error") {
		t.Fatal(w.Body.Len())
	}
}

func TestReviewSSECRLFBoundary(t *testing.T) {
	w := httptest.NewRecorder()
	tw := &responseWriter{w: w, header: http.Header{"Content-Type": []string{"text/event-stream"}}, streamWanted: true}
	payload := textChunk + strings.Repeat(" ", MaxSSEEventBytes-len(textChunk))
	if _, err := io.WriteString(tw, "data: "+payload+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	io.WriteString(tw, "data: "+finishChunk+"\r\n\r\ndata: [DONE]\r\n\r\n")
	if !strings.Contains(w.Body.String(), "message_stop") || strings.Contains(w.Body.String(), "event: error") {
		t.Fatal(w.Body.Len())
	}
}

func TestReviewMalformedFirstToolStream(t *testing.T) {
	w := httptest.NewRecorder()
	New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(rw, `data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"f","arguments":"bad"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
	})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.TrimSuffix(validRequest, "}")+`,"stream":true}`)))
	evs := events(t, w.Body.String())
	if evs[len(evs)-1].kind != "error" || !strings.Contains(w.Body.String(), errMalformedToolArguments.Error()) || strings.Contains(w.Body.String(), "input_json_delta") || strings.Contains(w.Body.String(), "message_stop") {
		t.Fatal(w.Body.String())
	}
}
