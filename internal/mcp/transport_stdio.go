package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
)

// stdioInheritedEnv is the only part of the gateway's own environment a
// stdio child receives. Everything else the gateway holds (database DSNs,
// signing keys, provider credentials, service tokens, any EVS_ or MF_
// setting) stays out of the child; the server's configured env is layered
// on top.
//
// Beyond PATH and HOME the list carries what common self-hosted setups
// need to work at all: an egress proxy (both spellings, since tools differ
// on which they read), a private CA bundle, a temp dir, locale, timezone
// and the user name. None of these names a secret. Note that a proxy URL
// can embed credentials (http://user:pass@proxy); that is the operator's
// own proxy, which the child must use anyway to reach the network. Never
// add a variable here whose name suggests a key, token, password or DSN.
var stdioInheritedEnv = []string{
	"PATH", "HOME", "USER", "TMPDIR",
	"LANG", "LC_ALL", "TZ",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
}

// childEnv builds the environment for a stdio MCP child process: the
// allowlisted parent variables that exist, then the configured env in a
// deterministic order. Configured keys may override the inherited ones.
func childEnv(cfg StdioConfig) []string {
	env := make([]string, 0, len(stdioInheritedEnv)+len(cfg.Env))
	for _, k := range stdioInheritedEnv {
		if _, overridden := cfg.Env[k]; overridden {
			continue
		}
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	keys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+cfg.Env[k])
	}
	return env
}

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
	pending map[string]chan *JsonRpcResponse // keyed by idKey(id)
	done    chan struct{}
}

func newStdioTransport(cfg ServerConfig) (*stdioTransport, error) {
	return &stdioTransport{
		cfg:     *cfg.StdioConfig,
		pending: make(map[string]chan *JsonRpcResponse),
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
	// Exec-time policy gate, the last line before a process is spawned:
	// a transport constructed before the switch was flipped (or by a
	// future path that bypasses NewTransport) still cannot run.
	if !StdioAllowed() {
		return ErrStdioNotAllowed
	}

	cmd := exec.Command(t.cfg.Command, t.cfg.Args...)
	if t.cfg.WorkingDir != "" {
		cmd.Dir = t.cfg.WorkingDir
	}
	// Never hand the child the gateway's full environment: it carries the
	// process's own secrets. Allowlisted vars plus the configured env only.
	cmd.Env = childEnv(t.cfg)
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

	// Keyed by idKey: the request id is an int64 from nextID but the
	// child's echoed id decodes as float64, so a raw interface{} key never
	// matched and every Send blocked until ctx timeout (same fix as SSE).
	key := idKey(req.ID)
	ch := make(chan *JsonRpcResponse, 1)
	t.mu.Lock()
	t.pending[key] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, key)
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
		if ch, ok := t.pending[idKey(resp.ID)]; ok {
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
