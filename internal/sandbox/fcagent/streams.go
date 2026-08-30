package fcagent

import (
	"context"
	"errors"
	"io"
	"sync"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
)

// logsReader adapts a server-streaming Logs gRPC client to io.ReadCloser.
type logsReader struct {
	stream  grpc.ServerStreamingClient[fcpb.LogChunk]
	cancel  context.CancelFunc
	buf     []byte
	closed  bool
	mu      sync.Mutex
	readErr error
}

func newLogsReader(stream grpc.ServerStreamingClient[fcpb.LogChunk], cancel context.CancelFunc) *logsReader {
	return &logsReader{stream: stream, cancel: cancel}
}

func (r *logsReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for len(r.buf) == 0 {
		if r.closed {
			return 0, io.EOF
		}
		if r.readErr != nil {
			return 0, r.readErr
		}
		chunk, err := r.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				r.readErr = io.EOF
				return 0, io.EOF
			}
			r.readErr = err
			return 0, err
		}
		r.buf = chunk.Data
	}

	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *logsReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.cancel()
	return nil
}

// shellConn adapts a bidi Shell stream to io.ReadWriteCloser.
type shellConn struct {
	stream grpc.BidiStreamingClient[fcpb.ShellClientMessage, fcpb.ShellServerMessage]
	cancel context.CancelFunc

	readMu  sync.Mutex
	readBuf []byte
	readErr error
	// queued holds a server message that the caller read off the stream
	// before handing the conn to us (used to capture the leading
	// ShellSession metadata frame without losing the next stdout
	// frame). The Read loop drains this before calling Recv again.
	queued *fcpb.ShellServerMessage

	writeMu     sync.Mutex
	closed      bool
	exitCode    int32
	terminated  bool
}

func newShellConn(stream grpc.BidiStreamingClient[fcpb.ShellClientMessage, fcpb.ShellServerMessage], cancel context.CancelFunc) *shellConn {
	return &shellConn{stream: stream, cancel: cancel}
}

// queueServerMessage stashes a single already-Recv'd message so the
// Read loop hands it back next. Used by the backend's session-id
// capture path: it reads the first frame to look for ShellSession,
// and any non-ShellSession first frame (e.g. an eager Stdout) goes
// here so we don't drop it.
func (c *shellConn) queueServerMessage(msg *fcpb.ShellServerMessage) {
	c.readMu.Lock()
	c.queued = msg
	c.readMu.Unlock()
}

// SessionTerminated reports whether the most recent exit indicated
// the underlying persistent session is gone. Callers (gateway) use
// this to decide whether to surface "session ended" vs. silently
// reconnect on the next user keystroke.
func (c *shellConn) SessionTerminated() bool {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return c.terminated
}

func (c *shellConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	for len(c.readBuf) == 0 {
		if c.readErr != nil {
			return 0, c.readErr
		}
		var msg *fcpb.ShellServerMessage
		if c.queued != nil {
			msg = c.queued
			c.queued = nil
		} else {
			var err error
			msg, err = c.stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					c.readErr = io.EOF
					return 0, io.EOF
				}
				c.readErr = err
				return 0, err
			}
		}
		switch m := msg.Msg.(type) {
		case *fcpb.ShellServerMessage_Stdout:
			c.readBuf = m.Stdout.Data
		case *fcpb.ShellServerMessage_Exit:
			c.exitCode = m.Exit.Code
			c.terminated = m.Exit.SessionTerminated
			c.readErr = io.EOF
			return 0, io.EOF
		case *fcpb.ShellServerMessage_Session:
			// Late ShellSession frames (e.g. from a server that
			// re-emits on reattach) are informational and shouldn't
			// surface to the byte reader. Drop and keep looping.
		}
	}

	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *shellConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return 0, io.ErrClosedPipe
	}
	if err := c.stream.Send(&fcpb.ShellClientMessage{
		Msg: &fcpb.ShellClientMessage_Stdin{Stdin: &fcpb.ShellStdin{Data: append([]byte(nil), p...)}},
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *shellConn) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.stream.Send(&fcpb.ShellClientMessage{
		Msg: &fcpb.ShellClientMessage_Close{Close: &fcpb.ShellClose{}},
	})
	_ = c.stream.CloseSend()
	c.cancel()
	return nil
}

func (c *shellConn) resize(rows, cols uint16) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	return c.stream.Send(&fcpb.ShellClientMessage{
		Msg: &fcpb.ShellClientMessage_Resize{Resize: &fcpb.ShellResize{Rows: uint32(rows), Cols: uint32(cols)}},
	})
}
