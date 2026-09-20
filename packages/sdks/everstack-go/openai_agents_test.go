package everstack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIAgentsCreateAndEventsProtocol(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("x-evs-api-key") != "pk_test" || r.Header.Get("x-evs-org-id") != "org" {
			t.Fatal("missing authentication headers")
		}
		switch r.URL.RequestURI() {
		case "/v1/openai-agents/sessions":
			if r.Method != http.MethodPost {
				t.Fatal(r.Method)
			}
			var body OpenAIAgentSessionParams
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.IdempotencyKey != "intent-1" || body.Environment.Type != "none" {
				t.Fatalf("body=%#v", body)
			}
			_ = json.NewEncoder(w).Encode(OpenAIAgentSession{ID: "s1", Provider: "openai_agents"})
		case "/v1/openai-agents/sessions/s1/events?after=7&limit=20":
			_ = json.NewEncoder(w).Encode(OpenAIAgentEventPage{NextCursor: 8})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()
	c := NewClient("pk_test", WithBaseURL(srv.URL), WithOrgID("org"))
	if c.OpenAIAgents == nil {
		t.Fatal("OpenAIAgents is not registered")
	}
	s, err := c.OpenAIAgents.Sessions.Create(context.Background(), OpenAIAgentSessionParams{ConnectionID: "c1", IdempotencyKey: "intent-1", Model: "gpt", Environment: OpenAIAgentEnvironment{Type: "none"}, Input: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "s1" {
		t.Fatal(s.ID)
	}
	p, err := c.OpenAIAgents.Sessions.Events(context.Background(), "s1", 7, 20)
	if err != nil {
		t.Fatal(err)
	}
	if p.NextCursor != 8 {
		t.Fatal(p.NextCursor)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestOpenAIAgentsMutationDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); panic(http.ErrAbortHandler) }))
	defer srv.Close()
	c := NewClient("pk", WithBaseURL(srv.URL))
	_, err := c.OpenAIAgents.Connections.Create(context.Background(), OpenAIAgentConnectionParams{Name: "x", APIKey: "upstream"})
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("error=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestOpenAIAgentsRejectsCredentialRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c := NewClient("pk", WithBaseURL(source.URL))
	_, err := c.OpenAIAgents.Sessions.Create(context.Background(), OpenAIAgentSessionParams{})
	if err == nil || !strings.Contains(err.Error(), "redirect rejected") {
		t.Fatalf("error=%v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatal("redirect target called")
	}
}

func TestOpenAIAgentsUsesConfiguredTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := NewClient("pk", WithBaseURL(srv.URL), WithTimeout(time.Second))
	_, err := c.OpenAIAgents.Sessions.Create(context.Background(), OpenAIAgentSessionParams{})
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("error=%v", err)
	}
}

func TestOpenAIAgentsSupportsBearerWithoutPlaceholderAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	c := NewClient("", WithBaseURL(srv.URL), WithAccessToken("user-token"))
	if _, err := c.OpenAIAgents.Connections.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIAgentsCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/openai-agents/capabilities" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"enabled":true,"provider":"openai_agents","environment_modes":["none"],"tool_types":[],"file_operations":[],"readiness":"ready","can_manage_connections":false,"can_run_sessions":true,"can_resolve_approvals":false}`))
	}))
	defer srv.Close()
	c := NewClient("pk", WithBaseURL(srv.URL))
	capabilities, err := c.OpenAIAgents.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Enabled || capabilities.Provider != "openai_agents" || capabilities.ExecutorVersion != "" || len(capabilities.EnvironmentModes) != 1 || !capabilities.CanRunSessions {
		t.Fatalf("capabilities=%#v", capabilities)
	}
}

func TestOpenAIAgentsFinalToolArtifactApprovalAndUsageRoutes(t *testing.T) {
	seen := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		seen[key] = true
		switch r.URL.Path {
		case "/v1/openai-agents/sessions":
			var p OpenAIAgentSessionParams
			_ = json.NewDecoder(r.Body).Decode(&p)
			if len(p.Tools) != 1 || p.Tools[0].ResourceID != "fn1" {
				t.Fatalf("tools=%v", p.Tools)
			}
			_, _ = w.Write([]byte(`{"id":"s1"}`))
		case "/v1/openai-agents/approvals":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/v1/openai-agents/approvals/a1/resolve":
			_, _ = w.Write([]byte(`{"resolved":true}`))
		case "/v1/openai-agents/sessions/s1/inputs", "/v1/openai-agents/sessions/s1/artifacts/export":
			_, _ = w.Write([]byte(`{"object_id":"o1"}`))
		case "/v1/openai-agents/sessions/s1/artifacts/o1/download":
			_, _ = w.Write([]byte(`{"object_id":"o1","url":"https://download.test/a"}`))
		case "/v1/openai-agents/sessions/s1/artifacts/o1":
			_, _ = w.Write([]byte(`{"deleted":true}`))
		case "/v1/openai-agents/sessions/s1/usage":
			_, _ = w.Write([]byte(`{"status":"reported","total_tokens":3,"source":"openai"}`))
		default:
			t.Fatal(key)
		}
	}))
	defer srv.Close()
	c := NewClient("pk", WithBaseURL(srv.URL))
	ctx := context.Background()
	_, _ = c.OpenAIAgents.Sessions.Create(ctx, OpenAIAgentSessionParams{Tools: []OpenAIAgentTool{{Type: "function", ResourceID: "fn1", Name: "run"}}})
	_, _ = c.OpenAIAgents.Approvals.List(ctx)
	_, _ = c.OpenAIAgents.Approvals.Resolve(ctx, "a1", "approved")
	_, _ = c.OpenAIAgents.Sessions.UploadArtifact(ctx, "s1", OpenAIAgentArtifactUpload{Filename: "a", ContentBase64: "YQ=="})
	_, _ = c.OpenAIAgents.Sessions.ExportArtifact(ctx, "s1", OpenAIAgentArtifactExport{Path: "/tmp/a"})
	_, _ = c.OpenAIAgents.Sessions.DownloadArtifact(ctx, "s1", "o1")
	_, _ = c.OpenAIAgents.Sessions.DeleteArtifact(ctx, "s1", "o1")
	_, _ = c.OpenAIAgents.Sessions.Usage(ctx, "s1")
	if len(seen) != 8 {
		t.Fatalf("seen=%v", seen)
	}
}
