package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// stubRunner is a fake AgentRunner for tests.
type stubRunner struct {
	fn func(ctx context.Context, tenant, agent, msg string) (string, error)
}

func (s stubRunner) Run(ctx context.Context, tenant, agent, msg string) (string, error) {
	return s.fn(ctx, tenant, agent, msg)
}

// --- fakes ---

type fakeAuth struct {
	tenant string
	ok     bool
}

func (f fakeAuth) Authenticate(r *http.Request) (string, bool) {
	if r.Header.Get("Authorization") == "" {
		return "", false
	}
	return f.tenant, f.ok
}

type fakeLookup struct {
	name, desc string
	found      bool
	err        error
	gotTenant  string
}

func (f *fakeLookup) GetAgent(_ context.Context, tenantID, _ string) (string, string, bool, error) {
	f.gotTenant = tenantID
	return f.name, f.desc, f.found, f.err
}

type fakePublisher struct{ published bool }

func (f fakePublisher) IsAgentPublished(_ context.Context, _, _ string) (bool, error) {
	return f.published, nil
}

// newRouter wires the server's routes onto a real gorilla mux so {agentId}
// vars resolve exactly as in production.
func newRouter(s *Server) *mux.Router {
	r := mux.NewRouter()
	s.Mount(routerAdapter{r})
	return r
}

type routerAdapter struct{ r *mux.Router }

func (a routerAdapter) HandleFunc(path string, f http.HandlerFunc) { a.r.HandleFunc(path, f) }

func newTestServer(d Deps) *Server {
	if d.Auth == nil {
		d.Auth = fakeAuth{tenant: "tenant-A", ok: true}
	}
	return New(d)
}

func doReq(t *testing.T, r http.Handler, method, path string, authed bool, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if authed {
		req.Header.Set("Authorization", "Bearer sk-test")
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// --- card tests ---

func TestAgentCardUnauthenticated(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{name: "Bot", found: true}})
	rr := doReq(t, newRouter(s), http.MethodGet, "/a2a/agents/agent-1/.well-known/agent.json", false, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestAgentCardServedForOwnedAgent(t *testing.T) {
	lk := &fakeLookup{name: "Support Bot", desc: "Helps customers", found: true}
	s := newTestServer(Deps{Agents: lk})
	rr := doReq(t, newRouter(s), http.MethodGet, "/a2a/agents/agent-1/.well-known/agent.json", true, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if lk.gotTenant != "tenant-A" {
		t.Errorf("lookup used tenant %q, want authenticated tenant-A", lk.gotTenant)
	}
	var card AgentCard
	if err := json.Unmarshal(rr.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	if card.Name != "Support Bot" || card.Description != "Helps customers" {
		t.Errorf("unexpected card: %+v", card)
	}
	if !strings.HasSuffix(card.URL, "/a2a/agents/agent-1") {
		t.Errorf("card url = %q, want .../a2a/agents/agent-1", card.URL)
	}
	if card.ProtocolVersion == "" || len(card.Skills) == 0 {
		t.Errorf("card missing protocolVersion/skills: %+v", card)
	}
}

func TestAgentCardNotFound(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{found: false}})
	rr := doReq(t, newRouter(s), http.MethodGet, "/a2a/agents/missing/.well-known/agent.json", true, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestUnpublishedAgentCardIs404(t *testing.T) {
	// Publisher denies => the card must 404 even though the agent exists.
	s := newTestServer(Deps{Agents: &fakeLookup{name: "Bot", found: true}, Publisher: fakePublisher{published: false}})
	rr := doReq(t, newRouter(s), http.MethodGet, "/a2a/agents/agent-1/.well-known/agent.json", true, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unpublished agent, got %d", rr.Code)
	}
}

func TestPublishedAgentCardServed(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{name: "Bot", found: true}, Publisher: fakePublisher{published: true}})
	rr := doReq(t, newRouter(s), http.MethodGet, "/a2a/agents/agent-1/.well-known/agent.json", true, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 for published agent, got %d", rr.Code)
	}
}

func TestUnpublishedMessageSendIs404(t *testing.T) {
	s := newTestServer(Deps{
		Agents:    &fakeLookup{found: true},
		Publisher: fakePublisher{published: false},
		Runner:    stubRunner{fn: func(context.Context, string, string, string) (string, error) { return "x", nil }},
	})
	rr := doReq(t, newRouter(s), http.MethodPost, "/a2a/agents/agent-1", true,
		`{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hi"}]}}}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unpublished agent message/send, got %d", rr.Code)
	}
}

// --- message/send tests ---

func TestMessageSendUnauthenticated(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{found: true}})
	rr := doReq(t, newRouter(s), http.MethodPost, "/a2a/agents/agent-1", false,
		`{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hi"}]}}}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestMessageSendCompletesAsTask(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{found: true}, Runner: stubRunner{fn: func(_ context.Context, tenant, agent, msg string) (string, error) {
		if tenant != "tenant-A" {
			return "", errors.New("wrong tenant")
		}
		return "echo:" + msg, nil
	}}})
	rr := doReq(t, newRouter(s), http.MethodPost, "/a2a/agents/agent-1", true,
		`{"jsonrpc":"2.0","id":7,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hello"}]}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var resp rpcResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	raw, _ := json.Marshal(resp.Result)
	var task a2aTask
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.Status.State != "completed" {
		t.Errorf("state = %q, want completed", task.Status.State)
	}
	if len(task.Artifacts) == 0 || task.Artifacts[0].Parts[0].Text != "echo:hello" {
		t.Errorf("unexpected artifacts: %+v", task.Artifacts)
	}
}

func TestMessageSendRunnerErrorIsFailedTask(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{found: true}, Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) {
		return "", errors.New("no active deployment")
	}}})
	rr := doReq(t, newRouter(s), http.MethodPost, "/a2a/agents/agent-1", true,
		`{"jsonrpc":"2.0","id":8,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hi"}]}}}`)
	var resp rpcResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	raw, _ := json.Marshal(resp.Result)
	var task a2aTask
	_ = json.Unmarshal(raw, &task)
	if task.Status.State != "failed" {
		t.Errorf("state = %q, want failed", task.Status.State)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{found: true}, Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) { return "x", nil }}})
	rr := doReq(t, newRouter(s), http.MethodPost, "/a2a/agents/agent-1", true,
		`{"jsonrpc":"2.0","id":9,"method":"tasks/cancel"}`)
	var resp rpcResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("want method-not-found, got %+v", resp.Error)
	}
}

func TestStreamingRejected(t *testing.T) {
	s := newTestServer(Deps{Agents: &fakeLookup{found: true}, Runner: stubRunner{fn: func(context.Context, string, string, string) (string, error) { return "x", nil }}})
	rr := doReq(t, newRouter(s), http.MethodPost, "/a2a/agents/agent-1", true,
		`{"jsonrpc":"2.0","id":10,"method":"message/stream","params":{}}`)
	var resp rpcResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error == nil {
		t.Fatalf("expected error for streaming, got result")
	}
}
