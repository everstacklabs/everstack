package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseTestServer is a minimal MCP-style SSE server for transport tests.
// It exposes:
//
//	GET  /sse       — opens an SSE stream, immediately sends an `endpoint`
//	                  event pointing at /post, then forwards every payload
//	                  pushed to its `out` channel as a `message` event
//	                  (unless overridden via writeRaw).
//	POST /post      — accepts JSON-RPC requests; the test pushes the
//	                  desired response onto `out` and the server flushes
//	                  it to the open SSE stream.
type sseTestServer struct {
	t      *testing.T
	mu     sync.Mutex
	out    chan string // raw SSE bytes (already including trailing blank line)
	writer http.ResponseWriter
	flush  http.Flusher
	posted chan json.RawMessage
}

func newSSETestServer(t *testing.T) (*sseTestServer, *httptest.Server) {
	t.Helper()
	s := &sseTestServer{
		t:      t,
		out:    make(chan string, 16),
		posted: make(chan json.RawMessage, 16),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", s.handleSSE)
	mux.HandleFunc("/post", s.handlePost)
	srv := httptest.NewServer(mux)
	return s, srv
}

func (s *sseTestServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	s.mu.Lock()
	s.writer = w
	s.flush = flush
	s.mu.Unlock()

	// Send the endpoint event so the client can resolve the post URL.
	fmt.Fprintf(w, "event: endpoint\ndata: /post\n\n")
	flush.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-s.out:
			if !ok {
				return
			}
			io.WriteString(w, payload)
			flush.Flush()
		}
	}
}

func (s *sseTestServer) handlePost(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	s.posted <- body
	w.WriteHeader(http.StatusAccepted)
}

// sendMessage queues a `message`-event SSE frame for the next request.
func (s *sseTestServer) sendMessage(jsonRPCBody string) {
	s.out <- fmt.Sprintf("event: message\ndata: %s\n\n", jsonRPCBody)
}

// sendDefaultEvent sends a `data:`-only frame (no `event:` line) — the
// SSE spec says these should be treated as the default `message` event.
func (s *sseTestServer) sendDefaultEvent(jsonRPCBody string) {
	s.out <- fmt.Sprintf("data: %s\n\n", jsonRPCBody)
}

// sendRaw sends an arbitrary SSE frame (must include the trailing "\n\n").
func (s *sseTestServer) sendRaw(payload string) {
	s.out <- payload
}

func mustSend(t *testing.T, tp *sseTransport, method string) *JsonRpcResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := tp.Send(ctx, &JsonRpcRequest{Method: method})
	if err != nil {
		t.Fatalf("Send(%s) error = %v", method, err)
	}
	return resp
}

// idFromPosted reads the last POST body and returns the JSON-RPC id as a
// JSON number string. The server always echoes back the id the client
// sent — so we know what to put in the synthetic response.
func idFromPosted(t *testing.T, body json.RawMessage) string {
	t.Helper()
	var env struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal posted body: %v", err)
	}
	return string(env.ID)
}

func newConfiguredTransport(t *testing.T, srv *httptest.Server) *sseTransport {
	t.Helper()
	tp, err := newSSETransport(ServerConfig{
		URL:           srv.URL + "/sse",
		TransportType: TransportSSE,
	})
	if err != nil {
		t.Fatalf("newSSETransport: %v", err)
	}
	t.Cleanup(func() { _ = tp.Close() })
	return tp
}

// TestSSE_DefaultEventTypeIsMessage covers the most common production
// failure mode: a server that sends `data:` lines without an explicit
// `event: message` line. Per spec the default is "message" and the
// response must dispatch.
func TestSSE_DefaultEventTypeIsMessage(t *testing.T) {
	t.Parallel()
	s, srv := newSSETestServer(t)
	t.Cleanup(srv.Close) // registered before tp; runs second (LIFO) so tp.Close kills the SSE connection first
	tp := newConfiguredTransport(t, srv)

	// Reply when the client posts.
	go func() {
		body := <-s.posted
		s.sendDefaultEvent(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}`, idFromPosted(t, body)))
	}()

	resp := mustSend(t, tp, "tools/list")
	if resp == nil || len(resp.Result) == 0 {
		t.Fatalf("response missing or empty: %+v", resp)
	}
}

// TestSSE_IDFloatVsIntMatch verifies idKey normalization: client sends
// id=1 (int64), server echoes back "id":1 which decodes as float64,
// and the pending map lookup must still hit.
func TestSSE_IDFloatVsIntMatch(t *testing.T) {
	t.Parallel()
	s, srv := newSSETestServer(t)
	t.Cleanup(srv.Close) // registered before tp; runs second (LIFO) so tp.Close kills the SSE connection first
	tp := newConfiguredTransport(t, srv)

	go func() {
		body := <-s.posted
		s.sendMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"ok":true}}`, idFromPosted(t, body)))
	}()

	if _, err := tp.Send(context.Background(), &JsonRpcRequest{Method: "ping"}); err != nil {
		t.Fatalf("Send error = %v", err)
	}
}

// TestSSE_MultiLineDataJoined exercises the spec's data-line concatenation:
// repeated `data:` lines within one event are joined with "\n".
func TestSSE_MultiLineDataJoined(t *testing.T) {
	t.Parallel()
	s, srv := newSSETestServer(t)
	t.Cleanup(srv.Close) // registered before tp; runs second (LIFO) so tp.Close kills the SSE connection first
	tp := newConfiguredTransport(t, srv)

	go func() {
		body := <-s.posted
		id := idFromPosted(t, body)
		// Split the JSON across two data lines.
		s.sendRaw(fmt.Sprintf("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\ndata: \"result\":{\"x\":1}}\n\n", id))
	}()

	resp := mustSend(t, tp, "ping")
	var result struct {
		X int `json:"x"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result %s: %v", string(resp.Result), err)
	}
	if result.X != 1 {
		t.Fatalf("result.x = %d, want 1", result.X)
	}
}

// TestSSE_LargePayloadOver64KB verifies the parser doesn't choke on
// data larger than bufio.Scanner's default 64KB buffer (the original
// implementation silently dropped these).
func TestSSE_LargePayloadOver64KB(t *testing.T) {
	t.Parallel()
	s, srv := newSSETestServer(t)
	t.Cleanup(srv.Close) // registered before tp; runs second (LIFO) so tp.Close kills the SSE connection first
	tp := newConfiguredTransport(t, srv)

	go func() {
		body := <-s.posted
		id := idFromPosted(t, body)
		bigStr := strings.Repeat("a", 200_000) // 200KB
		s.sendMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"blob":%q}}`, id, bigStr))
	}()

	resp := mustSend(t, tp, "tools/list")
	var result struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Blob) != 200_000 {
		t.Fatalf("blob length = %d, want 200000", len(result.Blob))
	}
}

// TestSSE_CommentLinesIgnored ensures `:` heartbeat/comment lines that
// many SSE servers emit between real events don't trip the parser.
func TestSSE_CommentLinesIgnored(t *testing.T) {
	t.Parallel()
	s, srv := newSSETestServer(t)
	t.Cleanup(srv.Close) // registered before tp; runs second (LIFO) so tp.Close kills the SSE connection first
	tp := newConfiguredTransport(t, srv)

	go func() {
		body := <-s.posted
		id := idFromPosted(t, body)
		s.sendRaw(": keep-alive heartbeat\n\n")
		s.sendRaw(fmt.Sprintf(": another comment\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"ok\":true}}\n\n", id))
	}()

	resp := mustSend(t, tp, "ping")
	if resp == nil || len(resp.Result) == 0 {
		t.Fatalf("response missing")
	}
}

// TestSSE_LeadingSpaceStrippedOnce checks the SSE spec rule that exactly
// one optional leading space is stripped from a field value, not all
// surrounding whitespace.
func TestSSE_LeadingSpaceStrippedOnce(t *testing.T) {
	t.Parallel()
	s, srv := newSSETestServer(t)
	t.Cleanup(srv.Close) // registered before tp; runs second (LIFO) so tp.Close kills the SSE connection first
	tp := newConfiguredTransport(t, srv)

	go func() {
		body := <-s.posted
		id := idFromPosted(t, body)
		// `data:  {...}` — server inserts two spaces. The first is the
		// spec-mandated separator; the second is part of the value. The
		// leading-space-preserving JSON below remains valid because JSON
		// allows leading whitespace.
		s.sendRaw(fmt.Sprintf("event: message\ndata:  {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"ok\":true}}\n\n", id))
	}()

	resp := mustSend(t, tp, "ping")
	if resp == nil || len(resp.Result) == 0 {
		t.Fatalf("response missing")
	}
}

// TestSSE_ConnectFailsOnNon200 keeps the existing surfaced error path
// (deepwiki's 410 Gone is the canonical example).
func TestSSE_ConnectFailsOnNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	t.Cleanup(srv.Close)

	tp, err := newSSETransport(ServerConfig{URL: srv.URL, TransportType: TransportSSE})
	if err != nil {
		t.Fatalf("newSSETransport: %v", err)
	}
	defer tp.Close()

	_, err = tp.Send(context.Background(), &JsonRpcRequest{Method: "ping"})
	if err == nil || !strings.Contains(err.Error(), "410") {
		t.Fatalf("Send err = %v, want one mentioning 410", err)
	}
}

// TestSSE_IDKeyNormalization is a unit test for the helper. Ensures
// every shape an id can take after JSON round-tripping collapses to the
// same canonical key as the int64 we sent.
func TestSSE_IDKeyNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b interface{}
	}{
		{"int64 vs float64", int64(1), float64(1)},
		{"int vs float64", 1, float64(1)},
		{"int64 vs json.Number", int64(42), json.Number("42")},
		{"string ids stay equal", "abc", "abc"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if idKey(c.a) != idKey(c.b) {
				t.Fatalf("idKey(%v) = %q, idKey(%v) = %q — want equal", c.a, idKey(c.a), c.b, idKey(c.b))
			}
		})
	}
}
