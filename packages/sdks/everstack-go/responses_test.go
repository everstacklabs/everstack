package everstack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The frames a native (x-evs-api-key) caller gets from POST /v1/responses
// with stream:true: proto-JSON snapshots when the response's structure
// changes, delta frames (no status, no output) carrying only what one item
// grew by, and the full response last.
var responseStreamFrames = []string{
	`{"result":{"id":"resp_1","object":"response","status":"in_progress","output":[]}}`,
	`{"result":{"id":"resp_1","object":"response","status":"in_progress","output":[{"id":"msg_1","type":"message","content":[{"type":"text","text":"Hel"}]}]}}`,
	`{"result":{"id":"resp_1","object":"response","delta":{"item_id":"msg_1","content_delta":"lo"}}}`,
	`{"result":{"id":"resp_1","object":"response","status":"in_progress","output":[{"id":"msg_1","type":"message","content":[{"type":"text","text":"Hello"}]},{"id":"call_1","type":"function_call","name":"get_weather"}]}}`,
	`{"result":{"id":"resp_1","object":"response","delta":{"item_id":"call_1","output_index":1,"arguments_delta":"{\"city\":"}}}`,
	`{"result":{"id":"resp_1","object":"response","delta":{"item_id":"call_1","output_index":1,"arguments_delta":"\"Paris\"}"}}}`,
	`{"result":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","content":[{"type":"text","text":"Hello"}]},{"id":"call_1","type":"function_call","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}]}}`,
}

func serveResponseFrames(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			w.(http.Flusher).Flush()
		}
	}))
}

func TestResponsesStreamSurfacesDeltaFrames(t *testing.T) {
	srv := serveResponseFrames(t, responseStreamFrames)
	defer srv.Close()

	c := NewClient("pk_test", WithBaseURL(srv.URL))
	stream, err := c.Responses.CreateStream(context.Background(), &ResponseCreateParams{Model: "gpt-5.5"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var frames []map[string]any
	var text, args strings.Builder
	for stream.Next() {
		frame := stream.Current()
		frames = append(frames, frame)
		res, _ := frame["result"].(map[string]any)
		if d, ok := res["delta"].(map[string]any); ok {
			s, _ := d["content_delta"].(string)
			text.WriteString(s)
			a, _ := d["arguments_delta"].(string)
			args.WriteString(a)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if len(frames) != len(responseStreamFrames) {
		t.Fatalf("got %d frames, want %d", len(frames), len(responseStreamFrames))
	}
	if text.String() != "lo" {
		t.Errorf("text deltas = %q, want %q", text.String(), "lo")
	}
	if args.String() != `{"city":"Paris"}` {
		t.Errorf("argument deltas = %q", args.String())
	}
	last := frames[len(frames)-1]["result"].(map[string]any)
	if last["status"] != "completed" {
		t.Errorf("last frame status = %v, want completed", last["status"])
	}
}

// The final frame is the whole response, which for a long reply is far
// larger than bufio.Scanner's default 64 KiB line limit.
func TestResponsesStreamReadsALargeFinalFrame(t *testing.T) {
	long := strings.Repeat("x", 1<<20)
	final := `{"result":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","content":[{"type":"text","text":"` + long + `"}]}]}}`
	srv := serveResponseFrames(t, []string{responseStreamFrames[0], final})
	defer srv.Close()

	c := NewClient("pk_test", WithBaseURL(srv.URL))
	stream, err := c.Responses.CreateStream(context.Background(), &ResponseCreateParams{Model: "gpt-5.5"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	n := 0
	var last map[string]any
	for stream.Next() {
		n++
		last = stream.Current()
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if n != 2 {
		t.Fatalf("got %d frames, want 2", n)
	}
	out := last["result"].(map[string]any)["output"].([]any)
	part := out[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if got := len(part["text"].(string)); got != len(long) {
		t.Fatalf("final text length = %d, want %d", got, len(long))
	}
}
