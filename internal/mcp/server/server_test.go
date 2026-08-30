package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/mcp"
)

// fakeAuth resolves any request bearing the magic header to a fixed tenant.
type fakeAuth struct {
	tenantID string
	ok       bool
}

func (f fakeAuth) Authenticate(r *http.Request) (string, bool) {
	if r.Header.Get("X-Test-Auth") == "" {
		return "", false
	}
	return f.tenantID, f.ok
}

// fakeProvider records the tenant it was asked about and returns canned tools.
type fakeProvider struct {
	listedTenant string
	calledTenant string
	calledTool   string
	callErr      error
}

func (p *fakeProvider) ListTools(_ context.Context, tenantID string) ([]mcp.ToolInfo, error) {
	p.listedTenant = tenantID
	return []mcp.ToolInfo{{Name: "everstack_whoami", Description: "who am i"}}, nil
}

func (p *fakeProvider) CallTool(_ context.Context, tenantID, name string, _ map[string]interface{}) (*mcp.ToolCallResult, error) {
	p.calledTenant = tenantID
	p.calledTool = name
	if p.callErr != nil {
		return nil, p.callErr
	}
	return &mcp.ToolCallResult{Content: []mcp.ContentBlock{{Type: "text", Text: "tenant=" + tenantID}}}, nil
}

func newTestHandler(t *testing.T, prov ToolProvider) *Handler {
	t.Helper()
	return New(prov, fakeAuth{tenantID: "tenant-A", ok: true}, nil)
}

func do(t *testing.T, h *Handler, authed bool, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/server", strings.NewReader(body))
	if authed {
		req.Header.Set("X-Test-Auth", "yes")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) mcp.JsonRpcResponse {
	t.Helper()
	var resp mcp.JsonRpcResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rr.Body.String())
	}
	return resp
}

func TestUnauthenticatedRejected(t *testing.T) {
	h := newTestHandler(t, &fakeProvider{})
	rr := do(t, h, false, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("missing WWW-Authenticate challenge")
	}
}

func TestAuthenticatorFailClosed(t *testing.T) {
	// Authenticator returns ok=false: must 401 even with the header present.
	h := New(&fakeProvider{}, fakeAuth{tenantID: "tenant-A", ok: false}, nil)
	rr := do(t, h, true, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on failed auth, got %d", rr.Code)
	}
}

func TestInitialize(t *testing.T) {
	h := newTestHandler(t, &fakeProvider{})
	rr := do(t, h, true, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	resp := decode(t, rr)
	var res mcp.InitializeResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal init result: %v", err)
	}
	if res.ProtocolVersion != protocolVersion {
		t.Errorf("protocol version = %q, want %q", res.ProtocolVersion, protocolVersion)
	}
	if res.Capabilities.Tools == nil {
		t.Errorf("expected tools capability advertised")
	}
	if res.ServerInfo.Name != serverName {
		t.Errorf("server name = %q, want %q", res.ServerInfo.Name, serverName)
	}
}

func TestToolsListUsesAuthenticatedTenant(t *testing.T) {
	prov := &fakeProvider{}
	h := newTestHandler(t, prov)
	rr := do(t, h, true, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	// The provider must be queried for the authenticated tenant, never a
	// client-supplied value.
	if prov.listedTenant != "tenant-A" {
		t.Errorf("provider listed tenant %q, want authenticated tenant-A", prov.listedTenant)
	}
	resp := decode(t, rr)
	var res mcp.ToolListResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal tools list: %v", err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "everstack_whoami" {
		t.Errorf("unexpected tools: %+v", res.Tools)
	}
}

func TestToolsCallUsesAuthenticatedTenant(t *testing.T) {
	prov := &fakeProvider{}
	h := newTestHandler(t, prov)
	// Even though the body carries a bogus tenant_id arg, the call must run for
	// the authenticated tenant.
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everstack_whoami","arguments":{"tenant_id":"tenant-EVIL"}}}`
	rr := do(t, h, true, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if prov.calledTenant != "tenant-A" {
		t.Errorf("CallTool ran for tenant %q, want tenant-A", prov.calledTenant)
	}
	if prov.calledTool != "everstack_whoami" {
		t.Errorf("called tool %q", prov.calledTool)
	}
	resp := decode(t, rr)
	var res mcp.ToolCallResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal tool call: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected isError")
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "tenant-A") {
		t.Errorf("unexpected content: %+v", res.Content)
	}
}

func TestToolsCallProviderErrorIsInResult(t *testing.T) {
	prov := &fakeProvider{callErr: context.DeadlineExceeded}
	h := newTestHandler(t, prov)
	rr := do(t, h, true, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"x"}}`)
	resp := decode(t, rr)
	if resp.Error != nil {
		t.Fatalf("tool failure should not be a JSON-RPC error: %+v", resp.Error)
	}
	var res mcp.ToolCallResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected isError result")
	}
}

func TestToolsCallMissingName(t *testing.T) {
	h := newTestHandler(t, &fakeProvider{})
	rr := do(t, h, true, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{}}`)
	resp := decode(t, rr)
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("want invalid params error, got %+v", resp.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	h := newTestHandler(t, &fakeProvider{})
	rr := do(t, h, true, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	resp := decode(t, rr)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("want method not found, got %+v", resp.Error)
	}
}

func TestInitializedNotificationNoBody(t *testing.T) {
	h := newTestHandler(t, &fakeProvider{})
	rr := do(t, h, true, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("want 202 for notification, got %d", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "" {
		t.Errorf("notification should have no body, got %q", rr.Body.String())
	}
}

func TestIDEchoedVerbatim(t *testing.T) {
	h := newTestHandler(t, &fakeProvider{})
	rr := do(t, h, true, `{"jsonrpc":"2.0","id":"abc-123","method":"ping"}`)
	resp := decode(t, rr)
	if id, _ := resp.ID.(string); id != "abc-123" {
		t.Errorf("id = %v, want abc-123", resp.ID)
	}
}

func TestBatchRejected(t *testing.T) {
	h := newTestHandler(t, &fakeProvider{})
	rr := do(t, h, true, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)
	resp := decode(t, rr)
	if resp.Error == nil || resp.Error.Code != codeInvalidRequest {
		t.Fatalf("want invalid request for batch, got %+v", resp.Error)
	}
}
