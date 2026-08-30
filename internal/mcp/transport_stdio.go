package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// stdioTransport implements Transport by managing an MCP server as a child
// process. Communication happens via stdin (requests) / stdout (responses)
// using newline-delimited JSON-RPC 2.0.
type stdioTransport struct {
	cfg    StdioConfig
	nextID atomic.Int64

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	started bool
	pending map[interface{}]chan *JsonRpcResponse
	done    chan struct{}
}

func newStdioTransport(cfg ServerConfig) (*stdioTransport, error) {
	return &stdioTransport{
		cfg:     *cfg.StdioConfig,
		pending: make(map[interface{}]chan *JsonRpcResponse),
		done:    make(chan struct{}),
	}, nil
}

// start launches the child process if not already running.
func (t *stdioTransport) start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return nil
	}

	cmd := exec.Command(t.cfg.Command, t.cfg.Args...)
	if t.cfg.WorkingDir != "" {
		cmd.Dir = t.cfg.WorkingDir
	}
	// Inherit parent env, then overlay custom vars
	cmd.Env = os.Environ()
	for k, v := range t.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdio stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("mcp: stdio stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp: start process %q: %w", t.cfg.Command, err)
	}

	t.cmd = cmd
	t.stdin = stdin
	t.started = true

	go t.readStdout(stdout)
	go t.waitProcess()

	return nil
}

func (t *stdioTransport) Send(ctx context.Context, req *JsonRpcRequest) (*JsonRpcResponse, error) {
	if err := t.start(); err != nil {
		return nil, err
	}

	if req.ID == nil {
		req.ID = t.nextID.Add(1)
	}
	req.JSONRPC = jsonRPCVersion

	ch := make(chan *JsonRpcResponse, 1)
	t.mu.Lock()
	t.pending[req.ID] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, req.ID)
		t.mu.Unlock()
	}()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}
	data = append(data, '\n')

	t.mu.Lock()
	_, err = t.stdin.Write(data)
	t.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("mcp: write to stdin: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, fmt.Errorf("mcp: child process exited")
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("mcp: child process closed stdout")
		}
		return resp, nil
	}
}

func (t *stdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started {
		return nil
	}
	t.started = false
	t.stdin.Close()
	// Allow graceful shutdown; process will exit when stdin closes.
	// waitProcess goroutine handles the rest.
	return nil
}

// readStdout reads newline-delimited JSON-RPC responses from stdout.
func (t *stdioTransport) readStdout(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp JsonRpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		t.mu.Lock()
		if ch, ok := t.pending[resp.ID]; ok {
			ch <- &resp
		}
		t.mu.Unlock()
	}
}

// waitProcess waits for the child process to exit and signals done.
func (t *stdioTransport) waitProcess() {
	if t.cmd != nil {
		_ = t.cmd.Wait()
	}
	close(t.done)
}
