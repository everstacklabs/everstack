package firecracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/firecracker/shellframe"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
)

// firecrackerShellConn is a thin adapter that exposes the
// gateway-facing io.ReadWriteCloser contract on top of a vsock conn
// running the shellframe binary protocol.
//
// Why this is just a pipe now: the previous implementation parsed
// xterm bytes on the host (splitting on \r, handling backspace, etc.)
// and synthesized one-shot exec calls per command. That broke pasting,
// killed TUIs, made `cd` a hack, and dropped all PTY semantics. The
// real shell now runs in a guest-side PTY and we just shovel bytes.
//
// The adapter handles three things only:
//
//   - Read: surfaces TypeStdout frame payloads to whoever's reading
//     the gateway side (xterm via the fcagent grpc stream).
//   - Write: wraps incoming xterm bytes in TypeStdin frames.
//   - Resize: encodes window size into a TypeResize control frame.
//
// All shell semantics — line editing, history, signals, TUIs — live
// in the guest, exactly like ssh.
type firecrackerShellConn struct {
	ctx       context.Context
	cancel    context.CancelFunc
	backend   *FirecrackerBackend
	sandboxID string

	conn net.Conn

	// readPipe surfaces stdout-frame payloads to the consumer. We
	// write to readPipeW from the reader goroutine and the gateway
	// reads from readPipeR. An io.Pipe gives us back-pressure for
	// free: if the gateway is slow, the reader goroutine blocks
	// instead of buffering unbounded.
	readPipeR *io.PipeReader
	readPipeW *io.PipeWriter

	// writeMu serializes writes to conn. Stdin frames (gateway →
	// guest) and resize frames (control) both write; without the mutex
	// their frames could interleave on the wire and produce garbage.
	writeMu sync.Mutex

	closeOnce sync.Once
}

func newFirecrackerShellConn(
	parent context.Context,
	backend *FirecrackerBackend,
	sandboxID string,
	conn net.Conn,
) *firecrackerShellConn {
	ctx, cancel := context.WithCancel(parent)
	r, w := io.Pipe()
	c := &firecrackerShellConn{
		ctx:       ctx,
		cancel:    cancel,
		backend:   backend,
		sandboxID: sandboxID,
		conn:      conn,
		readPipeR: r,
		readPipeW: w,
	}

	// Note the shell session in the per-sandbox log buffer so the Logs
	// tab shows *something* the moment a user opens the shell — without
	// this, a manual sandbox with no Exec activity reads as "no logs
	// yet" forever.
	backend.getOrCreateLogs(sandboxID).Append(logbuffer.Entry{
		Timestamp: time.Now(),
		Stream:    "system",
		Line:      "shell session opened",
	})

	go c.readLoop()
	go func() {
		<-ctx.Done()
		_ = c.Close()
	}()
	return c
}

// Read returns bytes from the guest PTY (xterm display path).
func (c *firecrackerShellConn) Read(p []byte) (int, error) {
	return c.readPipeR.Read(p)
}

// Write sends bytes to the guest PTY's master (xterm keystrokes path).
func (c *firecrackerShellConn) Write(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, io.EOF
	}
	if err := c.writeFrame(shellframe.TypeStdin, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize forwards a window-size change to the guest. Errors are
// non-fatal — a missed resize is a redraw issue, not a session-killer.
func (c *firecrackerShellConn) Resize(rows, cols uint16) error {
	if c.ctx.Err() != nil {
		return io.EOF
	}
	return c.writeFrame(shellframe.TypeResize, shellframe.EncodeResize(rows, cols))
}

// Close tears down both directions. Idempotent — the gateway may
// call this on its own cancel path, and the read loop may call it on
// guest EOF; we want exactly one cleanup.
func (c *firecrackerShellConn) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		_ = c.conn.Close()
		_ = c.readPipeW.Close()
		_ = c.readPipeR.Close()
	})
	return nil
}

func (c *firecrackerShellConn) writeFrame(t shellframe.Type, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.ctx.Err() != nil {
		return io.EOF
	}
	return shellframe.WriteFrame(c.conn, t, payload)
}

// readLoop pumps guest → gateway. Each TypeStdout frame's payload is
// written verbatim to readPipe; each TypeExit frame's payload is
// logged and the session is closed.
func (c *firecrackerShellConn) readLoop() {
	defer c.Close()
	for {
		f, err := shellframe.ReadFrame(c.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && c.ctx.Err() == nil {
				c.backend.getOrCreateLogs(c.sandboxID).Append(logbuffer.Entry{
					Timestamp: time.Now(),
					Stream:    "system",
					Line:      fmt.Sprintf("shell read: %v", err),
				})
			}
			return
		}
		switch f.Type {
		case shellframe.TypeStdout:
			if _, err := c.readPipeW.Write(f.Payload); err != nil {
				return
			}
			// Tee the guest's PTY output into the per-sandbox log buffer
			// so the Logs tab reflects shell activity in (near) real time.
			// Strip terminal control sequences first, then skip the entry if
			// nothing printable remains: a frame that is purely cursor-move
			// or redraw escapes has len(payload) > 0 but strips to "" (or
			// whitespace), which would otherwise emit a blank log line.
			if line := stripControl(f.Payload); strings.TrimSpace(line) != "" {
				c.backend.getOrCreateLogs(c.sandboxID).Append(logbuffer.Entry{
					Timestamp: time.Now(),
					Stream:    "shell",
					Line:      line,
				})
			}
		case shellframe.TypeScrollback:
			// Write scrollback bytes directly to the pipe as-is. These are
			// pre-existing terminal history from tmux capture-pane, sent
			// once by the guest right after the ack on a reattach. The
			// gateway forwards them verbatim to xterm.js, which re-renders
			// the last N lines so the user sees continuity across a reconnect.
			// We do NOT tee into the log buffer — these bytes are replayed
			// history, not new activity.
			if len(f.Payload) > 0 {
				if _, err := c.readPipeW.Write(f.Payload); err != nil {
					return
				}
			}
		case shellframe.TypeExit:
			code, _ := shellframe.DecodeExit(f.Payload)
			c.backend.getOrCreateLogs(c.sandboxID).Append(logbuffer.Entry{
				Timestamp: time.Now(),
				Stream:    "system",
				Line:      fmt.Sprintf("shell session ended (exit %d)", code),
			})
			return
		default:
			// Unknown frame type from the guest. A future-version guest
			// might add new types; drop unknown frames silently so we
			// don't break the session.
		}
	}
}

// stripControl produces a printable-friendly approximation of PTY
// bytes for the log buffer. We keep tabs + newlines (so each emitted
// line in logs looks like an actual line) but drop the ANSI/CSI/OSC
// terminal escape sequences that would otherwise junk up the Logs tab.
// The shell itself still gets the raw bytes.
//
// The previous implementation skipped only ONE byte after ESC, which
// left the body of every variable-length CSI sequence in the output —
// e.g. ESC[?25l → "?25l", ESC[1;24r → "1;24r", ESC[30m → "30m",
// ESC[K → "K" — exactly the noise this is meant to remove. We now parse
// whole sequences.
//
// Limitation: stripping is per-payload; an escape sequence split across
// two frames leaves a small residue. Frames are whole PTY writes in
// practice, so this is rare; carry parser state on the conn if it ever
// matters.
func stripControl(b []byte) string {
	out := make([]byte, 0, len(b))
	i, n := 0, len(b)
	for i < n {
		r := b[i]
		switch {
		case r == 0x1b: // ESC — start of an escape sequence
			i++
			if i >= n {
				break
			}
			switch b[i] {
			case '[': // CSI: ESC [ (params 0x30-0x3f)(interm 0x20-0x2f) final(0x40-0x7e)
				i++
				for i < n && (b[i] < 0x40 || b[i] > 0x7e) {
					i++
				}
				i++ // consume the final byte (or no-op past end if truncated)
			case ']', 'P', 'X', '^', '_': // OSC / DCS / SOS / PM / APC: string until BEL or ST (ESC \)
				i++
				for i < n {
					if b[i] == 0x07 { // BEL
						i++
						break
					}
					if b[i] == 0x1b && i+1 < n && b[i+1] == '\\' { // ST
						i += 2
						break
					}
					i++
				}
			case '(', ')', '*', '+': // charset designation: ESC ( <one byte>
				i += 2
			default: // simple two-byte ESC (ESC =, ESC >, ESC c, ESC M, ...)
				i++
			}
		case r == '\n' || r == '\r' || r == '\t':
			out = append(out, r)
			i++
		case r < 0x20 || r == 0x7f: // other C0 controls + DEL
			i++
		default:
			out = append(out, r)
			i++
		}
	}
	return string(out)
}
