package tools

import (
	"context"
	"database/sql"
	"testing"

	"github.com/everstacklabs/everstack/internal/agents/projectruntime"
	"github.com/everstacklabs/everstack/internal/agents/revision"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/fnexec"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

type spawnRevisionStore struct {
	active *revision.Revision
}

func (s *spawnRevisionStore) CreateAndActivate(context.Context, string, string, string, *revision.Manifest) (*revision.Revision, bool, error) {
	panic("not used")
}
func (s *spawnRevisionStore) Get(context.Context, string, string) (*revision.Revision, error) {
	panic("not used")
}
func (s *spawnRevisionStore) GetActive(context.Context, string, string) (*revision.Revision, error) {
	return s.active, nil
}
func (s *spawnRevisionStore) GetForSession(context.Context, string, string, string) (*revision.Revision, error) {
	panic("not used")
}

type spawnProjectRunner struct{}

func (spawnProjectRunner) Run(context.Context, fnexec.Execer, projectruntime.RunRequest) *isolation.ExecutionResult {
	return &isolation.ExecutionResult{Success: true}
}

func TestSpawnAgentRegistersTheSelectedChildRevisionFunctions(t *testing.T) {
	manifest, err := revision.NewManifest([]revision.File{{
		Path: "child.ts", Content: []byte("export const run = () => 'child'\n"),
	}}, []revision.Function{{
		Name: "child_function", Path: "child.ts", Export: "run", Runtime: isolation.RuntimeDeno,
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := &SpawnAgentHandler{
		RevisionStore: &spawnRevisionStore{active: &revision.Revision{
			ID: "child-revision", AgentID: "child-agent", Manifest: *manifest,
		}},
		ProjectRuntime: spawnProjectRunner{},
	}
	interceptor := NewToolInterceptor(nil)
	tools := []string{"child_function"}

	err = handler.registerChildProjectFunctions(
		context.Background(), interceptor, &SandboxSessionContext{}, "tenant-1", "child-agent",
		map[string]interface{}{"sandbox": map[string]interface{}{"enabled": true, "network_mode": "deny"}}, &tools,
	)
	if err != nil {
		t.Fatalf("registerChildProjectFunctions() error = %v", err)
	}
	projectHandler, ok := interceptor.Handlers["child_function"].(*ProjectFunctionHandler)
	if !ok {
		t.Fatalf("child function handler = %T", interceptor.Handlers["child_function"])
	}
	if projectHandler.Revision.ID != "child-revision" {
		t.Fatalf("handler revision = %q, want child-revision", projectHandler.Revision.ID)
	}
}

func TestSelectedChildSandboxPolicyUsesChildDefinition(t *testing.T) {
	t.Parallel()

	config := map[string]interface{}{
		"sandbox": map[string]interface{}{
			"enabled":      true,
			"memory_mb":    float64(1536),
			"network_mode": "deny",
		},
		"browser": map[string]interface{}{
			"enabled":  true,
			"headless": false,
		},
	}

	sandboxConfig, browserConfig, needsSandbox, err := selectedChildSandboxPolicy(
		config, "child-agent", false, true,
	)
	if err != nil {
		t.Fatalf("selectedChildSandboxPolicy() error = %v", err)
	}
	if !needsSandbox || !sandboxConfig.Enabled {
		t.Fatal("selected child sandbox was not enabled")
	}
	if sandboxConfig.MemoryMB != 1536 || sandboxConfig.NetworkMode != "deny" {
		t.Fatalf("child sandbox policy = %#v", sandboxConfig)
	}
	if !browserConfig.Enabled || browserConfig.Headless {
		t.Fatalf("child browser policy = %#v", browserConfig)
	}
}

func TestSelectedChildRuntimeConfigUsesProjectedSandboxPolicy(t *testing.T) {
	t.Parallel()

	config := AgentRuntimeConfig(&agentsquery.AgentDefinitionReadModel{
		LifecycleMode:         "persistent",
		Config:                []byte(`{"sandbox":{"enabled":true,"memory_mb":128,"network_mode":"allow"}}`),
		SandboxMemoryMB:       sql.NullInt32{Int32: 2048, Valid: true},
		SandboxNetworkMode:    sql.NullString{String: "deny", Valid: true},
		SandboxTimeoutSeconds: sql.NullInt32{Int32: 900, Valid: true},
		SandboxAllowedHosts:   []string{"packages.example.com"},
		SandboxEnvVars:        []byte(`{"RUNTIME_ENV":"child"}`),
		SandboxGitRepoURL:     sql.NullString{String: "https://example.com/child.git", Valid: true},
		SandboxGitBranch:      sql.NullString{String: "release", Valid: true},
		SandboxSSHEnabled:     sql.NullBool{Bool: true, Valid: true},
	})
	sandboxConfig := sandbox.ParseSandboxConfig(config)
	if sandboxConfig.MemoryMB != 2048 || sandboxConfig.NetworkMode != "deny" || sandboxConfig.TimeoutSeconds != 900 {
		t.Fatalf("projected child sandbox policy = %#v", sandboxConfig)
	}
	if len(sandboxConfig.AllowedHosts) != 1 || sandboxConfig.AllowedHosts[0] != "packages.example.com" {
		t.Fatalf("projected child allowed hosts = %v", sandboxConfig.AllowedHosts)
	}
	if sandboxConfig.EnvVars["RUNTIME_ENV"] != "child" || sandboxConfig.GitBranch != "release" || !sandboxConfig.SSHEnabled {
		t.Fatalf("projected child sandbox settings = %#v", sandboxConfig)
	}
}

func TestSelectedEphemeralChildRuntimeConfigPreservesProjectSandboxPolicy(t *testing.T) {
	t.Parallel()

	config := AgentRuntimeConfig(&agentsquery.AgentDefinitionReadModel{
		LifecycleMode:         "ephemeral",
		Config:                []byte(`{"sandbox":{"enabled":true,"memory_mb":1536,"network_mode":"deny","env_vars":{"PROJECT_ENV":"kept"}}}`),
		SandboxMemoryMB:       sql.NullInt32{Int32: 0, Valid: true},
		SandboxNetworkMode:    sql.NullString{String: "", Valid: true},
		SandboxTimeoutSeconds: sql.NullInt32{Int32: 0, Valid: true},
		SandboxEnvVars:        []byte(`{}`),
	})

	sandboxConfig := sandbox.ParseSandboxConfig(config)
	if sandboxConfig.MemoryMB != 1536 || sandboxConfig.NetworkMode != "deny" {
		t.Fatalf("ephemeral child sandbox policy = %#v", sandboxConfig)
	}
	if sandboxConfig.EnvVars["PROJECT_ENV"] != "kept" {
		t.Fatalf("ephemeral child environment = %#v", sandboxConfig.EnvVars)
	}
}

func TestPersistentAgentRuntimeConfigPreservesNestedPolicyWhenProjectionContainsDefaults(t *testing.T) {
	t.Parallel()

	config := AgentRuntimeConfig(&agentsquery.AgentDefinitionReadModel{
		LifecycleMode:      "persistent",
		Config:             []byte(`{"sandbox":{"memory_mb":1536,"network_mode":"deny","env_vars":{"PROJECT_ENV":"kept"}}}`),
		SandboxMemoryMB:    sql.NullInt32{Int32: 0, Valid: true},
		SandboxNetworkMode: sql.NullString{String: "", Valid: true},
		SandboxEnvVars:     []byte(`{}`),
	})

	sandboxConfig := sandbox.ParseSandboxConfig(config)
	if !sandboxConfig.Enabled || !sandboxConfig.Persistent {
		t.Fatalf("persistent sandbox flags = %#v", sandboxConfig)
	}
	if sandboxConfig.MemoryMB != 1536 || sandboxConfig.NetworkMode != "deny" {
		t.Fatalf("persistent sandbox policy = %#v", sandboxConfig)
	}
	if sandboxConfig.EnvVars["PROJECT_ENV"] != "kept" {
		t.Fatalf("persistent sandbox environment = %#v", sandboxConfig.EnvVars)
	}
}

func TestPersistentAgentRuntimeConfigBuildsPolicyFromTypedSandboxFields(t *testing.T) {
	t.Parallel()

	config := AgentRuntimeConfig(&agentsquery.AgentDefinitionReadModel{
		LifecycleMode:      "persistent",
		SandboxNetworkMode: sql.NullString{String: "deny", Valid: true},
	})

	if err := projectruntime.ValidateFunctionSandboxPolicy(config); err != nil {
		t.Fatalf("typed persistent sandbox policy was rejected: %v", err)
	}
	sandboxConfig := sandbox.ParseSandboxConfig(config)
	if !sandboxConfig.Enabled || !sandboxConfig.Persistent || sandboxConfig.NetworkMode != "deny" {
		t.Fatalf("typed persistent sandbox policy = %#v", sandboxConfig)
	}
}

func TestSelectedPersistentChildForcesItsOwnSandboxIdentity(t *testing.T) {
	t.Parallel()

	sandboxConfig, _, needsSandbox, err := selectedChildSandboxPolicy(
		map[string]interface{}{}, "child-agent", true, false,
	)
	if err != nil {
		t.Fatalf("selectedChildSandboxPolicy() error = %v", err)
	}
	if !needsSandbox || !sandboxConfig.Enabled || !sandboxConfig.Persistent {
		t.Fatalf("persistent child sandbox policy = %#v", sandboxConfig)
	}
	if sandboxConfig.AgentID != "child-agent" {
		t.Fatalf("persistent child agent ID = %q", sandboxConfig.AgentID)
	}
}

func TestSelectedChildRuntimeHandlersAreRebound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler SyntheticToolHandler
		want    bool
	}{
		{name: "sandbox_execute", want: true},
		{name: "browser_navigate", want: true},
		{name: "use_skill", want: true},
		{name: "project_function", handler: &ProjectFunctionHandler{}, want: true},
		{name: "web_fetch", want: false},
		{name: "memory_query", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectedChildOwnsRuntimeHandler(tt.name, tt.handler); got != tt.want {
				t.Fatalf("selectedChildOwnsRuntimeHandler(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestWithoutSelectedChildRuntimeTools(t *testing.T) {
	t.Parallel()

	got := withoutSelectedChildRuntimeTools([]string{
		"web_fetch", "sandbox_execute", "browser_navigate", "use_skill", "child_function",
	})
	want := []string{"web_fetch", "child_function"}
	if len(got) != len(want) {
		t.Fatalf("filtered tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filtered tools = %v, want %v", got, want)
		}
	}
}

func TestParseApprovedArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   interface{}
		want bool
	}{
		{name: "bool true", in: true, want: true},
		{name: "bool false", in: false, want: false},
		{name: "string true", in: "true", want: true},
		{name: "string yes", in: "yes", want: true},
		{name: "string approved", in: "approved", want: true},
		{name: "string no", in: "no", want: false},
		{name: "number one", in: float64(1), want: true},
		{name: "number zero", in: float64(0), want: false},
		{name: "nil", in: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseApprovedArg(tt.in)
			if got != tt.want {
				t.Fatalf("parseApprovedArg(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestHasExplicitDelegationApproval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want bool
	}{
		{text: "Yes, run the research", want: true},
		{text: "I confirm", want: true},
		{text: "approved, go ahead", want: true},
		{text: "no, do not run it", want: false},
		{text: "don't proceed", want: false},
		{text: "maybe later", want: false},
		{text: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := hasExplicitDelegationApproval(tt.text)
			if got != tt.want {
				t.Fatalf("hasExplicitDelegationApproval(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
