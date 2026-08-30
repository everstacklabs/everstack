package main

// WebSocket transport for shell sessions. Phase 5a: this endpoint
// runs alongside the existing vsock shell_open handler. The host's
// fcagent keeps dialing vsock by default; the WS path is dormant
// until Phase 5b flips the fcagent dialer over.
//
// Protocol on the wire:
//
//  1. Client opens ws://<guest-ip>:8080/shell.
//  2. First frame is a TEXT message containing shellOpenParams JSON.
//  3. Server replies with a TEXT message: either {"session_id":"...",
//     "reattached":true} on success or {"error":"..."} on failure.
//  4. After ack the connection carries shellframe binary frames in
//     both directions, exactly the same byte format the vsock path
//     uses. WS message boundaries are irrelevant — the wsStream
//     wrapper exposes the conn as a byte stream and shellframe
//     handles its own length prefixing.
//
// We picked binary-message byte-stream over "one shellframe per WS
// message" because (a) reuse of shellframe.ReadFrame as-is, (b) the
// host's writer code stays transport-agnostic — it just calls
// WriteFrame against an io.Writer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// handleShellWS is the HTTP handler for /shell. Mounted by
// startHTTPServer (http.go).
func handleShellWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// CompressionContextTakeover would be a small bandwidth win
		// on stdout streams but adds CPU on every frame; the in-guest
		// agent is single-core so we skip it.
		CompressionMode: websocket.CompressionDisabled,
		// InsecureSkipVerify equivalent: we don't enforce origin
		// because the only caller is the fcagent on the host side,
		// not a browser. Same trust model as vsock.
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Accept handles the HTTP response on its own — we don't
		// need to call WriteHeader.
		fmt.Fprintf(os.Stderr, "sandbox-agent: ws accept: %v\n", err)
		return
	}
	// Default close reason; can be overridden when we know more.
	defer c.Close(websocket.StatusNormalClosure, "")

	// Per-message read budget. The first message must be the init
	// JSON; subsequent messages carry binary frames. We don't cap
	// individual messages — shellframe enforces its own per-frame
	// payload limit, and an over-large WS message would just fail
	// to parse as a frame and we'd tear down.
	//
	// Per-session budget caps the whole conn at the duration of a
	// shell. No timeout here — interactive shells stay open for
	// hours. tmux keepalive plus the kernel's TCP keepalive cover
	// dead-peer detection.
	ctx := r.Context()

	// Init handshake: read one TEXT message, expect shellOpenParams.
	_, raw, err := c.Read(ctx)
	if err != nil {
		_ = c.Close(websocket.StatusUnsupportedData, "missing init message")
		return
	}
	var params shellOpenParams
	if err := json.Unmarshal(raw, &params); err != nil {
		_ = wsSendJSON(ctx, c, map[string]string{"error": "invalid init JSON: " + err.Error()})
		_ = c.Close(websocket.StatusUnsupportedData, "bad init")
		return
	}

	// Ack callback: writes the response (or error) as a TEXT message
	// so the host can distinguish JSON control from binary frames.
	ack := func(resp *shellOpenResponse, err error) error {
		if err != nil {
			return wsSendJSON(ctx, c, map[string]string{"error": err.Error()})
		}
		return wsSendJSON(ctx, c, resp)
	}

	stream := newWSStream(ctx, c)
	defer stream.close()

	runShellSession(params, ack, stream, stream)
}

// wsSendJSON writes a single JSON TEXT message. Used for the init
// ack and for error replies before we hand off to runShellSession.
func wsSendJSON(ctx context.Context, c *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, data)
}

// wsStream adapts a *websocket.Conn to io.Reader + io.Writer so the
// transport-agnostic runShellSession + shellframe library can use it
// without knowing it's WS.
//
// Read: pulls binary messages from the conn, buffers them, returns
// bytes on demand. shellframe.ReadFrame may call Read several times
// to assemble one frame (1 byte type + 4 byte length + N byte
// payload); the buffer handles that fragmentation invisibly.
//
// Write: each Write call sends one WS binary message. shellframe's
// WriteFrame call sites already write atomically (single Write per
// frame with type+len+payload concatenated), so this maps cleanly:
// one shellframe = one WS message.
type wsStream struct {
	ctx context.Context
	c   *websocket.Conn

	// Pending bytes from the last incoming WS message that haven't
	// been Read out yet. Refilled by the next c.Read when empty.
	rmu     sync.Mutex
	pending []byte

	wmu sync.Mutex // serializes c.Write so concurrent goroutines don't interleave
}

func newWSStream(ctx context.Context, c *websocket.Conn) *wsStream {
	return &wsStream{ctx: ctx, c: c}
}

func (s *wsStream) Read(p []byte) (int, error) {
	s.rmu.Lock()
	defer s.rmu.Unlock()

	for len(s.pending) == 0 {
		typ, data, err := s.c.Read(s.ctx)
		if err != nil {
			// Map websocket close codes onto io.EOF so callers can
			// distinguish "client done" from real errors using the
			// stdlib idiom.
			if websocket.CloseStatus(err) != -1 {
				return 0, io.EOF
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return 0, io.EOF
			}
			return 0, err
		}
		if typ != websocket.MessageBinary {
			// TEXT messages after the init handshake are out-of-spec
			// for this protocol. Drop them rather than tearing down —
			// a future-version host may extend with control messages.
			continue
		}
		s.pending = data
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *wsStream) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	// Use a per-write timeout so a stuck client can't wedge the pump
	// goroutine forever. 30s is generous — even a slow client should
	// drain a 32KB stdout chunk within that.
	wctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if err := s.c.Write(wctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// close signals end-of-session. Caller invokes from a defer so we
// always send a normal-closure code even on early returns. The
// underlying *websocket.Conn.Close call in handleShellWS is the
// authoritative close — this is a hook for future cleanup (e.g.
// flushing in-flight metrics) that's idempotent for now.
func (s *wsStream) close() {
	// no-op for Phase 5a; reserved for future cleanup
}
