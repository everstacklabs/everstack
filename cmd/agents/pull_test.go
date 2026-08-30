package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	"github.com/everstacklabs/everstack/internal/cli/agentproject"
	"github.com/everstacklabs/everstack/internal/cli/client"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1/agentsconnect"
	functionsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1/functionsconnect"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestValidateRemotePathSegment(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", ".", "..", "../escape", "nested/escape", `nested\escape`, "/absolute"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if err := validateRemotePathSegment(value, "remote name"); err == nil {
				t.Fatalf("validateRemotePathSegment(%q) succeeded", value)
			}
		})
	}
	if err := validateRemotePathSegment("risk-reviewer_2", "remote name"); err != nil {
		t.Fatalf("valid segment rejected: %v", err)
	}
}

type pullGraphAgentsHandler struct {
	agentsconnect.UnimplementedAgentsServiceHandler
	triggers  map[string][]*agentsv1.AgentTrigger
	links     map[string][]*agentsv1.AgentLink
	agents    map[string]*agentsv1.AgentDefinition
	revisions map[string]*agentsv1.AgentRevision
}

func (h *pullGraphAgentsHandler) GetActiveAgentRevision(
	_ context.Context,
	req *connect.Request[agentsv1.GetActiveAgentRevisionRequest],
) (*connect.Response[agentsv1.GetActiveAgentRevisionResponse], error) {
	revision := h.revisions[req.Msg.GetAgentId()]
	if revision == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&agentsv1.GetActiveAgentRevisionResponse{Revision: revision}), nil
}

func (h *pullGraphAgentsHandler) ListAgentTriggers(
	_ context.Context,
	req *connect.Request[agentsv1.ListAgentTriggersRequest],
) (*connect.Response[agentsv1.ListAgentTriggersResponse], error) {
	return connect.NewResponse(&agentsv1.ListAgentTriggersResponse{Triggers: h.triggers[req.Msg.GetAgentId()]}), nil
}

func (h *pullGraphAgentsHandler) ListAgentLinks(
	_ context.Context,
	req *connect.Request[agentsv1.ListAgentLinksRequest],
) (*connect.Response[agentsv1.ListAgentLinksResponse], error) {
	return connect.NewResponse(&agentsv1.ListAgentLinksResponse{Links: h.links[req.Msg.GetAgentId()]}), nil
}

func (h *pullGraphAgentsHandler) GetAgent(
	_ context.Context,
	req *connect.Request[agentsv1.GetAgentRequest],
) (*connect.Response[agentsv1.GetAgentResponse], error) {
	agent := h.agents[req.Msg.GetId()]
	if agent == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&agentsv1.GetAgentResponse{Agent: agent}), nil
}

func (h *pullGraphAgentsHandler) ListAgents(
	_ context.Context,
	_ *connect.Request[agentsv1.ListAgentsRequest],
) (*connect.Response[agentsv1.ListAgentsResponse], error) {
	agents := make([]*agentsv1.AgentDefinition, 0, len(h.agents))
	for _, agent := range h.agents {
		agents = append(agents, agent)
	}
	return connect.NewResponse(&agentsv1.ListAgentsResponse{Agents: agents}), nil
}

type pullGraphFunctionsHandler struct {
	functionsconnect.UnimplementedFunctionsServiceHandler
	functions map[string]*functionsv1.Function
	getCalls  int
}

func TestPullLegacyAgentAllowsEmptyInstructions(t *testing.T) {
	agent := &agentsv1.AgentDefinition{
		Id: "agent-1", Name: "no-prompt", Model: "model", SystemPrompt: "",
		Mode:   agentsv1.AgentMode_AGENT_MODE_PRIMARY,
		Config: mustStruct(t, map[string]any{}),
	}
	agentsHandler := &pullGraphAgentsHandler{
		triggers:  map[string][]*agentsv1.AgentTrigger{},
		links:     map[string][]*agentsv1.AgentLink{},
		revisions: map[string]*agentsv1.AgentRevision{},
	}
	target := t.TempDir()

	if _, err := pullAgentInto(context.Background(), newPullGraphFactory(t, agentsHandler, &pullGraphFunctionsHandler{}), target, agent, true); err != nil {
		t.Fatalf("pullAgentInto() error = %v", err)
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatalf("Load(pulled project) error = %v", err)
	}
	if project.Instructions != "" {
		t.Fatalf("pulled instructions = %q, want empty", project.Instructions)
	}
}

func TestPullLegacyAgentOmitsAmbiguousInvalidTriggerGroup(t *testing.T) {
	agent := &agentsv1.AgentDefinition{
		Id: "agent-1", Name: "support", Model: "model", SystemPrompt: "",
		Mode:   agentsv1.AgentMode_AGENT_MODE_PRIMARY,
		Config: mustStruct(t, map[string]any{}),
	}
	agentsHandler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{"agent-1": {
			{Id: "cron-1", Name: "incoming", TriggerType: "cron", Enabled: true},
			{Id: "webhook-1", Name: "incoming", TriggerType: "webhook", Enabled: true, CronTimezone: "UTC"},
		}},
		links:     map[string][]*agentsv1.AgentLink{},
		revisions: map[string]*agentsv1.AgentRevision{},
	}
	target := t.TempDir()

	summary, err := pullAgentInto(context.Background(), newPullGraphFactory(t, agentsHandler, &pullGraphFunctionsHandler{}), target, agent, true)
	if err != nil {
		t.Fatalf("pullAgentInto() error = %v", err)
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatalf("Load(pulled project) error = %v", err)
	}
	if len(project.Config.Triggers) != 0 {
		t.Fatalf("pulled triggers = %+v, want ambiguous legacy group omitted", project.Config.Triggers)
	}
	if len(summary.warnings) != 1 || !strings.Contains(summary.warnings[0], "incoming") || !strings.Contains(summary.warnings[0], "left dashboard-owned") {
		t.Fatalf("pull warnings = %q", summary.warnings)
	}
}

func TestPullLegacyAgentOmitsUniqueInvalidCron(t *testing.T) {
	agent := &agentsv1.AgentDefinition{
		Id: "agent-1", Name: "support", Model: "model", Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
		Config: mustStruct(t, map[string]any{}),
	}
	handler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{"agent-1": {{
			Id: "cron-1", Name: "daily", TriggerType: "cron", CronExpression: "not-a-cron", CronTimezone: "UTC", Enabled: true,
		}}},
		links: map[string][]*agentsv1.AgentLink{}, revisions: map[string]*agentsv1.AgentRevision{},
	}
	target := t.TempDir()

	summary, err := pullAgentInto(context.Background(), newPullGraphFactory(t, handler, &pullGraphFunctionsHandler{}), target, agent, true)
	if err != nil {
		t.Fatalf("pullAgentInto() error = %v", err)
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Config.Triggers) != 0 || len(summary.warnings) != 1 || !strings.Contains(summary.warnings[0], "invalid cron expression") {
		t.Fatalf("pulled triggers = %+v, warnings = %q", project.Config.Triggers, summary.warnings)
	}
}

func (h *pullGraphFunctionsHandler) GetFunctionByName(
	_ context.Context,
	req *connect.Request[functionsv1.GetFunctionByNameRequest],
) (*connect.Response[functionsv1.GetFunctionByNameResponse], error) {
	h.getCalls++
	function := h.functions[req.Msg.GetName()]
	if function == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&functionsv1.GetFunctionByNameResponse{Function: function}), nil
}

func TestPullRestoresRevisionFilesWithoutReadingGlobalFunctions(t *testing.T) {
	manifest, err := agentrevision.NewManifest([]agentrevision.File{
		{Path: "agent.yaml", Content: []byte("name: support\ndescription: Source description\nmodel: model-old\ninstructions: ./prompts/system.md\nlimits:\n  max_turns: 2\npermissions:\n  task_mode: ask\nconfig:\n  temperature: 0.1\ntools:\n  - web_search\ntriggers:\n  - name: daily\n    type: cron\n    schedule: 0 9 * * *\nfiles:\n  - ./src\nfunctions:\n  lookup_customer:\n    file: ./src/lookup.ts\n    export: lookupCustomer\n    description: Find a customer\n    parameters:\n      type: object\n      required: [email]\n")},
		{Path: "prompts/system.md", Content: []byte("Help users.\n")},
		{Path: "src/lookup.ts", Mode: 0o755, Content: []byte("import { normalize } from './normalize.ts';\nexport const lookupCustomer = ({ email }) => normalize(email);\n")},
		{Path: "src/normalize.ts", Content: []byte("export const normalize = (value: string) => value.trim().toLowerCase();\n")},
	}, []agentrevision.Function{{
		Name: "lookup_customer", Description: "Find a customer", Path: "src/lookup.ts",
		Export: "lookupCustomer", Runtime: isolation.RuntimeDeno,
		Parameters: map[string]any{"type": "object", "required": []any{"email"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	remoteRevision := revisionProtoForPullTest(manifest, "revision-1", "agent-1")
	maxSteps := int32(12)
	agent := &agentsv1.AgentDefinition{
		Id: "agent-1", Name: "support", Description: "Dashboard description", Model: "model-dashboard",
		SystemPrompt: "Use the dashboard instructions.", Tools: []string{"browser", "lookup_customer"},
		Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY, MaxTurns: 8, MaxToolCallsPerTurn: 3,
		MaxSteps: &maxSteps, TaskPermissionMode: agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ALWAYS,
		Config: mustStruct(t, map[string]any{
			"temperature": 0.7,
			"sandbox":     map[string]any{"enabled": true, "network_mode": "deny"},
			"skills":      []any{map[string]any{"name": "generated"}},
			projectMetaKey: map[string]any{
				"source": "evs deploy", "hash": manifest.Digest,
				"managed_triggers": []any{map[string]any{"id": "trigger-1", "name": "daily"}},
			},
		}),
	}
	agentsHandler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{"agent-1": {{
			Id: "trigger-1", Name: "daily", TriggerType: "cron", CronExpression: "0 10 * * *", Enabled: true,
		}}}, links: map[string][]*agentsv1.AgentLink{},
		revisions: map[string]*agentsv1.AgentRevision{"agent-1": remoteRevision},
	}
	functionsHandler := &pullGraphFunctionsHandler{}
	target := t.TempDir()

	summary, err := pullAgentInto(context.Background(), newPullGraphFactory(t, agentsHandler, functionsHandler), target, agent, true)
	if err != nil {
		t.Fatalf("pullAgentInto() error = %v", err)
	}
	if functionsHandler.getCalls != 0 {
		t.Fatalf("global Function lookups = %d, want 0", functionsHandler.getCalls)
	}
	if summary.tools != 2 {
		t.Fatalf("tool summary = %d, want 2", summary.tools)
	}
	for path, want := range map[string]string{
		"src/lookup.ts":    "import { normalize } from './normalize.ts';\nexport const lookupCustomer = ({ email }) => normalize(email);\n",
		"src/normalize.ts": "export const normalize = (value: string) => value.trim().toLowerCase();\n",
	} {
		got, readErr := os.ReadFile(filepath.Join(target, filepath.FromSlash(path)))
		if readErr != nil || string(got) != want {
			t.Fatalf("pulled %s = %q, error = %v", path, got, readErr)
		}
	}
	info, err := os.Stat(filepath.Join(target, "src", "lookup.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("lookup.ts mode = %#o, want %#o", info.Mode().Perm(), os.FileMode(0o755))
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatalf("Load(pulled revision) error = %v", err)
	}
	if project.RevisionManifest.Digest == manifest.Digest {
		t.Fatal("dashboard definition edits did not produce a new desired revision")
	}
	if project.Config.Description != "Dashboard description" || project.Config.Model != "model-dashboard" ||
		project.Instructions != "Use the dashboard instructions." {
		t.Fatalf("pulled dashboard definition = %+v, instructions = %q", project.Config, project.Instructions)
	}
	if project.Config.Limits.MaxTurns != 8 || project.Config.Limits.MaxToolCallsPerTurn != 3 ||
		project.Config.Limits.MaxSteps != 12 || project.Config.Permissions.TaskMode != "always" {
		t.Fatalf("pulled dashboard limits or permissions = %+v, %+v", project.Config.Limits, project.Config.Permissions)
	}
	if project.Config.Config["temperature"] != 0.7 || project.Config.Config["skills"] != nil ||
		project.Config.Config["sandbox"] != nil || project.Config.Config[projectMetaKey] != nil {
		t.Fatalf("pulled dashboard config = %#v", project.Config.Config)
	}
	if !reflect.DeepEqual(project.BuiltinTools, []string{"browser"}) || len(project.ToolFiles) != 1 || project.ToolFiles[0].Name != "lookup_customer" {
		t.Fatalf("pulled dashboard tools = builtins %v, project functions %+v", project.BuiltinTools, project.ToolFiles)
	}
	if len(project.Config.Triggers) != 1 || project.Config.Triggers[0].Schedule != "0 10 * * *" {
		t.Fatalf("pulled dashboard triggers = %+v", project.Config.Triggers)
	}
}

func TestPullRevisionWithoutDashboardEditsPreservesDigest(t *testing.T) {
	agentYAML := []byte("# keep this comment and ordering\nname: support\nmodel: model\ninstructions: ./prompts/system.md\ntools:\n  - web_search\ntriggers:\n  - type: cron\n    schedule: 0 9 * * *\nfunctions:\n  lookup_customer:\n    file: ./src/lookup.ts\n    export: lookupCustomer\n    parameters:\n      type: object\n")
	manifest, err := agentrevision.NewManifest([]agentrevision.File{
		{Path: "agent.yaml", Content: agentYAML},
		{Path: "prompts/system.md", Content: []byte("Help users.\n")},
		{Path: "src/lookup.ts", Content: []byte("export const lookupCustomer = (args) => args;\n")},
	}, []agentrevision.Function{{
		Name: "lookup_customer", Path: "src/lookup.ts", Export: "lookupCustomer", Runtime: isolation.RuntimeDeno,
		Parameters: map[string]any{"type": "object"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentsv1.AgentDefinition{
		Id: "agent-1", Name: "support", Model: "model", SystemPrompt: "Help users.",
		Tools: []string{"web_search", "lookup_customer"}, Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
		TaskPermissionMode: agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ASK,
		Config: mustStruct(t, map[string]any{
			"sandbox": map[string]any{"enabled": true, "network_mode": "deny"},
			projectMetaKey: map[string]any{
				"source": "evs deploy", "hash": manifest.Digest,
				"managed_triggers": []any{map[string]any{"id": "trigger-1", "name": "support-cron-1"}},
			},
		}),
	}
	handler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{"agent-1": {{
			Id: "trigger-1", Name: "support-cron-1", TriggerType: "cron", CronExpression: "0 9 * * *", CronTimezone: "UTC", Enabled: true,
		}}}, links: map[string][]*agentsv1.AgentLink{},
		revisions: map[string]*agentsv1.AgentRevision{"agent-1": revisionProtoForPullTest(manifest, "revision-1", "agent-1")},
	}
	target := t.TempDir()
	if _, err := pullAgentInto(context.Background(), newPullGraphFactory(t, handler, &pullGraphFunctionsHandler{}), target, agent, true); err != nil {
		t.Fatalf("pullAgentInto() error = %v", err)
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if project.RevisionManifest.Digest != manifest.Digest {
		t.Fatalf("unchanged pull digest = %s, want %s", project.RevisionManifest.Digest, manifest.Digest)
	}
	pulledYAML, err := os.ReadFile(filepath.Join(target, "agent.yaml"))
	if err != nil || !reflect.DeepEqual(pulledYAML, agentYAML) {
		t.Fatalf("unchanged agent.yaml = %q, error = %v", pulledYAML, err)
	}
}

func TestPullRestoresRevisionSubagentAtStampedProjectPath(t *testing.T) {
	rootManifest, err := agentrevision.NewManifest([]agentrevision.File{
		{Path: "agent.yaml", Content: []byte("name: root\nmodel: model\nsubagents:\n  - ./workers/risk\n  - ./workers/obsolete\n")},
		{Path: "instructions.md", Content: []byte("Coordinate.\n")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	childManifest, err := agentrevision.NewManifest([]agentrevision.File{
		{Path: "agent.yaml", Content: []byte("name: risk-reviewer\nmodel: model\n")},
		{Path: "instructions.md", Content: []byte("Review risk.\n")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := &agentsv1.AgentDefinition{
		Id: "root-id", Name: "root", Model: "model", SystemPrompt: "Coordinate.",
		Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
		Config: mustStruct(t, map[string]any{projectMetaKey: map[string]any{
			"subagent_paths": map[string]any{"risk-reviewer": "./workers/risk"},
		}}),
	}
	child := &agentsv1.AgentDefinition{
		Id: "child-id", Name: "risk-reviewer", Model: "model", SystemPrompt: "Review risk.",
		Mode: agentsv1.AgentMode_AGENT_MODE_SUBAGENT,
	}
	agentsHandler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{},
		links: map[string][]*agentsv1.AgentLink{"root-id": {{
			Id: "link-1", TargetId: "child-id", TargetType: agentLinkTargetAgent,
			LinkType: agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE,
		}}},
		agents: map[string]*agentsv1.AgentDefinition{"child-id": child},
		revisions: map[string]*agentsv1.AgentRevision{
			"root-id":  revisionProtoForPullTest(rootManifest, "root-revision", "root-id"),
			"child-id": revisionProtoForPullTest(childManifest, "child-revision", "child-id"),
		},
	}
	target := t.TempDir()
	summary, err := pullAgentInto(context.Background(), newPullGraphFactory(t, agentsHandler, &pullGraphFunctionsHandler{}), target, root, true)
	if err != nil {
		t.Fatalf("pullAgentInto() error = %v", err)
	}
	if summary.subagents != 1 {
		t.Fatalf("subagent summary = %d, want 1", summary.subagents)
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project.Config.Subagents, []string{"./workers/risk"}) {
		t.Fatalf("pulled current subordinate links = %v", project.Config.Subagents)
	}
	content, err := os.ReadFile(filepath.Join(target, "workers", "risk", "instructions.md"))
	if err != nil || string(content) != "Review risk.\n" {
		t.Fatalf("custom-path subagent instructions = %q, error = %v", content, err)
	}
}

func TestPullRevisionAddsDashboardLinkedSubagentAtSafeDefaultPath(t *testing.T) {
	rootManifest, err := agentrevision.NewManifest([]agentrevision.File{
		{Path: "agent.yaml", Content: []byte("name: root\nmodel: model\n")},
		{Path: "instructions.md", Content: []byte("Coordinate.\n")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	childManifest, err := agentrevision.NewManifest([]agentrevision.File{
		{Path: "agent.yaml", Content: []byte("name: new-worker\nmodel: model\n")},
		{Path: "instructions.md", Content: []byte("Do the work.\n")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := &agentsv1.AgentDefinition{
		Id: "root-id", Name: "root", Model: "model", SystemPrompt: "Coordinate.", Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
	}
	child := &agentsv1.AgentDefinition{
		Id: "child-id", Name: "new-worker", Model: "model", SystemPrompt: "Do the work.", Mode: agentsv1.AgentMode_AGENT_MODE_SUBAGENT,
	}
	handler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{},
		links: map[string][]*agentsv1.AgentLink{"root-id": {{
			Id: "link-1", TargetId: "child-id", TargetType: agentLinkTargetAgent,
			LinkType: agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE,
		}}},
		agents: map[string]*agentsv1.AgentDefinition{"child-id": child},
		revisions: map[string]*agentsv1.AgentRevision{
			"root-id":  revisionProtoForPullTest(rootManifest, "root-revision", "root-id"),
			"child-id": revisionProtoForPullTest(childManifest, "child-revision", "child-id"),
		},
	}
	target := t.TempDir()
	if _, err := pullAgentInto(context.Background(), newPullGraphFactory(t, handler, &pullGraphFunctionsHandler{}), target, root, true); err != nil {
		t.Fatal(err)
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project.Config.Subagents, []string{"./subagents/new-worker"}) || len(project.Subagents) != 1 {
		t.Fatalf("pulled added subordinate = paths %v projects %d", project.Config.Subagents, len(project.Subagents))
	}
}

func revisionProtoForPullTest(manifest *agentrevision.Manifest, revisionID, agentID string) *agentsv1.AgentRevision {
	result := &agentsv1.AgentRevision{
		Id: revisionID, AgentId: agentID, Number: 1, Digest: manifest.Digest, Format: int32(manifest.Format),
	}
	for _, file := range manifest.Files {
		result.Files = append(result.Files, &agentsv1.AgentRevisionFile{
			Path: file.Path, Content: append([]byte(nil), file.Content...), Sha256: file.SHA256,
			Mode: file.Mode, SizeBytes: file.Size,
		})
	}
	for _, function := range manifest.Functions {
		parameters, _ := structpb.NewStruct(function.Parameters)
		result.Functions = append(result.Functions, &agentsv1.AgentProjectFunction{
			Name: function.Name, Description: function.Description, Path: function.Path,
			ExportName: function.Export, Runtime: string(function.Runtime), Parameters: parameters,
		})
	}
	return result
}

func newPullGraphFactory(
	t *testing.T,
	agentsHandler agentsconnect.AgentsServiceHandler,
	functionsHandler functionsconnect.FunctionsServiceHandler,
) *client.Factory {
	t.Helper()
	mux := http.NewServeMux()
	agentsPath, agentsHTTP := agentsconnect.NewAgentsServiceHandler(agentsHandler)
	functionsPath, functionsHTTP := functionsconnect.NewFunctionsServiceHandler(functionsHandler)
	mux.Handle(agentsPath, agentsHTTP)
	mux.Handle(functionsPath, functionsHTTP)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return client.New(client.Options{APIURL: server.URL})
}

func TestPullRoundTripsSkillDescriptionToolParametersAndDisabledTrigger(t *testing.T) {
	config, err := structpb.NewStruct(map[string]any{
		"skills": []any{map[string]any{
			"name": "style", "description": "House style", "content": "# Style\n\nBe concise.",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentsv1.AgentDefinition{
		Id: "agent-1", Name: "support", Model: "model", SystemPrompt: "Help users.",
		Tools: []string{"lookup"}, Config: config, Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
		TaskPermissionMode: agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ASK,
	}
	agentsHandler := &pullGraphAgentsHandler{triggers: map[string][]*agentsv1.AgentTrigger{
		"agent-1": {{
			Id: "trigger-1", AgentId: "agent-1", Name: "daily", TriggerType: "cron",
			Enabled: false, CronExpression: "0 9 * * *", CronTimezone: "UTC",
		}},
	}}
	functionsHandler := &pullGraphFunctionsHandler{functions: map[string]*functionsv1.Function{
		"lookup": {
			Id: "function-1", Name: "lookup", Mode: functionsv1.ExecutionMode_EXECUTION_MODE_ISOLATED,
			Enabled: true, Isolated: &functionsv1.IsolatedConfig{Runtime: "deno", Code: "export default () => 'ok'"},
			Parameters: mustStruct(t, map[string]any{"type": "object", "required": []any{"query"}}),
		},
	}}
	factory := newPullGraphFactory(t, agentsHandler, functionsHandler)
	target := t.TempDir()
	if _, err := pullAgentInto(context.Background(), factory, target, agent, true); err != nil {
		t.Fatalf("pullAgentInto() error = %v", err)
	}

	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatalf("Load(pulled project) error = %v", err)
	}
	if len(project.Skills) != 1 || project.Skills[0].Description != "House style" || !strings.Contains(project.Skills[0].Content, "Be concise.") {
		t.Fatalf("pulled skills = %+v", project.Skills)
	}
	if len(project.ToolFiles) != 1 || !reflect.DeepEqual(project.ToolFiles[0].Parameters, map[string]any{"type": "object", "required": []any{"query"}}) {
		t.Fatalf("pulled tools = %+v", project.ToolFiles)
	}
	if len(project.Config.Triggers) != 1 || project.Config.Triggers[0].IsEnabled() || project.Config.Triggers[0].Timezone != "UTC" {
		t.Fatalf("pulled triggers = %+v", project.Config.Triggers)
	}
}

func TestPullMaterializesSharedFunctionOnce(t *testing.T) {
	root := &agentsv1.AgentDefinition{Id: "root", Name: "root", Model: "model", SystemPrompt: "root", Tools: []string{"shared"}, Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY}
	child := &agentsv1.AgentDefinition{Id: "child", Name: "child", Model: "model", SystemPrompt: "child", Tools: []string{"shared"}, Mode: agentsv1.AgentMode_AGENT_MODE_SUBAGENT}
	agentsHandler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{},
		links: map[string][]*agentsv1.AgentLink{"root": {{
			Id: "link-1", TargetId: "child", TargetType: agentLinkTargetAgent,
			LinkType: agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE,
		}}},
		agents: map[string]*agentsv1.AgentDefinition{"child": child},
	}
	functionsHandler := &pullGraphFunctionsHandler{functions: map[string]*functionsv1.Function{
		"shared": {Id: "function-1", Name: "shared", Mode: functionsv1.ExecutionMode_EXECUTION_MODE_ISOLATED, Enabled: true, Isolated: &functionsv1.IsolatedConfig{Runtime: "nodejs20", Code: "export default () => null"}},
	}}
	target := t.TempDir()
	if _, err := pullAgentInto(context.Background(), newPullGraphFactory(t, agentsHandler, functionsHandler), target, root, true); err != nil {
		t.Fatal(err)
	}
	project, err := agentproject.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.ToolFiles) != 1 || len(project.Subagents) != 1 || !reflect.DeepEqual(project.Subagents[0].BuiltinTools, []string{"shared"}) {
		t.Fatalf("shared function projection = root tools %+v child builtins %+v", project.ToolFiles, project.Subagents[0].BuiltinTools)
	}
}

func TestPullRejectsDanglingSubagentLinkBeforeWriting(t *testing.T) {
	root := &agentsv1.AgentDefinition{
		Id: "root", Name: "root", Model: "model", SystemPrompt: "root",
		Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
	}
	agentsHandler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{},
		links: map[string][]*agentsv1.AgentLink{"root": {{
			Id: "link-1", TargetId: "missing-child", TargetName: "missing",
			TargetType: agentLinkTargetAgent,
			LinkType:   agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE,
		}}},
		agents: map[string]*agentsv1.AgentDefinition{},
	}
	factory := newPullGraphFactory(t, agentsHandler, &pullGraphFunctionsHandler{})
	target := t.TempDir()
	original := []byte("existing instructions\n")
	if err := os.WriteFile(filepath.Join(target, "instructions.md"), original, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := pullAgentInto(context.Background(), factory, target, root, true)
	if err == nil || !strings.Contains(err.Error(), "missing-child") {
		t.Fatalf("pullAgentInto() error = %v, want dangling target ID", err)
	}
	got, readErr := os.ReadFile(filepath.Join(target, "instructions.md"))
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("target changed after graph fetch failure: content=%q error=%v", got, readErr)
	}
}

func TestPullRejectsLinkedPrimaryAgent(t *testing.T) {
	root := &agentsv1.AgentDefinition{
		Id: "root", Name: "root", Model: "model", SystemPrompt: "root",
		Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
	}
	child := &agentsv1.AgentDefinition{
		Id: "child", Name: "child", Model: "model", SystemPrompt: "child",
		Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
	}
	agentsHandler := &pullGraphAgentsHandler{
		triggers: map[string][]*agentsv1.AgentTrigger{},
		links: map[string][]*agentsv1.AgentLink{"root": {{
			Id: "link-1", TargetId: "child", TargetName: "child",
			TargetType: agentLinkTargetAgent,
			LinkType:   agentsv1.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE,
		}}},
		agents: map[string]*agentsv1.AgentDefinition{"child": child},
	}
	factory := newPullGraphFactory(t, agentsHandler, &pullGraphFunctionsHandler{})

	_, err := pullAgentInto(context.Background(), factory, t.TempDir(), root, true)
	if err == nil || !strings.Contains(err.Error(), "without subagent mode") {
		t.Fatalf("pullAgentInto() error = %v, want mode mismatch", err)
	}
}

func TestPullRejectsUnsupportedFunctionRuntimeAndName(t *testing.T) {
	for name, function := range map[string]*functionsv1.Function{
		"unsupported runtime": {Name: "lookup", Mode: functionsv1.ExecutionMode_EXECUTION_MODE_ISOLATED, Isolated: &functionsv1.IsolatedConfig{Runtime: "nodejs22", Code: "x"}},
		"unsafe local name":   {Name: "lookup-order", Mode: functionsv1.ExecutionMode_EXECUTION_MODE_ISOLATED, Isolated: &functionsv1.IsolatedConfig{Runtime: "nodejs20", Code: "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			factory := newPullGraphFactory(t, &pullGraphAgentsHandler{}, &pullGraphFunctionsHandler{functions: map[string]*functionsv1.Function{function.GetName(): function}})
			if _, _, _, err := fetchPulledTool(context.Background(), factory, function.GetName()); err == nil {
				t.Fatal("fetchPulledTool() succeeded")
			}
		})
	}
}

func TestPullRejectsStandaloneSubagentRoot(t *testing.T) {
	agent := &agentsv1.AgentDefinition{Name: "child", Mode: agentsv1.AgentMode_AGENT_MODE_SUBAGENT}
	if _, err := pullAgentInto(context.Background(), nil, t.TempDir(), agent, true); err == nil || !strings.Contains(err.Error(), "primary parent") {
		t.Fatalf("pullAgentInto() error = %v, want parent hint", err)
	}
}

func TestPullTreatsRemoteNameAsOpaqueWhenDirIsExplicit(t *testing.T) {
	agent := &agentsv1.AgentDefinition{
		Id: "agent-1", Name: "team/support", Model: "model", SystemPrompt: "help",
		Mode: agentsv1.AgentMode_AGENT_MODE_PRIMARY,
	}
	agentsHandler := &pullGraphAgentsHandler{agents: map[string]*agentsv1.AgentDefinition{"agent-1": agent}}
	functionsHandler := &pullGraphFunctionsHandler{}
	mux := http.NewServeMux()
	agentsPath, agentsHTTP := agentsconnect.NewAgentsServiceHandler(agentsHandler)
	functionsPath, functionsHTTP := functionsconnect.NewFunctionsServiceHandler(functionsHandler)
	mux.Handle(agentsPath, agentsHTTP)
	mux.Handle(functionsPath, functionsHTTP)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	target := t.TempDir()
	options := &connectionOptions{apiURL: server.URL, apiKey: "key"}
	cmd := newPullCmd(options)
	cmd.SetArgs([]string{"team/support", "--dir", target})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pull explicit directory error = %v", err)
	}
	project, err := agentproject.Load(target)
	if err != nil || project.Config.Name != "team/support" {
		t.Fatalf("pulled project = %+v error=%v", project, err)
	}
}

func TestSecureProjectPathRejectsSymlinkParent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "skills")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := secureProjectPath(root, "skills", "billing", "SKILL.md"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("secureProjectPath() error = %v, want symlink rejection", err)
	}
}

func TestReconcileStaleProjectFilesRequiresExplicitForce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for path, content := range map[string]string{
		"agent.yaml":      "name: local-agent\nmodel: model\nfiles:\n  - ./stale.ts\n",
		"instructions.md": "instructions\n",
		"stale.ts":        "export const stale = true\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bundle := &pullBundle{files: map[string]pullFile{
		"agent.yaml":      {data: []byte("name: remote-agent\nmodel: model\n"), mode: 0o644},
		"instructions.md": {data: []byte("remote instructions\n"), mode: 0o644},
	}}

	if err := reconcileStaleProjectFiles(root, bundle, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("reconcileStaleProjectFiles() error = %v, want force requirement", err)
	}
	if _, err := os.Stat(filepath.Join(root, "stale.ts")); err != nil {
		t.Fatalf("stale file changed without force: %v", err)
	}
	if err := reconcileStaleProjectFiles(root, bundle, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "stale.ts")); !os.IsNotExist(err) {
		t.Fatalf("stale file still exists after force: %v", err)
	}
}

func TestPullAgentRejectsUnsafeInlineSkillName(t *testing.T) {
	t.Parallel()

	config, err := structpb.NewStruct(map[string]any{
		"skills": []any{map[string]any{
			"name":    "../../escape",
			"content": "unsafe",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentsv1.AgentDefinition{
		Name:         "safe-agent",
		Model:        "model",
		SystemPrompt: "instructions",
		Config:       config,
	}

	_, err = pullAgentInto(context.Background(), nil, t.TempDir(), agent, false)
	if err == nil || !strings.Contains(err.Error(), "skill name") {
		t.Fatalf("pullAgentInto() error = %v, want unsafe skill name rejection", err)
	}
}

type failingTriggerListHandler struct {
	agentsconnect.UnimplementedAgentsServiceHandler
}

func newPullAgentsTestServer(t *testing.T, handler agentsconnect.AgentsServiceHandler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	path, connectHandler := agentsconnect.NewAgentsServiceHandler(handler)
	mux.Handle(path, connectHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func (failingTriggerListHandler) ListAgentTriggers(
	context.Context,
	*connect.Request[agentsv1.ListAgentTriggersRequest],
) (*connect.Response[agentsv1.ListAgentTriggersResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, nil)
}

func TestPullAgentReturnsTriggerListError(t *testing.T) {
	t.Parallel()

	server := newPullAgentsTestServer(t, failingTriggerListHandler{})
	factory := client.New(client.Options{APIURL: server.URL, APIKey: "test-key"})
	agent := &agentsv1.AgentDefinition{
		Id:           "agent-1",
		Name:         "safe-agent",
		Model:        "model",
		SystemPrompt: "instructions",
		Config:       &structpb.Struct{},
	}

	target := t.TempDir()
	original := []byte("existing instructions\n")
	if err := os.WriteFile(filepath.Join(target, "instructions.md"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := pullAgentInto(context.Background(), factory, target, agent, false)
	if err == nil || !strings.Contains(err.Error(), "server unavailable") {
		t.Fatalf("pullAgentInto() error = %v, want trigger list error", err)
	}
	got, readErr := os.ReadFile(filepath.Join(target, "instructions.md"))
	if readErr != nil || string(got) != string(original) {
		t.Fatalf("target changed after fetch failure: content=%q error=%v", got, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(target, "agent.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("agent.yaml exists after fetch failure: %v", statErr)
	}
}
