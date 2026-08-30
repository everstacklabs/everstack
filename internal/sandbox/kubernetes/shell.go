package kubernetes

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// terminalSizeQueue implements remotecommand.TerminalSizeQueue using a channel.
type terminalSizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{
		ch: make(chan remotecommand.TerminalSize, 4),
	}
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

// shellConn wraps a bidirectional pipe pair and hooks into the SPDY executor
// stream for interactive shell sessions.
type shellConn struct {
	// stdin is the write end of the pipe that feeds into the SPDY executor's
	// stdin. User input from the WebSocket is written here.
	stdin io.WriteCloser
	// stdout is the read end of the pipe that the caller reads shell output from.
	stdout io.ReadCloser
	// cancel terminates the SPDY stream goroutine.
	cancel context.CancelFunc
}

func (c *shellConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *shellConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }
func (c *shellConn) Close() error {
	c.cancel()
	c.stdin.Close()
	c.stdout.Close()
	return nil
}

// Shell opens an interactive TTY shell session inside the sandbox pod.
func (b *KubernetesBackend) Shell(ctx context.Context, id string, cmd []string) (*sandbox.ShellSession, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}

	name := podName(id)

	execReq := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(name).
		Namespace(b.config.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "sandbox",
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", execReq.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY executor for shell: %w", err)
	}

	// Create bidirectional pipes:
	//   User (WebSocket) → stdinWriter → stdinReader → SPDY stdin
	//   SPDY stdout → stdoutWriter → stdoutReader → User (WebSocket)
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	// Terminal resize queue
	sizeQueue := newTerminalSizeQueue()

	// Tee shell output into the log buffer
	sl := b.getOrCreateLogs(id)
	sl.Append(logbuffer.Entry{Timestamp: time.Now(), Stream: "system", Line: "shell session started"})
	teeW := logbuffer.NewTeeWriter(sl)
	teedStdout := io.MultiWriter(stdoutWriter, teeW)

	streamCtx, streamCancel := context.WithCancel(ctx)

	// Run the SPDY stream in a goroutine — it blocks until the shell exits
	// or the context is cancelled.
	go func() {
		defer stdoutWriter.Close()
		defer stdinReader.Close()

		err := executor.StreamWithContext(streamCtx, remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            teedStdout,
			Stderr:            teedStdout, // TTY mode merges stderr into stdout
			Tty:               true,
			TerminalSizeQueue: sizeQueue,
		})
		if err != nil && streamCtx.Err() == nil {
			sl.Append(logbuffer.Entry{
				Timestamp: time.Now(),
				Stream:    "system",
				Line:      fmt.Sprintf("shell session ended: %v", err),
			})
		} else {
			sl.Append(logbuffer.Entry{
				Timestamp: time.Now(),
				Stream:    "system",
				Line:      "shell session ended",
			})
		}
	}()

	return &sandbox.ShellSession{
		Conn: &shellConn{
			stdin:  stdinWriter,
			stdout: stdoutReader,
			cancel: streamCancel,
		},
		Resize: func(rows, cols uint16) error {
			select {
			case sizeQueue.ch <- remotecommand.TerminalSize{
				Width:  cols,
				Height: rows,
			}:
				return nil
			default:
				return nil // drop resize if queue is full
			}
		},
	}, nil
}
