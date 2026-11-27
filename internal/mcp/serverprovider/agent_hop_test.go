package serverprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/adk"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp"
	"github.com/everstacklabs/everstack/internal/mcp/server"
)

type hopRunner struct {
	fn func(context.Context) (string, error)
}

func (r hopRunner) Run(ctx context.Context, _, _, _ string) (string, error) { return r.fn(ctx) }

type hopADK struct{ fn func(context.Context) }

func (r hopADK) Run(ctx context.Context, _ adk.RunRequest) (*adk.RunResult, error) {
	r.fn(ctx)
	return &adk.RunResult{Output: "done"}, nil
}
func TestRunToolsDepth(t *testing.T) {
	for _, hop := range []int{0, 1, 2, 16} {
		for _, adkTool := range []bool{false, true} {
			called := false
			check := func(ctx context.Context) {
				called = true
				if contextkeys.AgentHop(ctx) != hop+1 {
					t.Error("hop was not incremented")
				}
			}
			ctx := contextkeys.WithAgentHop(context.Background(), hop)
			var err error
			if adkTool {
				_, err = (&runADKTool{rt: hopADK{check}}).Execute(ctx, map[string]interface{}{"agent_code": "code"})
			} else {
				_, err = (&runAgentTool{runner: hopRunner{func(ctx context.Context) (string, error) { check(ctx); return "done", nil }}}).Execute(ctx, map[string]interface{}{"agent_id": "a", "message": "hi"})
			}
			if hop < 2 {
				if err != nil || !called {
					t.Fatalf("depth %d refused: %v", hop, err)
				}
			} else if called || err == nil || err.Error() != "agent call depth limit reached" {
				t.Fatalf("depth %d: called=%v err=%v", hop, called, err)
			}
		}
	}
}

type loopAuth struct{}

func (loopAuth) AuthenticateKey(*http.Request) (string, string, bool) { return "tenant", "key", true }

type loopTools struct{ tool *runAgentTool }

func (loopTools) ListTools(context.Context, string) ([]mcp.ToolInfo, error) { return nil, nil }
func (p loopTools) CallTool(ctx context.Context, _, _ string, args map[string]interface{}) (*mcp.ToolCallResult, error) {
	out, err := p.tool.Execute(ctx, args)
	return &mcp.ToolCallResult{Content: []mcp.ContentBlock{{Type: "text", Text: out}}}, err
}

// loopTransport models a peer calling this same gateway, without a local port.
// Both sides use the real MCP server and run_agent tool, including header parsing.
type loopTransport struct{ handler http.Handler }

func (t loopTransport) Send(ctx context.Context, _ *mcp.JsonRpcRequest) (*mcp.JsonRpcResponse, error) {
	r := httptest.NewRequest("POST", "https://example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_agent","arguments":{"agent_id":"a","message":"hi"}}}`))
	contextkeys.WriteAgentHop(ctx, r.Header)
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	var rpc mcp.JsonRpcResponse
	err := json.Unmarshal(w.Body.Bytes(), &rpc)
	return &rpc, err
}
func (loopTransport) Close() error { return nil }
func TestRecursiveMCPLoopStopsAtTwoRuns(t *testing.T) {
	var transport loopTransport
	runs := 0
	tool := &runAgentTool{tenantID: "tenant", runner: hopRunner{func(ctx context.Context) (string, error) {
		runs++
		// The session detach must retain the hop when the parent HTTP call ends.
		response, err := transport.Send(context.WithoutCancel(ctx), nil)
		if err != nil {
			return "", err
		}
		return string(response.Result), nil
	}}}
	transport.handler = server.New(loopTools{tool}, loopAuth{}, nil)
	response, err := transport.Send(context.Background(), nil)
	if err != nil || runs != 2 || !strings.Contains(string(response.Result), "agent call depth limit reached") {
		t.Fatalf("runs=%d err=%v response=%s", runs, err, response.Result)
	}
	// Malformed headers must fail closed before running even the first agent.
	for _, raw := range []string{"bad", "-1", "17", ""} {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_agent","arguments":{"agent_id":"a","message":"hi"}}}`))
		r.Header.Set(contextkeys.AgentHopHeader, raw)
		w := httptest.NewRecorder()
		transport.handler.ServeHTTP(w, r)
		if runs != 2 || !strings.Contains(w.Body.String(), "agent call depth limit reached") {
			t.Fatalf("malformed %q ran agent: %s", raw, w.Body)
		}
	}
}
