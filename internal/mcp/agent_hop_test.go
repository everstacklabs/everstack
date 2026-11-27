package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

type hopRoundTrip func(*http.Request) (*http.Response, error)

func (f hopRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestStreamableAgentHopPerCall(t *testing.T) {
	tr, _ := newStreamableHTTPTransport(ServerConfig{URL: "https://example.test/mcp", Headers: map[string]string{contextkeys.AgentHopHeader: "0"}})
	want := ""
	tr.client = &http.Client{Transport: hopRoundTrip(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get(contextkeys.AgentHopHeader); got != want {
			t.Errorf("hop=%q want %q", got, want)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`))}, nil
	})}
	for _, v := range []struct {
		hop    int
		header string
	}{{0, ""}, {1, "1"}, {2, "2"}} {
		want = v.header
		if _, err := tr.Send(contextkeys.WithAgentHop(context.Background(), v.hop), &JsonRpcRequest{Method: "ping"}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSSEAgentHopOnConnectAndEveryPost(t *testing.T) {
	tr, _ := newSSETransport(ServerConfig{URL: "https://example.test/sse", Headers: map[string]string{contextkeys.AgentHopHeader: "0"}})
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	defer tr.Close()
	want := "1"
	tr.client = &http.Client{Transport: hopRoundTrip(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get(contextkeys.AgentHopHeader); got != want {
			t.Errorf("%s hop=%q want %q", r.Method, got, want)
		}
		if r.Method == "GET" {
			go func() { _, _ = io.WriteString(writer, "event: endpoint\ndata: /messages\n\n") }()
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
		}
		var rpc JsonRpcRequest
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(JsonRpcResponse{JSONRPC: "2.0", ID: rpc.ID, Result: json.RawMessage(`{}`)})
		_, _ = io.WriteString(writer, "event: message\ndata: "+string(body)+"\n\n")
		return &http.Response{StatusCode: 202, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	for _, hop := range []int{1, 2} {
		want = string(rune('0' + hop))
		ctx, cancel := context.WithTimeout(contextkeys.WithAgentHop(context.Background(), hop), time.Second)
		_, err := tr.Send(ctx, &JsonRpcRequest{Method: "ping"})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
}
