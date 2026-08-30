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

// streamableHTTPTransport implements Transport for the MCP Streamable HTTP
// transport. Each JSON-RPC request is a POST to the server URL with
// Content-Type: application/json, and the response body is JSON.
type streamableHTTPTransport struct {
	url         string
	baseHeaders map[string]string
	auth        *AuthConfig
	client      *http.Client
	nextID      atomic.Int64

	mu        sync.RWMutex
	sessionID string
}

func newStreamableHTTPTransport(cfg ServerConfig) (*streamableHTTPTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp: url required for streamable_http transport")
	}
	return &streamableHTTPTransport{
		url:         cfg.URL,
		baseHeaders: cfg.Headers,
		auth:        cfg.AuthConfig,
		client:      &http.Client{},
	}, nil
}

func (t *streamableHTTPTransport) Send(ctx context.Context, req *JsonRpcRequest) (*JsonRpcResponse, error) {
	if req.ID == nil {
		req.ID = t.nextID.Add(1)
	}
	req.JSONRPC = jsonRPCVersion

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp: create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	for k, v := range mergeHeaders(t.baseHeaders, ResolveAuthHeaders(t.auth)) {
		httpReq.Header.Set(k, v)
	}
	if sessionID := t.getSessionID(); sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp: http request: %w", err)
	}
	defer httpResp.Body.Close()
	if sessionID := httpResp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		t.setSessionID(sessionID)
	}

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("mcp: read response body: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp: server returned %d: %s", httpResp.StatusCode, string(respBody))
	}
	if len(bytes.TrimSpace(respBody)) == 0 {
		return &JsonRpcResponse{JSONRPC: jsonRPCVersion, ID: req.ID}, nil
	}

	contentType := strings.ToLower(httpResp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		respBody, err = extractSSEData(respBody)
		if err != nil {
			return nil, err
		}
	}

	var rpcResp JsonRpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal response: %w", err)
	}
	return &rpcResp, nil
}

func (t *streamableHTTPTransport) Close() error {
	t.client.CloseIdleConnections()
	return nil
}

func (t *streamableHTTPTransport) getSessionID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sessionID
}

func (t *streamableHTTPTransport) setSessionID(sessionID string) {
	t.mu.Lock()
	t.sessionID = sessionID
	t.mu.Unlock()
}

func extractSSEData(body []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	dataLines := make([]string, 0, 4)

	flush := func() []byte {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" {
			return nil
		}
		return []byte(payload)
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if payload := flush(); payload != nil {
				return payload, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp: read event stream response: %w", err)
	}
	if payload := flush(); payload != nil {
		return payload, nil
	}
	return nil, fmt.Errorf("mcp: event stream response did not contain data")
}
