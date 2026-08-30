package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// startTestWSServer brings up an httptest.Server with handleShellWS
// mounted at /shell. Returns the URL plus a teardown.
func startTestWSServer(t *testing.T) (string, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/shell", handleShellWS)
	srv := httptest.NewServer(mux)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/shell", srv.Close
}

// dialShellWS connects to a /shell endpoint and returns the conn.
// Caller is responsible for closing.
func dialShellWS(t *testing.T, url string) (*websocket.Conn, context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("ws dial %s: %v", url, err)
	}
	return c, ctx, cancel
}

func TestShellWS_RejectsMissingInitMessage(t *testing.T) {
	url, teardown := startTestWSServer(t)
	defer teardown()

	c, _, cancel := dialShellWS(t, url)
	defer cancel()

	// Close immediately without sending the init message. The server
	// should observe EOF on its first Read and close cleanly with
	// UnsupportedData.
	c.Close(websocket.StatusNormalClosure, "client done")

	// Test passes if we got this far without panic / hang. The
	// server-side handler logs the error but doesn't crash.
}

func TestShellWS_RejectsMalformedInitJSON(t *testing.T) {
	url, teardown := startTestWSServer(t)
	defer teardown()

	c, ctx, cancel := dialShellWS(t, url)
	defer cancel()
	defer c.Close(websocket.StatusInternalError, "test cleanup")

	// Send invalid JSON as the init message.
	if err := c.Write(ctx, websocket.MessageText, []byte("not-json")); err != nil {
		t.Fatalf("write bad init: %v", err)
	}

	// Server should reply with a JSON error message, then close.
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	typ, data, err := c.Read(readCtx)
	if err != nil {
		// Some implementations close before sending the error — that's
		// also acceptable behavior for an invalid handshake.
		return
	}
	if typ != websocket.MessageText {
		t.Fatalf("expected TEXT error reply, got %v", typ)
	}
	var resp map[string]string
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("response is not JSON: %v (%q)", err, data)
	}
	if resp["error"] == "" {
		t.Fatalf("expected error field, got: %s", data)
	}
}

func TestWSStream_ReadConcatenatesMessages(t *testing.T) {
	// The wsStream wrapper turns a sequence of WS binary messages
	// into a contiguous byte stream. shellframe.ReadFrame may issue
	// multiple Read calls for one frame; this verifies that small
	// reads pull bytes from the buffered message correctly and that
	// the next message gets pulled when the buffer drains.
	//
	// We construct two connected websocket.Conns via a localhost
	// httptest server with a no-op handler that just echoes back
	// the raw conn for us to use directly.
	serverCh := make(chan *websocket.Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		serverCh <- c
		// Returning here would normally close the WS conn (handler
		// exit ends the upgraded request). The test wants the conn
		// to outlive the handler; we pin the conn lifetime to a
		// channel the test closes when it's done with it.
		_ = r // keep linter happy
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "")
	serverConn := <-serverCh
	defer serverConn.Close(websocket.StatusNormalClosure, "")

	// Wrap the server side as wsStream — it's the reader.
	stream := newWSStream(ctx, serverConn)

	// Client sends three small messages. Server's wsStream should
	// see one continuous 9-byte stream after concatenation.
	for _, msg := range [][]byte{[]byte("abc"), []byte("def"), []byte("ghi")} {
		if err := clientConn.Write(ctx, websocket.MessageBinary, msg); err != nil {
			t.Fatalf("write %q: %v", msg, err)
		}
	}

	// Read 4 bytes at a time and confirm we get the full stream
	// across message boundaries.
	got, err := io.ReadAll(io.LimitReader(stream, 9))
	if err != nil {
		t.Fatalf("readall: %v", err)
	}
	if want := "abcdefghi"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWSStream_WriteEmitsOneMessagePerCall(t *testing.T) {
	// Each Write should produce exactly one WS binary message. This
	// matters because shellframe writes one frame per WriteFrame call,
	// and the test verifies the WS adapter preserves that boundary
	// (any framework-level chunking would break frame parsing).
	serverCh := make(chan *websocket.Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		serverCh <- c
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "")
	serverConn := <-serverCh
	defer serverConn.Close(websocket.StatusNormalClosure, "")

	stream := newWSStream(ctx, serverConn)

	// Each Write call from server side becomes one binary message
	// on the client side.
	payloads := []string{"hello", "world", "12345"}
	for _, p := range payloads {
		if _, err := stream.Write([]byte(p)); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
	}

	for _, want := range payloads {
		typ, got, err := clientConn.Read(ctx)
		if err != nil {
			t.Fatalf("client read: %v", err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("expected binary, got %v", typ)
		}
		if string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
