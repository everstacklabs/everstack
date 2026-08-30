package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// sseTransport implements Transport for legacy MCP SSE transport.
//
// The SSE transport works in two phases:
//  1. A GET request to the server URL opens an SSE stream that delivers
//     the "endpoint" event containing the POST URL for messages.
//  2. JSON-RPC requests are POSTed to the endpoint URL, and responses
//     arrive on the SSE stream keyed by the request ID.
type sseTransport struct {
	baseURL string
	headers map[string]string
	client  *http.Client
	nextID  atomic.Int64

	// Set after the SSE connection is established.
	mu        sync.Mutex
	postURL   string
	connected bool
	sseCancel context.CancelFunc
	pending   map[string]chan *JsonRpcResponse
}

func newSSETransport(cfg ServerConfig) (*sseTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp: url required for sse transport")
	}
	headers := mergeHeaders(cfg.Headers, ResolveAuthHeaders(cfg.AuthConfig))
	return &sseTransport{
		baseURL: cfg.URL,
		headers: headers,
		client:  &http.Client{},
		pending: make(map[string]chan *JsonRpcResponse),
	}, nil
}

// connect opens the SSE stream and waits for the endpoint event.
func (t *sseTransport) connect(ctx context.Context) error {
	t.mu.Lock()
	if t.connected {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()

	sseCtx, cancel := context.WithCancel(context.Background())

	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, t.baseURL, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: sse connect request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: sse connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("mcp: sse connect returned %d", resp.StatusCode)
	}

	// Wait for the endpoint event
	endpointCh := make(chan string, 1)
	go t.readSSE(resp.Body, endpointCh)

	select {
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	case ep := <-endpointCh:
		if ep == "" {
			cancel()
			return fmt.Errorf("mcp: sse stream closed without endpoint event")
		}
		t.mu.Lock()
		t.postURL = resolveEndpoint(t.baseURL, ep)
		t.connected = true
		t.sseCancel = cancel
		t.mu.Unlock()
	}
	return nil
}

func (t *sseTransport) Send(ctx context.Context, req *JsonRpcRequest) (*JsonRpcResponse, error) {
	if err := t.connect(ctx); err != nil {
		return nil, err
	}

	if req.ID == nil {
		req.ID = t.nextID.Add(1)
	}
	req.JSONRPC = jsonRPCVersion

	// Register pending response channel
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

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	t.mu.Lock()
	postURL := t.postURL
	t.mu.Unlock()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp: create post request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp: post request: %w", err)
	}
	httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp: post returned %d", httpResp.StatusCode)
	}

	// Wait for response on the SSE stream
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("mcp: sse stream closed")
		}
		return resp, nil
	}
}

func (t *sseTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sseCancel != nil {
		t.sseCancel()
	}
	t.connected = false
	t.client.CloseIdleConnections()
	return nil
}

// readSSE parses the SSE stream, dispatching the first `endpoint` event to
// endpointCh and JSON-RPC `message` events to t.pending. Implements the
// subset of the SSE spec MCP needs:
//
//   - Lines starting with ":" are comments and ignored.
//   - "field: value" — strip a single optional leading space from value.
//   - "data:" — append to data buffer; multi-line data joined with "\n".
//   - "event:" — set the event type for the next dispatch (default
//     "message" per spec).
//   - Empty line — dispatch the buffered event and reset.
//
// Uses bufio.Reader.ReadString instead of bufio.Scanner so events larger
// than 64KB (typical for tools/list on a server with many tools) don't
// silently truncate with bufio.ErrTooLong.
func (t *sseTransport) readSSE(body io.ReadCloser, endpointCh chan<- string) {
	defer body.Close()
	reader := bufio.NewReader(body)

	var (
		eventType    = "message" // SSE spec default
		dataBuf      strings.Builder
		endpointSent bool
	)

	dispatch := func() {
		defer func() {
			dataBuf.Reset()
			eventType = "message"
		}()
		if dataBuf.Len() == 0 {
			return
		}
		data := dataBuf.String()
		switch eventType {
		case "endpoint":
			if !endpointSent {
				endpointCh <- data
				endpointSent = true
			}
		case "message":
			var resp JsonRpcResponse
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				return
			}
			t.mu.Lock()
			ch, ok := t.pending[idKey(resp.ID)]
			t.mu.Unlock()
			if !ok {
				return
			}
			// Buffered chan(1) plus a non-blocking send means a duplicate
			// response (rare; some servers retry) doesn't deadlock the
			// reader goroutine.
			select {
			case ch <- &resp:
			default:
			}
		}
	}

	for {
		line, err := reader.ReadString('\n')
		// Handle the line *before* checking err so we don't lose the
		// final unterminated line when the server closes the stream.
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			dispatch()
		case strings.HasPrefix(line, ":"):
			// Comment, ignore.
		case strings.HasPrefix(line, "event:"):
			eventType = trimSSEFieldValue(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(trimSSEFieldValue(line[len("data:"):]))
		// id:, retry: are spec-defined but not used by MCP today; ignore.
		}
		if err != nil {
			// Stream closed. Flush any pending event that lacked a
			// trailing blank line, then exit.
			dispatch()
			break
		}
	}

	if !endpointSent {
		select {
		case endpointCh <- "":
		default:
		}
	}
}

// trimSSEFieldValue strips a single optional leading space, per the SSE
// spec ("If value starts with a U+0020 SPACE character, remove it.").
// Using strings.TrimSpace would corrupt JSON payloads with intentional
// leading whitespace.
func trimSSEFieldValue(v string) string {
	if strings.HasPrefix(v, " ") {
		return v[1:]
	}
	return v
}

// idKey returns a canonical string key for a JSON-RPC id. JSON-RPC ids
// can be strings, numbers, or null. encoding/json decodes JSON numbers
// into interface{} as float64, while requests are sent with int64 ids
// from nextID.Add. Without normalization, map[interface{}]chan would
// store int64(1) but look up float64(1) on response and miss every
// time, leaving every Send() blocked until ctx timeout.
func idKey(id interface{}) string {
	switch v := id.(type) {
	case nil:
		return ""
	case string:
		return "s:" + v
	case json.Number:
		return "n:" + v.String()
	case float64:
		// Whole numbers are the common case (request id from nextID).
		if v == float64(int64(v)) {
			return fmt.Sprintf("n:%d", int64(v))
		}
		return fmt.Sprintf("n:%g", v)
	case float32:
		return idKey(float64(v))
	case int:
		return fmt.Sprintf("n:%d", v)
	case int32:
		return fmt.Sprintf("n:%d", v)
	case int64:
		return fmt.Sprintf("n:%d", v)
	case uint, uint32, uint64:
		return fmt.Sprintf("n:%d", v)
	default:
		return fmt.Sprintf("v:%v", v)
	}
}

// resolveEndpoint resolves a potentially relative endpoint against the base URL.
func resolveEndpoint(base, endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	// Relative path: join with base URL origin
	idx := strings.Index(base, "://")
	if idx == -1 {
		return endpoint
	}
	rest := base[idx+3:]
	slashIdx := strings.Index(rest, "/")
	if slashIdx == -1 {
		return base + endpoint
	}
	origin := base[:idx+3+slashIdx]
	return origin + endpoint
}
