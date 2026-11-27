package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// verifiedTenantCtx is what the router-level auth middleware leaves on a
// request after it verifies a session cookie for tenantID.
func verifiedTenantCtx(tenantID string) context.Context {
	ctx := contextkeys.WithTenantID(context.Background(), tenantID)
	ctx = contextkeys.WithCloudUserID(ctx, "user-1")
	return contextkeys.WithTenantAuthenticated(ctx)
}

// unverifiedContexts are the shapes a request can have without any credential
// having been checked. Every guarded route must refuse all of them.
func unverifiedContexts() map[string]context.Context {
	return map[string]context.Context{
		"no context at all": context.Background(),
		// Standalone local-tenant middleware: tenant on every request.
		"tenant id only": contextkeys.WithTenantID(context.Background(), "tenant-b"),
		// Legacy compatibility path: flag and tenant, no user and no key.
		"tenant id plus bare auth flag": contextkeys.WithTenantAuthenticated(contextkeys.WithTenantID(context.Background(), "tenant-b")),
		// A user id with no authenticated flag proves nothing either.
		"user id without auth flag": contextkeys.WithCloudUserID(contextkeys.WithTenantID(context.Background(), "tenant-b"), "user-1"),
	}
}

func serveRaw(t *testing.T, template string, h http.Handler, method, path string, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	r := mux.NewRouter()
	r.Handle(template, h)
	req := httptest.NewRequest(method, path, strings.NewReader(`{"command":"id","tenant_id":"tenant-b","input_id":"in-1"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func markerHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

// The sandbox command, file and stream routes are registered directly on the
// router, outside the /v1 auth chain. Until 2026-11-11 they resolved the
// sandbox from the path and ran with no caller check at all: anyone who knew
// or guessed a sandbox id or name could run commands in it.
func TestRequireSandboxOwnershipRefusesUnverifiedCallers(t *testing.T) {
	s := &Server{sandboxMgr: ownershipTestManager()}
	routes := []struct{ template, path string }{
		{"/v1/sandbox/{sandbox_id}/command", "/v1/sandbox/sbx-b/command"},
		{"/v1/sandbox/{sandbox_id}/events/stream", "/v1/sandbox/sbx-b/events/stream"},
		{"/v1/sandbox/{session_id}/logs/stream", "/v1/sandbox/sess-b/logs/stream"},
		{"/v1/sandbox/instances/{sandbox_id}/ssh-info", "/v1/sandbox/instances/sbx-b/ssh-info"},
	}
	for _, rt := range routes {
		for name, ctx := range unverifiedContexts() {
			t.Run(rt.template+"/"+name, func(t *testing.T) {
				reached := false
				rec := serveRaw(t, rt.template, s.RequireSandboxOwnership(markerHandler(&reached)), "POST", rt.path, ctx)
				if reached {
					t.Fatal("handler ran for a caller with no verified principal")
				}
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("want 401, got %d (%s)", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

func TestRequireSandboxOwnershipScopesToCallerTenant(t *testing.T) {
	s := &Server{sandboxMgr: ownershipTestManager()}
	const template = "/v1/sandbox/{sandbox_id}/command"

	t.Run("owner reaches the handler with the resolved sandbox", func(t *testing.T) {
		var resolved string
		h := s.RequireSandboxOwnership(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if inst, ok := ResolvedSandboxFromContext(r.Context()); ok {
				resolved = inst.ID
			}
		}))
		rec := serveRaw(t, template, h, "POST", "/v1/sandbox/sbx-a/command", verifiedTenantCtx("tenant-a"))
		if rec.Code != http.StatusOK || resolved != "sbx-a" {
			t.Fatalf("owner should pass with sbx-a resolved, got %d resolved=%q", rec.Code, resolved)
		}
	})

	t.Run("another tenant's sandbox id is 404 and the handler does not run", func(t *testing.T) {
		reached := false
		rec := serveRaw(t, template, s.RequireSandboxOwnership(markerHandler(&reached)), "POST", "/v1/sandbox/sbx-b/command", verifiedTenantCtx("tenant-a"))
		if reached || rec.Code != http.StatusNotFound {
			t.Fatalf("cross-tenant must be 404 without running the handler, got %d reached=%v", rec.Code, reached)
		}
		if strings.Contains(rec.Body.String(), "sbx-b") {
			t.Fatalf("response must not echo the other tenant's sandbox: %s", rec.Body.String())
		}
	})

	t.Run("a colliding name resolves only inside the caller's tenant", func(t *testing.T) {
		var resolved string
		h := s.RequireSandboxOwnership(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if inst, ok := ResolvedSandboxFromContext(r.Context()); ok {
				resolved = inst.ID
			}
		}))
		serveRaw(t, template, h, "POST", "/v1/sandbox/web/command", verifiedTenantCtx("tenant-a"))
		if resolved != "sbx-a" {
			t.Fatalf("tenant-a asking for 'web' must get sbx-a, got %q", resolved)
		}
	})

	t.Run("an API key principal is accepted", func(t *testing.T) {
		reached := false
		ctx := contextkeys.WithAuthenticatedAPIKeyID(context.Background(), "tenant-a", "hash", "key-1")
		rec := serveRaw(t, template, s.RequireSandboxOwnership(markerHandler(&reached)), "POST", "/v1/sandbox/sbx-a/command", ctx)
		if !reached || rec.Code != http.StatusOK {
			t.Fatalf("API key owner should pass, got %d reached=%v", rec.Code, reached)
		}
	})
}

func TestRequireSandboxOwnershipRejectsCrossTenantSessionMapping(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "shared"}},
	})
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"shared": {ID: "sbx-b", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "shared"}},
	})
	s := &Server{sandboxMgr: mgr}
	reached := false
	rec := serveRaw(t, "/v1/sandbox/{sandbox_id}/command", s.RequireSandboxOwnership(markerHandler(&reached)), "POST", "/v1/sandbox/sbx-a/command", verifiedTenantCtx("tenant-a"))
	if reached || rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"sandbox not found"}` {
		t.Fatalf("response = %d %q, reached=%v", rec.Code, rec.Body.String(), reached)
	}
}

func TestRequireVerifiedTenant(t *testing.T) {
	s := &Server{}
	for name, ctx := range unverifiedContexts() {
		t.Run("refuses/"+name, func(t *testing.T) {
			reached := false
			rec := serveRaw(t, "/v1/snapshots", s.RequireVerifiedTenant(markerHandler(&reached)), "GET", "/v1/snapshots", ctx)
			if reached || rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401 without running the handler, got %d reached=%v", rec.Code, reached)
			}
		})
	}
	t.Run("verified caller passes", func(t *testing.T) {
		reached := false
		rec := serveRaw(t, "/v1/snapshots", s.RequireVerifiedTenant(markerHandler(&reached)), "GET", "/v1/snapshots", verifiedTenantCtx("tenant-a"))
		if !reached || rec.Code != http.StatusOK {
			t.Fatalf("verified caller should pass, got %d reached=%v", rec.Code, reached)
		}
	})
}

// The instance verbs, fs and process handlers check ownership inside the
// handler through this helper. It has to refuse the same unverified shapes.
func TestOwnedSandboxHTTPRefusesUnverifiedCallers(t *testing.T) {
	s := &Server{sandboxMgr: ownershipTestManager()}
	for name, ctx := range unverifiedContexts() {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/v1/sandbox/instances/sbx-b/fs/list", nil).WithContext(ctx)
			if _, ok := s.ownedSandboxHTTP(rec, req, "sbx-b"); ok {
				t.Fatal("unverified caller passed the ownership helper")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
		})
	}
}

// collisionTestManager has tenant-a owning a sandbox whose NAME is tenant-b's
// sandbox id. An unscoped id-or-name lookup of "sbx-b" returns tenant-b's
// sandbox (exact id wins), so a check that passes on the name and a handler
// that looks the string up again operate on different sandboxes.
func collisionTestManager() *sandbox.SandboxManager {
	m := &sandbox.SandboxManager{}
	m.SeedInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", Name: "sbx-b", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "sess-a"}},
		"sbx-b": {ID: "sbx-b", Name: "victim", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "sess-b"}},
	})
	return m
}

func TestHandlersActOnTheCheckedSandboxNotTheCallerString(t *testing.T) {
	s := &Server{sandboxMgr: collisionTestManager()}

	t.Run("middleware route: resolveSandbox and the path var name the caller's sandbox", func(t *testing.T) {
		var gotID, gotSession, gotVar string
		h := s.RequireSandboxOwnership(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			gotVar = mux.Vars(r)["sandbox_id"]
			inst, sessionID, err := s.resolveSandbox(r.Context(), gotVar)
			if err != nil {
				t.Fatalf("resolveSandbox: %v", err)
			}
			gotID, gotSession = inst.ID, sessionID
		}))
		serveRaw(t, "/v1/sandbox/{sandbox_id}/command", h, "POST", "/v1/sandbox/sbx-b/command", verifiedTenantCtx("tenant-a"))
		if gotID != "sbx-a" || gotSession != "sess-a" || gotVar != "sbx-a" {
			t.Fatalf("tenant-a was routed to id=%q session=%q var=%q; must stay on its own sbx-a/sess-a", gotID, gotSession, gotVar)
		}
	})

	t.Run("in-handler helper returns the caller's sandbox id", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/sandbox/instances/sbx-b/stop", nil).WithContext(verifiedTenantCtx("tenant-a"))
		inst, ok := s.ownedSandboxHTTP(rec, req, "sbx-b")
		if !ok || inst.ID != "sbx-a" {
			t.Fatalf("want tenant-a's sbx-a, got ok=%v inst=%+v", ok, inst)
		}
	})

	t.Run("resolveSandbox has no unscoped fallback", func(t *testing.T) {
		// Connect path: no middleware instance on the context.
		if inst, _, err := s.resolveSandbox(verifiedTenantCtx("tenant-a"), "victim"); err == nil {
			t.Fatalf("tenant-a resolved tenant-b's sandbox by name: %+v", inst)
		}
		inst, _, err := s.resolveSandbox(verifiedTenantCtx("tenant-a"), "sbx-b")
		if err != nil || inst.ID != "sbx-a" {
			t.Fatalf("tenant-a asking for sbx-b must get its own sandbox of that name, got %+v err=%v", inst, err)
		}
		if _, _, err := s.resolveSandbox(context.Background(), "sbx-b"); err == nil {
			t.Fatal("a context with no tenant resolved a sandbox")
		}
	})
}

// stubQueryBus answers every query the way DefaultQueryBus does: a non-nil
// *query.Response whose Data is nil when the tenant-scoped lookup found
// nothing.
type stubQueryBus struct{ data interface{} }

func (b stubQueryBus) Execute(context.Context, query.Query) (interface{}, error) {
	return &query.Response{Data: b.data}, nil
}
func (stubQueryBus) RegisterHandler(query.QueryHandler) {}

func TestSessionOwnershipReadsTheWrappedResult(t *testing.T) {
	withBus := func(ctx context.Context, data interface{}) context.Context {
		return cqrs.WithSystem(ctx, &cqrs.System{QueryBus: stubQueryBus{data: data}})
	}
	s := &Server{sessionMgr: &agentrt.SessionManager{}}

	t.Run("a session of another tenant is not owned", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/", nil).WithContext(withBus(verifiedTenantCtx("tenant-a"), nil))
		if s.sessionOwnedByTenantHTTP(req, "sess-b", "tenant-a") {
			t.Fatal("an empty tenant-scoped result was treated as ownership")
		}
	})
	t.Run("the caller's own session is owned", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/", nil).WithContext(withBus(verifiedTenantCtx("tenant-a"), struct{}{}))
		if !s.sessionOwnedByTenantHTTP(req, "sess-a", "tenant-a") {
			t.Fatal("the owner's session was refused")
		}
	})
	t.Run("stop-turn on another tenant's session is 404", func(t *testing.T) {
		ctx := withBus(verifiedTenantCtx("tenant-a"), nil)
		rec := serveRaw(t, "/v1/agents/sessions/{session_id}/turns/stop", http.HandlerFunc(s.HandleStopSessionTurnHTTP), "POST", "/v1/agents/sessions/sess-b/turns/stop", ctx)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
		}
	})
}

// Stopping a turn used to take the tenant from the request body, so anyone
// could interrupt another tenant's agent. Answering a pending question had no
// check at all.
func TestSessionRawRoutesRequireVerifiedCaller(t *testing.T) {
	s := &Server{sessionMgr: &agentrt.SessionManager{}}
	routes := []struct {
		template, path string
		h              http.HandlerFunc
	}{
		{"/v1/agents/sessions/{session_id}/turns/stop", "/v1/agents/sessions/sess-b/turns/stop", s.HandleStopSessionTurnHTTP},
		{"/v1/agents/sessions/{session_id}/user-input", "/v1/agents/sessions/sess-b/user-input", s.HandleSubmitUserInputHTTP},
	}
	for _, rt := range routes {
		for name, ctx := range unverifiedContexts() {
			t.Run(rt.template+"/"+name, func(t *testing.T) {
				rec := serveRaw(t, rt.template, rt.h, "POST", rt.path, ctx)
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("want 401, got %d (%s)", rec.Code, rec.Body.String())
				}
			})
		}
		t.Run(rt.template+"/verified caller cannot act on a session that is not theirs", func(t *testing.T) {
			// The body names tenant-b; the caller is tenant-a. With no session
			// store wired the ownership lookup fails closed.
			rec := serveRaw(t, rt.template, rt.h, "POST", rt.path, verifiedTenantCtx("tenant-a"))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// Background command ids are global to the process. Passing the ownership
// check on your own sandbox must not let you read or stop a command that is
// running in someone else's.
func TestCommandIDsAreScopedToTheCheckedSandbox(t *testing.T) {
	tracker := sandbox.NewCommandTracker()
	s := &Server{sandboxMgr: ownershipTestManager(), commandTracker: tracker}
	interrupted := false
	victimCmd := tracker.Track("sbx-b", "cat /secrets", "/", func() { interrupted = true })
	ownCmd := tracker.Track("sbx-a", "echo mine", "/", func() {})

	get := func(template, path string, h http.HandlerFunc) *httptest.ResponseRecorder {
		return serveRaw(t, template, s.RequireSandboxOwnership(h), "GET", path, verifiedTenantCtx("tenant-a"))
	}

	t.Run("status of another sandbox's command is 404", func(t *testing.T) {
		rec := get("/v1/sandbox/{sandbox_id}/command/status/{cmd_id}", "/v1/sandbox/sbx-a/command/status/"+victimCmd, s.HandleSandboxCommandStatus)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secrets") {
			t.Fatalf("want 404 with no command text, got %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("logs of another sandbox's command are 404", func(t *testing.T) {
		rec := get("/v1/sandbox/{sandbox_id}/command/{cmd_id}/logs", "/v1/sandbox/sbx-a/command/"+victimCmd+"/logs", s.HandleSandboxCommandLogs)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("another sandbox's command cannot be interrupted", func(t *testing.T) {
		r := mux.NewRouter()
		r.Handle("/v1/sandbox/{sandbox_id}/command", s.RequireSandboxOwnership(http.HandlerFunc(s.HandleSandboxCommandInterrupt)))
		req := httptest.NewRequest("DELETE", "/v1/sandbox/sbx-a/command", strings.NewReader(`{"command_id":"`+victimCmd+`"}`)).WithContext(verifiedTenantCtx("tenant-a"))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || interrupted {
			t.Fatalf("want 404 and no interrupt, got %d interrupted=%v", rec.Code, interrupted)
		}
	})
	t.Run("the caller's own command is still readable", func(t *testing.T) {
		rec := get("/v1/sandbox/{sandbox_id}/command/status/{cmd_id}", "/v1/sandbox/sbx-a/command/status/"+ownCmd, s.HandleSandboxCommandStatus)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "echo mine") {
			t.Fatalf("owner should read its own command, got %d %s", rec.Code, rec.Body.String())
		}
	})
}
