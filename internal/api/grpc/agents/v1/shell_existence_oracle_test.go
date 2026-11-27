package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestShellFamilyAuthenticatesBeforeResourceLookup(t *testing.T) {
	t.Run("authentication precedes sandbox availability", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/sandbox/sbx-a/shell", nil)
		req = mux.SetURLVars(req, map[string]string{"sandbox_id": "sbx-a"})
		rec := httptest.NewRecorder()
		(&Server{}).HandleSandboxShellByIDOrName(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
		}
	})

	mgr := &sandbox.SandboxManager{}
	mgr.SeedLiveInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {
			ID:             "sbx-a",
			LifecycleState: sandbox.LifecycleRunning,
			Config:         sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "sess-a"},
		},
	})
	s := &Server{sandboxMgr: mgr}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		vars    func(string) map[string]string
	}{
		{"shell by session", s.HandleSandboxShell, func(id string) map[string]string { return map[string]string{"session_id": id} }},
		{"shell by id", s.HandleSandboxShellByIDOrName, func(id string) map[string]string { return map[string]string{"sandbox_id": id} }},
		{"browser by session", s.HandleBrowserStream, func(id string) map[string]string { return map[string]string{"session_id": id} }},
		{"browser by id", s.HandleBrowserStreamByIDOrName, func(id string) map[string]string { return map[string]string{"sandbox_id": id} }},
		{"sessions by id", s.HandleListSandboxShellSessions, func(id string) map[string]string { return map[string]string{"sandbox_id": id} }},
		{"kill session by id", s.HandleKillSandboxShellSession, func(id string) map[string]string {
			return map[string]string{"sandbox_id": id, "shell_session_id": "shell-1"}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := func(id string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/v1/sandbox/test", nil)
				req = mux.SetURLVars(req, tc.vars(id))
				rec := httptest.NewRecorder()
				tc.handler(rec, req)
				return rec
			}
			existingID, missingID := "sbx-a", "sbx-missing"
			if tc.vars("probe")["session_id"] != "" {
				existingID, missingID = "sess-a", "sess-missing"
			}
			existing := request(existingID)
			missing := request(missingID)
			if existing.Code != http.StatusUnauthorized || existing.Code != missing.Code || existing.Body.String() != missing.Body.String() {
				t.Fatalf("existing = %d %q, missing = %d %q", existing.Code, existing.Body.String(), missing.Code, missing.Body.String())
			}
		})
	}
}

func TestLookupShellInstanceByIDOrNameScopesAuthenticatedRequest(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedLiveInstancesForTest(map[string]*sandbox.Instance{
		"sbx-b": {
			ID:             "sbx-b",
			Name:           "database",
			LifecycleState: sandbox.LifecycleRunning,
			Config:         sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "sess-b"},
		},
	})
	s := &Server{sandboxMgr: mgr}
	req := httptest.NewRequest(http.MethodGet, "/v1/sandbox/sbx-b/shell", nil).WithContext(verifiedTenantCtx("tenant-a"))
	tenantID, scoped, err := s.prepareShellLookup(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.lookupShellInstanceByIDOrName(req.Context(), "sbx-b", tenantID, scoped); ok {
		t.Fatal("authenticated tenant-a request resolved tenant-b sandbox by id")
	}
	if _, ok := s.lookupShellInstanceByIDOrName(req.Context(), "database", tenantID, scoped); ok {
		t.Fatal("authenticated tenant-a request resolved tenant-b sandbox by name")
	}
}

func TestLookupShellInstanceBySessionScopesAuthenticatedRequest(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{
		"sess-b": {
			ID:             "sbx-b",
			LifecycleState: sandbox.LifecycleRunning,
			Config:         sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "sess-b"},
		},
	})
	s := &Server{sandboxMgr: mgr}
	req := httptest.NewRequest(http.MethodGet, "/v1/sandbox/sess-b/shell", nil).WithContext(verifiedTenantCtx("tenant-a"))
	tenantID, scoped, err := s.prepareShellLookup(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.lookupShellInstanceBySession(req.Context(), "sess-b", tenantID, scoped); ok {
		t.Fatal("authenticated tenant-a request resolved tenant-b sandbox by session")
	}
}
