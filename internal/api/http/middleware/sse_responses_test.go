package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func eventNames(evs []responsesEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.name
	}
	return out
}

// Snapshots that grow the text yield one delta per growth, sequence numbers
// run across frames, and the terminal snapshot closes items then the response.
func TestResponsesStreamStateGrowingSnapshots(t *testing.T) {
	st := &responsesStreamState{}
	var all []responsesEvent
	all = append(all, st.events(`{"result":{"id":"r","object":"response","created_at":"5","status":"in_progress","model":"m"}}`)...)
	all = append(all, st.events(`{"result":{"id":"r","object":"response","status":"in_progress","output":[{"id":"i","type":"message","status":"in_progress","role":3,"content":[{"type":"text","text":"hel"}]}]}}`)...)
	all = append(all, st.events(`{"result":{"id":"r","object":"response","status":"in_progress","output":[{"id":"i","type":"message","status":"in_progress","role":3,"content":[{"type":"text","text":"hello"}]}]}}`)...)
	all = append(all, st.events(`{"result":{"id":"r","object":"response","status":"completed","output":[{"id":"i","type":"message","status":"completed","role":3,"content":[{"type":"text","text":"hello"}]},{"id":"fc","type":"function_call","status":"completed","call_id":"c","name":"f","arguments":"{}"}]}}`)...)

	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added", "response.output_text.delta",
		"response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.output_item.added", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.output_item.done",
		"response.completed",
	}
	if got := eventNames(all); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}
	var deltas []string
	for i, e := range all {
		var m map[string]interface{}
		if err := json.Unmarshal(e.data, &m); err != nil {
			t.Fatalf("event %d not JSON: %s", i, e.data)
		}
		if m["type"] != e.name || int(m["sequence_number"].(float64)) != i {
			t.Fatalf("event %d: type %v seq %v", i, m["type"], m["sequence_number"])
		}
		if e.name == "response.output_text.delta" {
			deltas = append(deltas, m["delta"].(string))
		}
	}
	if strings.Join(deltas, "|") != "hel|lo" {
		t.Fatalf("deltas = %v", deltas)
	}
	// Nothing after the terminal event.
	if more := st.events(`{"result":{"id":"r","object":"response","status":"completed"}}`); len(more) != 0 {
		t.Fatalf("events after terminal: %v", eventNames(more))
	}
}

// A failed response ends with response.failed carrying its error; a
// transport error frame becomes an `error` event.
func TestResponsesStreamStateFailures(t *testing.T) {
	st := &responsesStreamState{}
	evs := st.events(`{"result":{"id":"r","object":"response","status":"failed","error":{"code":"chat_error","message":"boom"}}}`)
	names := eventNames(evs)
	if names[len(names)-1] != "response.failed" || !strings.Contains(string(evs[len(evs)-1].data), `"message":"boom"`) {
		t.Fatalf("failed response: %v %s", names, evs[len(evs)-1].data)
	}

	st = &responsesStreamState{}
	evs = st.events(`{"error":{"code":13,"message":"upstream exploded"}}`)
	if len(evs) != 1 || evs[0].name != "error" || !strings.Contains(string(evs[0].data), "upstream exploded") {
		t.Fatalf("error frame: %v", eventNames(evs))
	}
}

// An error status from downstream before any stream output passes through
// as JSON instead of being turned into an empty event stream.
func TestSSENegotiatorPassesErrorStatusThrough(t *testing.T) {
	for _, format := range []string{"openai", SSEFormatOpenAIResponses, "proto"} {
		h := WrapWithSSENegotiationWith(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":401,"message":"Invalid API key"}}`)
		}), func(*http.Request) string { return format })
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)))
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != "application/json" ||
			rec.Body.String() != `{"error":{"code":401,"message":"Invalid API key"}}` {
			t.Fatalf("%s: status %d, type %q, body %q", format, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
}

// The responses format writes named events and no chat [DONE] sentinel.
func TestSSENegotiatorResponsesFormatFraming(t *testing.T) {
	h := WrapWithSSENegotiationWith(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":{"id":"r","object":"response","status":"completed","output":[]}}`+"\n")
	}), func(*http.Request) string { return SSEFormatOpenAIResponses })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)))
	body := rec.Body.String()
	if rec.Header().Get("Content-Type") != "text/event-stream" ||
		!strings.HasPrefix(body, "event: response.created\ndata: {") ||
		!strings.Contains(body, "event: response.completed\ndata: ") || strings.Contains(body, "[DONE]") {
		t.Fatalf("framing: %q", body)
	}
}

// Delta frames carry only the fragment: each becomes one delta event, the
// running text is kept here, and the done events and a final snapshot that
// restates the text produce no duplicate delta.
func TestResponsesStreamStateDeltaFrames(t *testing.T) {
	st := &responsesStreamState{}
	var all []responsesEvent
	for _, line := range []string{
		`{"result":{"id":"r","object":"response","created_at":"5","status":"in_progress","model":"m"}}`,
		`{"result":{"id":"r","object":"response","status":"in_progress","output":[{"id":"i","type":"message","status":"in_progress","role":3,"content":[{"type":"text","text":"Hel"}]}]}}`,
		`{"result":{"id":"r","object":"response","delta":{"item_id":"i","content_delta":"lo"}}}`,
		`{"result":{"id":"r","object":"response","delta":{"item_id":"i","content_delta":" world"}}}`,
		`{"result":{"id":"r","object":"response","status":"in_progress","output":[{"id":"i","type":"message","status":"in_progress","role":3,"content":[{"type":"text","text":"Hello world"}]},{"id":"fc","type":"function_call","status":"in_progress","call_id":"fc","name":"f","arguments":"{\"a\":"}]}}`,
		`{"result":{"id":"r","object":"response","delta":{"item_id":"fc","output_index":1,"arguments_delta":"1}"}}}`,
		// A delta naming an item never announced is dropped.
		`{"result":{"id":"r","object":"response","delta":{"item_id":"x","output_index":7,"content_delta":"lost"}}}`,
		`{"result":{"id":"r","object":"response","status":"completed","output":[{"id":"i","type":"message","status":"completed","role":3,"content":[{"type":"text","text":"Hello world"}]},{"id":"fc","type":"function_call","status":"completed","call_id":"fc","name":"f","arguments":"{\"a\":1}"}]}}`,
	} {
		all = append(all, st.events(line)...)
	}
	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added", "response.output_text.delta",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_item.added", "response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.function_call_arguments.done", "response.output_item.done",
		"response.completed",
	}
	if got := eventNames(all); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}
	var text, args []string
	for i, e := range all {
		var m map[string]interface{}
		if err := json.Unmarshal(e.data, &m); err != nil || int(m["sequence_number"].(float64)) != i {
			t.Fatalf("event %d: %s", i, e.data)
		}
		switch e.name {
		case "response.output_text.delta":
			text = append(text, m["delta"].(string))
		case "response.function_call_arguments.delta":
			args = append(args, m["delta"].(string))
		case "response.output_text.done":
			if m["text"] != "Hello world" {
				t.Fatalf("done text = %v", m["text"])
			}
		}
	}
	if strings.Join(text, "|") != "Hel|lo| world" || strings.Join(args, "|") != `{"a":|1}` {
		t.Fatalf("deltas: text %q args %q", text, args)
	}
}
