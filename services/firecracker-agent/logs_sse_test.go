package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// nopReadCloser wraps a Reader as a ReadCloser without affecting Close
// behavior. Test-only stand-in for the backend's Logs return value.
type nopReadCloser struct{ io.Reader }

func (nopReadCloser) Close() error { return nil }

// driveSSE wires a test ReadCloser through streamSSE and returns the
// response body that would have been written to a client. Useful for
// asserting on exact wire bytes.
func driveSSE(t *testing.T, body io.ReadCloser) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/logs/test-vm", nil)
	rec := httptest.NewRecorder()
	streamSSE(rec, req, body)
	return rec
}

func TestSSE_HeadersAreSet(t *testing.T) {
	rec := driveSSE(t, nopReadCloser{strings.NewReader("")})
	want := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-store",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestSSE_SnapshotLinesAreEmittedAsEvents(t *testing.T) {
	// Body is what the backend's Logs pipe would deliver: three
	// newline-separated text entries. SSE output should be three
	// "data: ...\n\n" events.
	body := nopReadCloser{strings.NewReader("first line\nsecond line\nthird line\n")}
	rec := driveSSE(t, body)
	got := rec.Body.String()
	want := "data: first line\n\ndata: second line\n\ndata: third line\n\n"
	if got != want {
		t.Errorf("body mismatch\n  got:  %q\n  want: %q", got, want)
	}
}

func TestSSE_EmptyLinesAreSkipped(t *testing.T) {
	body := nopReadCloser{strings.NewReader("a\n\n\nb\n")}
	rec := driveSSE(t, body)
	if got := rec.Body.String(); got != "data: a\n\ndata: b\n\n" {
		t.Errorf("empty-line skipping broken: %q", got)
	}
}

func TestSSE_PartialLineWaitsForNewline(t *testing.T) {
	// If the reader returns a chunk that doesn't end on a newline,
	// streamSSE must buffer those bytes and wait. A subsequent
	// chunk that finishes the line produces one event.
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("incomplete "))
		_, _ = pw.Write([]byte("but now complete\n"))
		_ = pw.Close()
	}()
	rec := driveSSE(t, pr)
	if got := rec.Body.String(); got != "data: incomplete but now complete\n\n" {
		t.Errorf("partial-line handling: %q", got)
	}
}

func TestSSE_ClientDisconnectStopsStream(t *testing.T) {
	// A live SSE handler should observe context cancellation and
	// return promptly. We give it a context that we cancel from
	// outside, simulating browser close / gateway drop.
	pr, pw := io.Pipe()
	// Goroutine emits a line then hangs — gives the handler one
	// event to flush, then nothing. The handler should block in
	// pr.Read; context cancellation should unblock it.
	go func() {
		_, _ = pw.Write([]byte("alive\n"))
		// hold the writer open so Read blocks
	}()
	defer pw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/logs/test-vm", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		streamSSE(rec, req, pr)
		close(done)
	}()

	// Give the handler a moment to emit the first event.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// good — handler returned within budget
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after ctx cancel")
	}

	if !strings.Contains(rec.Body.String(), "data: alive") {
		t.Errorf("expected first event before cancel: %q", rec.Body.String())
	}
}

func TestParseLogsOpts_Defaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/logs/x", nil)
	opts := parseLogsOpts(r)
	if opts.Tail != 100 {
		t.Errorf("default tail = %d, want 100", opts.Tail)
	}
	if !opts.Follow {
		t.Error("default follow = false, want true")
	}
	if opts.Timestamps {
		t.Error("default timestamps = true, want false")
	}
}

func TestParseLogsOpts_QueryOverrides(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet,
		"/logs/x?tail=50&follow=0&timestamps=1&since=1700000000", nil)
	opts := parseLogsOpts(r)
	if opts.Tail != 50 {
		t.Errorf("tail = %d, want 50", opts.Tail)
	}
	if opts.Follow {
		t.Error("follow query=0 ignored")
	}
	if !opts.Timestamps {
		t.Error("timestamps query=1 ignored")
	}
	if opts.Since.Unix() != 1700000000 {
		t.Errorf("since = %d, want 1700000000", opts.Since.Unix())
	}
}

func TestParseLogsOpts_RejectsGarbage(t *testing.T) {
	// Bogus values fall through to defaults rather than tripping
	// the handler. Defensive — a typo in the URL shouldn't break
	// the stream.
	r := httptest.NewRequest(http.MethodGet,
		"/logs/x?tail=abc&since=xyz&follow=maybe", nil)
	opts := parseLogsOpts(r)
	if opts.Tail != 100 {
		t.Errorf("bad tail value should fall through, got %d", opts.Tail)
	}
	if opts.Follow {
		t.Error("bad follow value should fall through to false (parseBoolQuery default)")
	}
}
