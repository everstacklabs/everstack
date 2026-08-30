package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestStreamableHTTPTransport_UsesSessionIDAcrossRequests(t *testing.T) {
	t.Parallel()

	type call struct {
		Accept    string
		SessionID string
		Method    string
	}
	var (
		mu    sync.Mutex
		calls []call
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		mu.Lock()
		calls = append(calls, call{
			Accept:    r.Header.Get("Accept"),
			SessionID: r.Header.Get("Mcp-Session-Id"),
			Method:    req.Method,
		})
		callNum := len(calls)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if callNum == 1 {
			w.Header().Set("Mcp-Session-Id", "session-123")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"test","version":"1.0.0"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`))
	}))
	defer server.Close()

	tp, err := newStreamableHTTPTransport(ServerConfig{
		URL:           server.URL,
		TransportType: TransportStreamableHTTP,
	})
	if err != nil {
		t.Fatalf("newStreamableHTTPTransport() error = %v", err)
	}

	if _, err := tp.Send(context.Background(), &JsonRpcRequest{Method: "initialize"}); err != nil {
		t.Fatalf("Send(initialize) error = %v", err)
	}
	if _, err := tp.Send(context.Background(), &JsonRpcRequest{Method: "tools/list"}); err != nil {
		t.Fatalf("Send(tools/list) error = %v", err)
	}

	mu.Lock()
	gotCalls := append([]call(nil), calls...)
	mu.Unlock()

	if len(gotCalls) != 2 {
		t.Fatalf("calls = %d, want 2", len(gotCalls))
	}
	if gotCalls[0].SessionID != "" {
		t.Fatalf("first request session ID = %q, want empty", gotCalls[0].SessionID)
	}
	if gotCalls[1].SessionID != "session-123" {
		t.Fatalf("second request session ID = %q, want %q", gotCalls[1].SessionID, "session-123")
	}
	for i := range gotCalls {
		if !strings.Contains(gotCalls[i].Accept, "application/json") || !strings.Contains(gotCalls[i].Accept, "text/event-stream") {
			t.Fatalf("request %d accept header = %q, want both application/json and text/event-stream", i+1, gotCalls[i].Accept)
		}
	}
}

func TestStreamableHTTPTransport_ParsesEventStreamResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\n"))
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[]}}\n\n"))
	}))
	defer server.Close()

	tp, err := newStreamableHTTPTransport(ServerConfig{
		URL:           server.URL,
		TransportType: TransportStreamableHTTP,
	})
	if err != nil {
		t.Fatalf("newStreamableHTTPTransport() error = %v", err)
	}

	resp, err := tp.Send(context.Background(), &JsonRpcRequest{Method: "tools/list"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp == nil || len(resp.Result) == 0 {
		t.Fatalf("response result missing")
	}
}
