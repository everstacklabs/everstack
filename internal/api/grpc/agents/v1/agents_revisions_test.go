package v1

import (
	"context"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/agents/projectruntime"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

type revisionPolicyStore struct {
	revision *agentrevision.Revision
}

func (s *revisionPolicyStore) CreateAndActivate(context.Context, string, string, string, *agentrevision.Manifest) (*agentrevision.Revision, bool, error) {
	panic("not used")
}
func (s *revisionPolicyStore) Get(context.Context, string, string) (*agentrevision.Revision, error) {
	panic("not used")
}
func (s *revisionPolicyStore) GetActive(context.Context, string, string) (*agentrevision.Revision, error) {
	panic("not used")
}
func (s *revisionPolicyStore) GetForSession(context.Context, string, string, string) (*agentrevision.Revision, error) {
	return s.revision, nil
}

type revisionPolicyRunner struct{}

func (revisionPolicyRunner) Run(context.Context, fnexec.Execer, projectruntime.RunRequest) *isolation.ExecutionResult {
	return &isolation.ExecutionResult{Success: true}
}

func TestRegisterProjectFunctionsFailsClosedOnInvalidNetworkPolicy(t *testing.T) {
	t.Parallel()

	manifest, err := agentrevision.NewManifest(
		[]agentrevision.File{{Path: "run.py", Content: []byte("def handler(args): return args\n")}},
		[]agentrevision.Function{{Name: "run_project", Path: "run.py", Runtime: isolation.RuntimePython3}},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		revisionStore:  &revisionPolicyStore{revision: &agentrevision.Revision{ID: "revision-1", Manifest: *manifest}},
		projectRuntime: revisionPolicyRunner{},
	}
	interceptor := agenttools.NewToolInterceptor(nil)
	sandboxCtx := &agenttools.SandboxSessionContext{Manager: &sandbox.SandboxManager{}}
	tools := []string{}

	err = server.registerProjectFunctions(
		context.Background(), interceptor, sandboxCtx, "tenant-1", "agent-1", "session-1",
		map[string]interface{}{"sandbox": map[string]interface{}{"enabled": true, "network_mode": "denyy"}}, &tools,
	)
	if err == nil || !strings.Contains(err.Error(), "network_mode must be deny, whitelist or allow") {
		t.Fatalf("registerProjectFunctions() error = %v, want fail-closed network policy error", err)
	}
	if interceptor.IsSyntheticTool("run_project") {
		t.Fatal("project function was registered despite invalid network policy")
	}
}
