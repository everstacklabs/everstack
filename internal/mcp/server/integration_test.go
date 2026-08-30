package server_test

// This external test drives the real ToolProvider (serverprovider) through the
// real Handler over httptest, exercising the exact JSON-RPC sequence an MCP
// client (Claude Desktop, Cursor, Google ADK) issues: initialize → tools/list →
// tools/call. It complements server_test.go (which uses fakes for dispatch
// edge cases) by validating real tool shapes end to end. A live client
// handshake against a running binary remains the final verification step.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/mcp"
	"github.com/everstacklabs/everstack/internal/mcp/server"
	"github.com/everstacklabs/everstack/internal/mcp/serverprovider"
)

// stubAuth attributes every request bearing any Authorization header to a fixed
// tenant; unauthenticated requests are rejected (fail closed).
type stubAuth struct{ tenant string }

func (s stubAuth) Authenticate(r *http.Request) (string, bool) {
	if r.Header.Get("Authorization") == "" {
		return "", false
	}
	return s.tenant, true
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := server.New(serverprovider.New(serverprovider.Deps{}), stubAuth{tenant: "tenant-A"}, nil)
	return httptest.NewServer(h)
}

func rpc(t *testing.T, ts *httptest.Server, authed bool, payload string) mcp.JsonRpcResponse {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader([]byte(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authed {
		req.Header.Set("Authorization", "Bearer sk-test")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if !authed {
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("want 401 unauthenticated, got %d", resp.StatusCode)
		}
		return mcp.JsonRpcResponse{}
	}
	var out mcp.JsonRpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestFullClientSequence(t *testing.T) {
	ts := newServer(t)
	defer ts.Close()

	// 1) initialize
	init := rpc(t, ts, true, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test-client","version":"0.1"}}}`)
	var ir mcp.InitializeResult
	if err := json.Unmarshal(init.Result, &ir); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	if ir.ProtocolVersion == "" || ir.Capabilities.Tools == nil {
		t.Fatalf("bad initialize result: %+v", ir)
	}

	// 2) tools/list — the real provider's builtins must be present and well-formed.
	list := rpc(t, ts, true, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var lr mcp.ToolListResult
	if err := json.Unmarshal(list.Result, &lr); err != nil {
		t.Fatalf("tools/list result: %v", err)
	}
	found := map[string]mcp.ToolInfo{}
	for _, tl := range lr.Tools {
		found[tl.Name] = tl
	}
	for _, want := range []string{"everstack_whoami", "everstack_echo"} {
		tl, ok := found[want]
		if !ok {
			t.Fatalf("tool %q missing from list %v", want, lr.Tools)
		}
		if tl.InputSchema == nil || tl.InputSchema["type"] != "object" {
			t.Errorf("tool %q has malformed input schema: %+v", want, tl.InputSchema)
		}
	}

	// 3) tools/call everstack_whoami — must report the authenticated tenant.
	call := rpc(t, ts, true, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everstack_whoami","arguments":{}}}`)
	var cr mcp.ToolCallResult
	if err := json.Unmarshal(call.Result, &cr); err != nil {
		t.Fatalf("tools/call result: %v", err)
	}
	if cr.IsError || len(cr.Content) == 0 || !strings.Contains(cr.Content[0].Text, "tenant-A") {
		t.Fatalf("whoami did not report tenant-A: %+v", cr)
	}

	// 4) unauthenticated request is rejected.
	rpc(t, ts, false, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
}
