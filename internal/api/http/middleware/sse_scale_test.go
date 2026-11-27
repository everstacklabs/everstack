package middleware

// Scaling benchmarks for sseWriter (EVS-83 Phase 2). The pre-fix Write
// re-scans the entire accumulated buffer on every call, so total framing
// cost grows quadratically with stream length. These benchmarks feed N
// downstream writes (one NDJSON line each, as grpc-gateway emits them) and
// measure total time: linear growth per chunk count = fixed; superlinear =
// the rescan defect. Run: go test ./internal/api/http/middleware/ -run XXX
// -bench 'BenchmarkSSEWriter' -benchtime 10x

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type captureWriter struct {
	header http.Header
	// events counts emitted SSE events (writes starting with "data: ").
	// A single event is 3 underlying writes ("data: " + payload + "\n\n"),
	// so counting raw writes would triple-count.
	events int
	bytes  int
	status int
}

func newCaptureWriter() *captureWriter       { return &captureWriter{header: http.Header{}} }
func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) WriteHeader(s int)   { c.status = s }
func (c *captureWriter) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), "data: ") {
		c.events++
	}
	c.bytes += len(p)
	return len(p), nil
}
func (c *captureWriter) Flush() {}

// protoFrame mimics one grpc-gateway NDJSON delta frame.
func protoFrame(id string, text string) string {
	return fmt.Sprintf("{\"result\":{\"id\":%q,\"model\":\"gpt-4o\",\"created\":1751600000,\"choices\":[{\"index\":0,\"message\":{\"content\":[{\"type\":\"text\",\"text\":%q}]}}]}}\n", id, text)
}

func BenchmarkSSEWriterProto(b *testing.B) {
	for _, n := range []int{50, 200, 800} {
		b.Run(fmt.Sprintf("chunks=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cap := newCaptureWriter()
				w := &sseWriter{ResponseWriter: cap, format: "proto"}
				for c := 0; c < n; c++ {
					if _, err := w.Write([]byte(protoFrame("chat_1", "tok "))); err != nil {
						b.Fatal(err)
					}
				}
				w.flushPending()
				if cap.events == 0 {
					b.Fatal("nothing written")
				}
			}
		})
	}
}

func BenchmarkSSEWriterOpenAI(b *testing.B) {
	for _, n := range []int{50, 200, 800} {
		b.Run(fmt.Sprintf("chunks=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cap := newCaptureWriter()
				w := &sseWriter{ResponseWriter: cap, format: "openai"}
				for c := 0; c < n; c++ {
					if _, err := w.Write([]byte(protoFrame("chat_1", "tok "))); err != nil {
						b.Fatal(err)
					}
				}
				w.flushPending()
			}
		})
	}
}

// TestSSEWriterSplitLines locks in split-write behavior: a downstream Write
// may split mid-line, and no event may emit until the line completes. The
// incremental rewrite must preserve this.
func TestSSEWriterSplitLines(t *testing.T) {
	cap := newCaptureWriter()
	w := &sseWriter{ResponseWriter: cap, format: "proto"}
	frame := protoFrame("chat_1", "hello")
	// Split at every possible point across trials would be thorough; a
	// mid-line and mid-frame split covers the buffering contract.
	for _, split := range []int{10, len(frame) / 2, len(frame) - 2} {
		cap = newCaptureWriter()
		w = &sseWriter{ResponseWriter: cap, format: "proto"}
		if _, err := w.Write([]byte(frame[:split])); err != nil {
			t.Fatal(err)
		}
		if cap.events != 0 {
			t.Fatalf("split %d: emitted %d events before line completed", split, cap.events)
		}
		if _, err := w.Write([]byte(frame[split:])); err != nil {
			t.Fatal(err)
		}
		if cap.events != 1 {
			t.Fatalf("split %d: expected 1 event after completion, got %d", split, cap.events)
		}
	}
}

// TestSSEWriterSkipsNonJSON guards the log-leak filter: stray non-JSON lines
// must not reach the client.
func TestSSEWriterSkipsNonJSON(t *testing.T) {
	cap := newCaptureWriter()
	w := &sseWriter{ResponseWriter: cap, format: "proto"}
	if _, err := w.Write([]byte("INFO some log line leaked into stream\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(protoFrame("chat_1", "ok"))); err != nil {
		t.Fatal(err)
	}
	if cap.events != 1 {
		t.Fatalf("expected 1 event (stray line dropped), got %d", cap.events)
	}
}
