package agents

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestCheckDriftFailsClosedForMalformedDeploymentStamp(t *testing.T) {
	cases := map[string]map[string]any{
		"missing hash": {
			"source": "evs deploy", "format": deploymentStampFormat,
			"deployed_at": "2026-08-04T12:00:00Z", "managed_triggers": []any{},
		},
		"wrong source": {
			"source": "dashboard", "hash": "abc123", "format": deploymentStampFormat,
			"deployed_at": "2026-08-04T12:00:00Z", "managed_triggers": []any{},
		},
		"invalid format": {
			"source": "evs deploy", "hash": "abc123", "format": 99,
			"deployed_at": "2026-08-04T12:00:00Z", "managed_triggers": []any{},
		},
		"invalid deployment time": {
			"source": "evs deploy", "hash": "abc123", "format": deploymentStampFormat,
			"deployed_at": "not-a-time", "managed_triggers": []any{},
		},
		"missing managed trigger manifest": {
			"source": "evs deploy", "hash": "abc123", "format": deploymentStampFormat,
			"deployed_at": "2026-08-04T12:00:00Z",
		},
	}

	for name, meta := range cases {
		t.Run(name, func(t *testing.T) {
			config, err := structpb.NewStruct(map[string]any{projectMetaKey: meta})
			if err != nil {
				t.Fatal(err)
			}
			agent := &agentsv1.AgentDefinition{
				Name:   "support",
				Config: config,
			}
			if err := checkDrift(agent, "support"); err == nil || !strings.Contains(err.Error(), "invalid deployment stamp") {
				t.Fatalf("checkDrift() error = %v, want invalid deployment stamp", err)
			}
		})
	}
}

func TestCheckDriftAcceptsUnmodifiedManagedAgent(t *testing.T) {
	config, err := structpb.NewStruct(map[string]any{projectMetaKey: map[string]any{
		"source": "evs deploy", "hash": "abc123", "format": deploymentStampFormat,
		"deployed_at": "2026-08-04T12:00:00Z", "managed_triggers": []any{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentsv1.AgentDefinition{
		Name:   "support",
		Config: config,
	}
	if err := checkDrift(agent, "support"); err != nil {
		t.Fatalf("checkDrift() error = %v", err)
	}
}

func TestDeploymentStampRecordsManagedTriggerIDs(t *testing.T) {
	project := &agentproject.Project{Config: agentproject.Config{
		Name: "support", Model: "model",
		Triggers: []agentproject.Trigger{{Name: "daily", Type: "cron"}},
	}}
	config, err := desiredAgentConfig(project, true, map[string]string{"trigger-1": "daily"})
	if err != nil {
		t.Fatal(err)
	}
	agent := &agentsv1.AgentDefinition{Name: "support", Config: config}
	if err := checkDrift(agent, "support"); err != nil {
		t.Fatalf("checkDrift() error = %v", err)
	}
	if got := deploymentStampManagedTriggers(agent); !reflect.DeepEqual(got, map[string]string{"trigger-1": "daily"}) {
		t.Fatalf("managed trigger manifest = %v", got)
	}
}

func TestDesiredAgentConfigRejectsInvalidProjectNetworkMode(t *testing.T) {
	t.Parallel()

	project := &agentproject.Project{
		Config: agentproject.Config{
			Name:  "support",
			Model: "model",
			Config: map[string]any{
				"sandbox": map[string]any{"network_mode": "denyy"},
			},
		},
		Instructions: "help",
		ToolFiles:    []agentproject.ToolFile{toolFixture()},
	}
	if err := project.EnsureRevisionManifest(); err != nil {
		t.Fatal(err)
	}

	_, err := desiredAgentConfig(project, false)
	if err == nil || !strings.Contains(err.Error(), "network_mode must be deny, whitelist or allow") {
		t.Fatalf("desiredAgentConfig() error = %v, want invalid network mode error", err)
	}
}

func TestAgentStateMatchDoesNotTrustStampAlone(t *testing.T) {
	project := &agentproject.Project{
		Config: agentproject.Config{
			Name:        "support",
			Description: "support agent",
			Model:       "wanted-model",
			Permissions: agentproject.Permissions{TaskMode: "ask"},
		},
		Instructions: "help",
	}
	config, err := desiredAgentConfig(project, true)
	if err != nil {
		t.Fatal(err)
	}
	existing := &agentsv1.AgentDefinition{
		Name:               project.Config.Name,
		Description:        project.Config.Description,
		Model:              "dashboard-model",
		SystemPrompt:       project.Instructions,
		Config:             config,
		Mode:               agentsv1.AgentMode_AGENT_MODE_PRIMARY,
		TaskPermissionMode: agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ASK,
	}

	if agentMatchesProject(existing, project, agentsv1.AgentMode_AGENT_MODE_PRIMARY, true) {
		t.Fatal("agentMatchesProject() trusted a valid stamp despite dashboard state drift")
	}
}

func toolFixture() agentproject.ToolFile {
	return agentproject.ToolFile{
		Name:       "lookup",
		Runtime:    "deno",
		Code:       "export default async () => 'ok'",
		Parameters: map[string]any{"type": "object"},
	}
}

func mustStruct(t *testing.T, value map[string]any) *structpb.Struct {
	t.Helper()
	got, err := structpb.NewStruct(value)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBuildUpdateAgentRequestUsesExplicitClearSemantics(t *testing.T) {
	project := &agentproject.Project{
		Config: agentproject.Config{
			Name:        "support",
			Model:       "model",
			Permissions: agentproject.Permissions{TaskMode: "ask"},
		},
		Instructions: "help",
	}
	config := mustStruct(t, map[string]any{})

	req := buildUpdateAgentRequest("agent-1", project, agentsv1.AgentMode_AGENT_MODE_PRIMARY, config)
	if req.Description == nil || req.GetDescription() != "" {
		t.Fatalf("description presence = %v value %q, want explicit empty", req.Description != nil, req.GetDescription())
	}
	if req.MaxTurns == nil || req.GetMaxTurns() != 0 || req.MaxToolCallsPerTurn == nil || req.GetMaxToolCallsPerTurn() != 0 {
		t.Fatalf("limits = max_turns:%v max_tool_calls:%v, want explicit zeroes", req.MaxTurns, req.MaxToolCallsPerTurn)
	}
	if req.MaxSteps == nil || req.GetMaxSteps() != 0 {
		t.Fatalf("max_steps = %v, want explicit zero", req.MaxSteps)
	}
	if !req.GetClearTools() {
		t.Fatal("clear_tools = false, want true for an empty desired tool set")
	}
	if req.TaskPermissionMode == nil || req.GetTaskPermissionMode() != agentsv1.TaskPermissionMode_TASK_PERMISSION_MODE_ASK {
		t.Fatalf("task permission = %v, want explicit ask", req.TaskPermissionMode)
	}
}

type deployAgentsHandler struct {
	agentsconnect.UnimplementedAgentsServiceHandler
	t               *testing.T
	created         *agentsv1.CreateAgentRequest
	updated         []*agentsv1.UpdateAgentRequest
	agent           *agentsv1.AgentDefinition
	triggers        []*agentsv1.AgentTrigger
	deletedTriggers []string
	createdTriggers []*agentsv1.CreateAgentTriggerRequest
	updatedTriggers []*agentsv1.UpdateAgentTriggerRequest
	getAgentMisses  int
	revision        *agentsv1.AgentRevision
	revisionRequest *agentsv1.CreateAgentRevisionRequest
}

func (h *deployAgentsHandler) CreateAgentRevision(
	_ context.Context,
	req *connect.Request[agentsv1.CreateAgentRevisionRequest],
) (*connect.Response[agentsv1.CreateAgentRevisionResponse], error) {
	h.checkAuth(req.Header())
	h.revisionRequest = req.Msg
	rev, err := testRevisionFromRequest(req.Msg.GetAgentId(), req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	created := h.revision == nil || h.revision.GetDigest() != rev.GetDigest()
	if created {
		h.revision = rev
	}
	return connect.NewResponse(&agentsv1.CreateAgentRevisionResponse{Revision: h.revision, Created: created}), nil
}

func (h *deployAgentsHandler) GetActiveAgentRevision(
	_ context.Context,
	req *connect.Request[agentsv1.GetActiveAgentRevisionRequest],
) (*connect.Response[agentsv1.GetActiveAgentRevisionResponse], error) {
	h.checkAuth(req.Header())
	if h.revision == nil || h.revision.GetAgentId() != req.Msg.GetAgentId() {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("revision not found"))
	}
	return connect.NewResponse(&agentsv1.GetActiveAgentRevisionResponse{Revision: h.revision}), nil
}

func (h *deployAgentsHandler) DeleteAgentTrigger(
	_ context.Context,
	req *connect.Request[agentsv1.DeleteAgentTriggerRequest],
) (*connect.Response[agentsv1.DeleteAgentTriggerResponse], error) {
	h.checkAuth(req.Header())
	h.deletedTriggers = append(h.deletedTriggers, req.Msg.GetId())
	return connect.NewResponse(&agentsv1.DeleteAgentTriggerResponse{}), nil
}

func (h *deployAgentsHandler) CreateAgentTrigger(
	_ context.Context,
	req *connect.Request[agentsv1.CreateAgentTriggerRequest],
) (*connect.Response[agentsv1.CreateAgentTriggerResponse], error) {
	h.checkAuth(req.Header())
	h.createdTriggers = append(h.createdTriggers, req.Msg)
	return connect.NewResponse(&agentsv1.CreateAgentTriggerResponse{Trigger: &agentsv1.AgentTrigger{Id: "replacement"}}), nil
}

func (h *deployAgentsHandler) UpdateAgentTrigger(
	_ context.Context,
	req *connect.Request[agentsv1.UpdateAgentTriggerRequest],
) (*connect.Response[agentsv1.UpdateAgentTriggerResponse], error) {
	h.checkAuth(req.Header())
	h.updatedTriggers = append(h.updatedTriggers, req.Msg)
	return connect.NewResponse(&agentsv1.UpdateAgentTriggerResponse{}), nil
}

func (h *deployAgentsHandler) ListAgentTriggers(
	_ context.Context,
	req *connect.Request[agentsv1.ListAgentTriggersRequest],
) (*connect.Response[agentsv1.ListAgentTriggersResponse], error) {
	h.checkAuth(req.Header())
	return connect.NewResponse(&agentsv1.ListAgentTriggersResponse{Triggers: h.triggers}), nil
}

func (h *deployAgentsHandler) ListAgents(
	_ context.Context,
	req *connect.Request[agentsv1.ListAgentsRequest],
) (*connect.Response[agentsv1.ListAgentsResponse], error) {
	h.checkAuth(req.Header())
	return connect.NewResponse(&agentsv1.ListAgentsResponse{}), nil
}

func (h *deployAgentsHandler) CreateAgent(
	_ context.Context,
	req *connect.Request[agentsv1.CreateAgentRequest],
) (*connect.Response[agentsv1.CreateAgentResponse], error) {
	h.checkAuth(req.Header())
	h.created = req.Msg
	h.agent = agentFromCreateRequest("agent-1", req.Msg)
	return connect.NewResponse(&agentsv1.CreateAgentResponse{Agent: &agentsv1.AgentDefinition{
		Id: h.agent.GetId(), Name: h.agent.GetName(), Model: h.agent.GetModel(),
	}}), nil
}

func (h *deployAgentsHandler) GetAgent(
	_ context.Context,
	req *connect.Request[agentsv1.GetAgentRequest],
) (*connect.Response[agentsv1.GetAgentResponse], error) {
	h.checkAuth(req.Header())
	if h.getAgentMisses > 0 {
		h.getAgentMisses--
		return nil, connect.NewError(connect.CodeNotFound, errors.New("projection pending"))
	}
	if h.agent == nil || h.agent.GetId() != req.Msg.GetId() {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	}
	return connect.NewResponse(&agentsv1.GetAgentResponse{Agent: h.agent}), nil
}

func (h *deployAgentsHandler) UpdateAgent(
	_ context.Context,
	req *connect.Request[agentsv1.UpdateAgentRequest],
) (*connect.Response[agentsv1.UpdateAgentResponse], error) {
	h.checkAuth(req.Header())
	h.updated = append(h.updated, req.Msg)
	if req.Msg.Description != nil {
		h.agent.Description = req.Msg.GetDescription()
	}
	if req.Msg.Model != nil {
		h.agent.Model = req.Msg.GetModel()
	}
	if req.Msg.SystemPrompt != nil {
		h.agent.SystemPrompt = req.Msg.GetSystemPrompt()
	}
	if req.Msg.GetClearTools() {
		h.agent.Tools = nil
	} else if req.Msg.Tools != nil {
		h.agent.Tools = req.Msg.GetTools()
	}
	if req.Msg.Config != nil {
		h.agent.Config = req.Msg.GetConfig()
	}
	if req.Msg.MaxTurns != nil {
		h.agent.MaxTurns = req.Msg.GetMaxTurns()
	}
	if req.Msg.MaxToolCallsPerTurn != nil {
		h.agent.MaxToolCallsPerTurn = req.Msg.GetMaxToolCallsPerTurn()
	}
	if req.Msg.Mode != nil {
		h.agent.Mode = req.Msg.GetMode()
	}
	if req.Msg.MaxSteps != nil {
		h.agent.MaxSteps = req.Msg.MaxSteps
	}
	if req.Msg.TaskPermissionMode != nil {
		h.agent.TaskPermissionMode = req.Msg.GetTaskPermissionMode()
	}
	return connect.NewResponse(&agentsv1.UpdateAgentResponse{Agent: &agentsv1.AgentDefinition{Id: h.agent.GetId()}}), nil
}

func (h *deployAgentsHandler) ListAgentLinks(
	_ context.Context,
	req *connect.Request[agentsv1.ListAgentLinksRequest],
) (*connect.Response[agentsv1.ListAgentLinksResponse], error) {
	h.checkAuth(req.Header())
	return connect.NewResponse(&agentsv1.ListAgentLinksResponse{}), nil
}

func (h *deployAgentsHandler) checkAuth(header http.Header) {
	h.t.Helper()
	if got := header.Get("x-evs-api-key"); got != "deploy-api-key" {
		h.t.Errorf("x-evs-api-key = %q, want deploy-api-key", got)
	}
}

type deployFunctionsHandler struct {
	functionsconnect.UnimplementedFunctionsServiceHandler
	t         *testing.T
	created   *functionsv1.CreateFunctionRequest
	getMisses int
}

func (h *deployFunctionsHandler) GetFunctionByName(
	_ context.Context,
	req *connect.Request[functionsv1.GetFunctionByNameRequest],
) (*connect.Response[functionsv1.GetFunctionByNameResponse], error) {
	h.checkAuth(req.Header())
	if h.getMisses > 0 {
		h.getMisses--
		return nil, connect.NewError(connect.CodeNotFound, errors.New("projection pending"))
	}
	if h.created != nil && h.created.GetName() == req.Msg.GetName() {
		return connect.NewResponse(&functionsv1.GetFunctionByNameResponse{Function: &functionsv1.Function{
			Id:          "function-1",
			Name:        h.created.GetName(),
			Description: h.created.GetDescription(),
			Mode:        h.created.GetMode(),
			Isolated:    h.created.GetIsolated(),
			Parameters:  h.created.GetParameters(),
			Enabled:     true,
		}}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("function not found"))
}

func (h *deployFunctionsHandler) CreateFunction(
	_ context.Context,
	req *connect.Request[functionsv1.CreateFunctionRequest],
) (*connect.Response[functionsv1.CreateFunctionResponse], error) {
	h.checkAuth(req.Header())
	h.created = req.Msg
	return connect.NewResponse(&functionsv1.CreateFunctionResponse{Function: &functionsv1.Function{
		Id:          "function-1",
		Name:        req.Msg.GetName(),
		Description: req.Msg.GetDescription(),
		Mode:        req.Msg.GetMode(),
		Isolated:    req.Msg.GetIsolated(),
		Parameters:  req.Msg.GetParameters(),
		Enabled:     true,
	}}), nil
}

func (h *deployFunctionsHandler) checkAuth(header http.Header) {
	h.t.Helper()
	if got := header.Get("x-evs-api-key"); got != "deploy-api-key" {
		h.t.Errorf("x-evs-api-key = %q, want deploy-api-key", got)
	}
}

func TestDeployGraphUsesGeneratedClientsAndStampsManagedState(t *testing.T) {
	agentsHandler := &deployAgentsHandler{t: t, getAgentMisses: 2}
	functionsHandler := &deployFunctionsHandler{t: t, getMisses: 2}
	mux := http.NewServeMux()
	agentsPath, agentsHTTPHandler := agentsconnect.NewAgentsServiceHandler(agentsHandler)
	functionsPath, functionsHTTPHandler := functionsconnect.NewFunctionsServiceHandler(functionsHandler)
	mux.Handle(agentsPath, agentsHTTPHandler)
	mux.Handle(functionsPath, functionsHTTPHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	factory := client.New(client.Options{APIURL: server.URL, APIKey: "deploy-api-key"})
	project := &agentproject.Project{
		Config: agentproject.Config{
			Name:        "support",
			Description: "Customer support agent",
			Model:       "model-1",
			Config:      map[string]any{"temperature": 0.2},
		},
		Instructions: "Help the customer.",
		BuiltinTools: []string{"web_search"},
		ToolFiles:    []agentproject.ToolFile{toolFixture()},
		Skills: []agentproject.Skill{{
			Name:        "refunds",
			Description: "Refund policy",
			Content:     "# Refunds",
		}},
	}

	var out bytes.Buffer
	err := deployGraph(
		context.Background(),
		factory,
		&out,
		project,
		false,
	)
	if err != nil {
		t.Fatalf("deployGraph() error = %v", err)
	}

	if functionsHandler.created != nil {
		t.Fatal("project function was written to the tenant-global Functions service")
	}
	if agentsHandler.revisionRequest == nil || agentsHandler.revision == nil {
		t.Fatal("CreateAgentRevision was not called")
	}
	if len(agentsHandler.revisionRequest.GetFunctions()) != 1 || agentsHandler.revisionRequest.GetFunctions()[0].GetName() != "lookup" {
		t.Fatalf("revision functions = %+v", agentsHandler.revisionRequest.GetFunctions())
	}
	if agentsHandler.created == nil {
		t.Fatal("CreateAgent was not called")
	}
	if got := agentsHandler.created.GetTools(); len(got) != 2 || got[0] != "web_search" || got[1] != "lookup" {
		t.Errorf("agent tools = %v", got)
	}
	if agentsHandler.created.GetConfig().GetFields()[projectMetaKey] != nil {
		t.Fatal("CreateAgent carried a success stamp before graph convergence")
	}
	if len(agentsHandler.updated) == 0 {
		t.Fatal("final UpdateAgent deployment stamp was not written")
	}
	meta := agentsHandler.updated[len(agentsHandler.updated)-1].GetConfig().GetFields()[projectMetaKey].GetStructValue()
	if meta.GetFields()["hash"].GetStringValue() != project.Hash() || meta.GetFields()["source"].GetStringValue() != "evs deploy" {
		t.Errorf("deployment stamp = %v", meta)
	}
	if !strings.Contains(out.String(), "created") {
		t.Errorf("deploy output = %q", out.String())
	}
}

func TestSyncTriggersReportsRemoteOnlyTriggersWhenProjectDeclaresNone(t *testing.T) {
	handler := &deployAgentsHandler{t: t, triggers: []*agentsv1.AgentTrigger{{
		Id:   "trigger-1",
		Name: "dashboard-trigger",
	}}}
	mux := http.NewServeMux()
	path, httpHandler := agentsconnect.NewAgentsServiceHandler(handler)
	mux.Handle(path, httpHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	factory := client.New(client.Options{APIURL: server.URL, APIKey: "deploy-api-key"})
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	if err := syncTriggers(context.Background(), factory, cmd, "agent-1", &agentproject.Project{}, map[string]string{}, false); err != nil {
		t.Fatalf("syncTriggers() error = %v", err)
	}
	if !strings.Contains(out.String(), "dashboard-trigger") || !strings.Contains(out.String(), "left untouched") {
		t.Fatalf("syncTriggers() output = %q", out.String())
	}
}

func TestSyncTriggersRefusesPlatformOnlyNameCollision(t *testing.T) {
	handler := &deployAgentsHandler{t: t, triggers: []*agentsv1.AgentTrigger{{
		Id: "dashboard-1", Name: "incoming", TriggerType: "webhook", Enabled: true,
	}}}
	factory := newDeployAgentsTestFactory(t, handler)
	project := &agentproject.Project{Config: agentproject.Config{Triggers: []agentproject.Trigger{{
		Name: "incoming", Type: "webhook",
	}}}}
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := syncTriggers(context.Background(), factory, cmd, "agent-1", project, map[string]string{}, true)
	if err == nil || !strings.Contains(err.Error(), "platform-only trigger") {
		t.Fatalf("syncTriggers() error = %v, want platform-only collision", err)
	}
	if len(handler.createdTriggers) != 0 || len(handler.updatedTriggers) != 0 || len(handler.deletedTriggers) != 0 {
		t.Fatalf(
			"platform-only trigger was mutated: creates=%d updates=%d deletes=%v",
			len(handler.createdTriggers), len(handler.updatedTriggers), handler.deletedTriggers,
		)
	}
}

func TestDeclaredTriggersMatchRequiresStableManagedID(t *testing.T) {
	project := &agentproject.Project{Config: agentproject.Config{Triggers: []agentproject.Trigger{{
		Name: "incoming", Type: "webhook",
	}}}}
	matching := &agentsv1.AgentTrigger{
		Id: "dashboard-1", Name: "incoming", TriggerType: "webhook", Enabled: true,
	}
	remote := map[string][]*agentsv1.AgentTrigger{"incoming": {matching}}

	if declaredTriggersMatch(project, remote, map[string]string{"managed-1": "incoming"}) {
		t.Fatal("same-name platform-only trigger satisfied a stale managed trigger ID")
	}
	matching.Id = "managed-1"
	if !declaredTriggersMatch(project, remote, map[string]string{"managed-1": "incoming"}) {
		t.Fatal("exact trigger with the managed stable ID did not match")
	}
	if declaredTriggersMatch(&agentproject.Project{}, map[string][]*agentsv1.AgentTrigger{}, map[string]string{"managed-1": "old"}) {
		t.Fatal("stale managed trigger stamp was treated as converged")
	}
}

func TestAdoptLegacyTriggersRequiresUniqueExactMatch(t *testing.T) {
	project := &agentproject.Project{Config: agentproject.Config{Triggers: []agentproject.Trigger{
		{Name: "daily", Type: "cron", Schedule: "0 9 * * *", Timezone: "UTC"},
		{Name: "incoming", Type: "webhook", Timezone: "UTC"},
	}}}
	remote := map[string][]*agentsv1.AgentTrigger{
		"daily": {{Id: "cron-1", Name: "daily", TriggerType: "cron", CronExpression: "0 9 * * *", CronTimezone: "UTC", Enabled: true}},
		"incoming": {
			{Id: "webhook-1", Name: "incoming", TriggerType: "webhook", CronTimezone: "UTC", Enabled: true},
			{Id: "webhook-2", Name: "incoming", TriggerType: "webhook", CronTimezone: "UTC", Enabled: true},
		},
	}

	got := adoptLegacyTriggers(project, remote)
	want := map[string]string{"cron-1": "daily"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("adoptLegacyTriggers() = %v, want %v", got, want)
	}
}

func TestSyncTriggersRecreatesTriggerWhenTypeChanges(t *testing.T) {
	handler := &deployAgentsHandler{t: t, triggers: []*agentsv1.AgentTrigger{{
		Id:          "trigger-1",
		Name:        "incoming",
		TriggerType: "cron",
	}}}
	mux := http.NewServeMux()
	path, httpHandler := agentsconnect.NewAgentsServiceHandler(handler)
	mux.Handle(path, httpHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	factory := client.New(client.Options{APIURL: server.URL, APIKey: "deploy-api-key"})
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	project := &agentproject.Project{Config: agentproject.Config{
		Name: "support",
		Triggers: []agentproject.Trigger{{
			Name: "incoming",
			Type: "webhook",
		}},
	}}
	if err := syncTriggers(context.Background(), factory, cmd, "agent-1", project, map[string]string{"trigger-1": "incoming"}, true); err != nil {
		t.Fatalf("syncTriggers() error = %v", err)
	}
	if len(handler.deletedTriggers) != 1 || handler.deletedTriggers[0] != "trigger-1" {
		t.Fatalf("deleted triggers = %v, want trigger-1", handler.deletedTriggers)
	}
	if len(handler.createdTriggers) != 1 || handler.createdTriggers[0].GetTriggerType() != "webhook" {
		t.Fatalf("created triggers = %+v, want replacement webhook", handler.createdTriggers)
	}
	if len(handler.updatedTriggers) != 0 {
		t.Fatalf("updated triggers = %+v, want recreate", handler.updatedTriggers)
	}
}

func TestSyncTriggersReclaimsRenamedManagedTriggerByID(t *testing.T) {
	handler := &deployAgentsHandler{t: t, triggers: []*agentsv1.AgentTrigger{{
		Id: "trigger-1", Name: "renamed-in-dashboard", TriggerType: "webhook", Enabled: true, CronTimezone: "UTC",
	}}}
	factory := newDeployAgentsTestFactory(t, handler)
	project := &agentproject.Project{Config: agentproject.Config{Triggers: []agentproject.Trigger{{
		Name: "incoming", Type: "webhook", Timezone: "UTC",
	}}}}
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := syncTriggers(context.Background(), factory, cmd, "agent-1", project, map[string]string{"trigger-1": "incoming"}, true); err != nil {
		t.Fatal(err)
	}
	if len(handler.updatedTriggers) != 1 || handler.updatedTriggers[0].GetId() != "trigger-1" || handler.updatedTriggers[0].GetName() != "incoming" {
		t.Fatalf("updated triggers = %+v, want renamed trigger-1 reclaimed", handler.updatedTriggers)
	}
	if len(handler.createdTriggers) != 0 || len(handler.deletedTriggers) != 0 {
		t.Fatalf("renamed managed trigger was duplicated or recreated: creates=%d deletes=%v", len(handler.createdTriggers), handler.deletedTriggers)
	}
}

func TestSyncTriggersRemovesStaleManagedTriggerButPreservesPlatformOnly(t *testing.T) {
	handler := &deployAgentsHandler{t: t, triggers: []*agentsv1.AgentTrigger{
		{Id: "managed-1", Name: "old", TriggerType: "webhook"},
		{Id: "dashboard-1", Name: "dashboard", TriggerType: "webhook"},
	}}
	factory := newDeployAgentsTestFactory(t, handler)
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := syncTriggers(context.Background(), factory, cmd, "agent-1", &agentproject.Project{}, map[string]string{"managed-1": "old"}, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(handler.deletedTriggers, []string{"managed-1"}) {
		t.Fatalf("deleted triggers = %v, want only managed-1", handler.deletedTriggers)
	}
}

func TestSyncTriggersRecreatesWhenClearingInput(t *testing.T) {
	handler := &deployAgentsHandler{t: t, triggers: []*agentsv1.AgentTrigger{{
		Id: "trigger-1", Name: "incoming", TriggerType: "webhook", Enabled: true,
		CronTimezone: "UTC", InputTemplate: "old input",
	}}}
	factory := newDeployAgentsTestFactory(t, handler)
	project := &agentproject.Project{Config: agentproject.Config{Triggers: []agentproject.Trigger{{
		Name: "incoming", Type: "webhook", Timezone: "UTC",
	}}}}
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := syncTriggers(context.Background(), factory, cmd, "agent-1", project, map[string]string{"trigger-1": "incoming"}, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(handler.deletedTriggers, []string{"trigger-1"}) || len(handler.createdTriggers) != 1 {
		t.Fatalf("clear transition did not recreate: deletes=%v creates=%+v", handler.deletedTriggers, handler.createdTriggers)
	}
}

func TestSyncTriggersDisablesNewTriggerAndSetsEventSource(t *testing.T) {
	disabled := false
	handler := &deployAgentsHandler{t: t}
	factory := newDeployAgentsTestFactory(t, handler)
	project := &agentproject.Project{Config: agentproject.Config{Triggers: []agentproject.Trigger{{
		Name: "agent-event", Type: "event", Event: "completed", SourceAgentID: "source-agent-id",
		Timezone: "UTC", Enabled: &disabled,
	}}}}
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := syncTriggers(context.Background(), factory, cmd, "agent-1", project, map[string]string{}, false); err != nil {
		t.Fatal(err)
	}
	if len(handler.createdTriggers) != 1 || handler.createdTriggers[0].GetEventSourceAgentId() != "source-agent-id" {
		t.Fatalf("create trigger requests = %+v", handler.createdTriggers)
	}
	if len(handler.updatedTriggers) != 1 || handler.updatedTriggers[0].GetEnabled() {
		t.Fatalf("disabled trigger update = %+v", handler.updatedTriggers)
	}
}

func newDeployAgentsTestFactory(t *testing.T, handler agentsconnect.AgentsServiceHandler) *client.Factory {
	t.Helper()
	mux := http.NewServeMux()
	path, httpHandler := agentsconnect.NewAgentsServiceHandler(handler)
	mux.Handle(path, httpHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return client.New(client.Options{APIURL: server.URL, APIKey: "deploy-api-key"})
}

type paginatedAgentsHandler struct {
	agentsconnect.UnimplementedAgentsServiceHandler
	t *testing.T
}

func (h paginatedAgentsHandler) ListAgents(
	_ context.Context,
	req *connect.Request[agentsv1.ListAgentsRequest],
) (*connect.Response[agentsv1.ListAgentsResponse], error) {
	h.t.Helper()
	if !req.Msg.GetIncludeHidden() || req.Msg.GetLimit() != 500 {
		h.t.Fatalf("ListAgents request = %+v", req.Msg)
	}
	if req.Msg.GetOffset() == 0 {
		page := make([]*agentsv1.AgentDefinition, 500)
		for i := range page {
			page[i] = &agentsv1.AgentDefinition{Name: "other"}
		}
		return connect.NewResponse(&agentsv1.ListAgentsResponse{Agents: page}), nil
	}
	if req.Msg.GetOffset() == 500 {
		return connect.NewResponse(&agentsv1.ListAgentsResponse{Agents: []*agentsv1.AgentDefinition{{
			Id:   "target-id",
			Name: "target",
		}}}), nil
	}
	h.t.Fatalf("unexpected offset %d", req.Msg.GetOffset())
	return nil, nil
}

func TestFindAgentByNamePaginates(t *testing.T) {
	mux := http.NewServeMux()
	path, handler := agentsconnect.NewAgentsServiceHandler(paginatedAgentsHandler{t: t})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	agent, err := findAgentByName(context.Background(), client.New(client.Options{APIURL: server.URL}), "target")
	if err != nil {
		t.Fatalf("findAgentByName() error = %v", err)
	}
	if agent.GetId() != "target-id" {
		t.Fatalf("findAgentByName() = %+v, want target-id", agent)
	}
}

type graphAgentsHandler struct {
	agentsconnect.UnimplementedAgentsServiceHandler
	agents            []*agentsv1.AgentDefinition
	createRequests    []*agentsv1.CreateAgentRequest
	updateRequests    []*agentsv1.UpdateAgentRequest
	failTriggerCreate bool
	getAgentMisses    int
	revisions         map[string]*agentsv1.AgentRevision
	revisionRequests  []*agentsv1.CreateAgentRevisionRequest
}

func (h *graphAgentsHandler) CreateAgentRevision(
	_ context.Context,
	req *connect.Request[agentsv1.CreateAgentRevisionRequest],
) (*connect.Response[agentsv1.CreateAgentRevisionResponse], error) {
	rev, err := testRevisionFromRequest(req.Msg.GetAgentId(), req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if h.revisions == nil {
		h.revisions = map[string]*agentsv1.AgentRevision{}
	}
	h.revisionRequests = append(h.revisionRequests, req.Msg)
	existing := h.revisions[req.Msg.GetAgentId()]
	created := existing == nil || existing.GetDigest() != rev.GetDigest()
	if created {
		h.revisions[req.Msg.GetAgentId()] = rev
	} else {
		rev = existing
	}
	return connect.NewResponse(&agentsv1.CreateAgentRevisionResponse{Revision: rev, Created: created}), nil
}

func (h *graphAgentsHandler) GetActiveAgentRevision(
	_ context.Context,
	req *connect.Request[agentsv1.GetActiveAgentRevisionRequest],
) (*connect.Response[agentsv1.GetActiveAgentRevisionResponse], error) {
	rev := h.revisions[req.Msg.GetAgentId()]
	if rev == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("revision not found"))
	}
	return connect.NewResponse(&agentsv1.GetActiveAgentRevisionResponse{Revision: rev}), nil
}

func (h *graphAgentsHandler) ListAgents(
	_ context.Context,
	_ *connect.Request[agentsv1.ListAgentsRequest],
) (*connect.Response[agentsv1.ListAgentsResponse], error) {
	return connect.NewResponse(&agentsv1.ListAgentsResponse{Agents: h.agents}), nil
}

func (h *graphAgentsHandler) CreateAgent(
	_ context.Context,
	req *connect.Request[agentsv1.CreateAgentRequest],
) (*connect.Response[agentsv1.CreateAgentResponse], error) {
	h.createRequests = append(h.createRequests, req.Msg)
	agent := agentFromCreateRequest("created-"+req.Msg.GetName(), req.Msg)
	h.agents = append(h.agents, agent)
	return connect.NewResponse(&agentsv1.CreateAgentResponse{Agent: &agentsv1.AgentDefinition{
		Id: agent.GetId(), Name: agent.GetName(), Model: agent.GetModel(),
	}}), nil
}

func (h *graphAgentsHandler) GetAgent(
	_ context.Context,
	req *connect.Request[agentsv1.GetAgentRequest],
) (*connect.Response[agentsv1.GetAgentResponse], error) {
	if h.getAgentMisses > 0 {
		h.getAgentMisses--
		return nil, connect.NewError(connect.CodeNotFound, errors.New("projection pending"))
	}
	for _, agent := range h.agents {
		if agent.GetId() == req.Msg.GetId() {
			return connect.NewResponse(&agentsv1.GetAgentResponse{Agent: agent}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
}

func (h *graphAgentsHandler) UpdateAgent(
	_ context.Context,
	req *connect.Request[agentsv1.UpdateAgentRequest],
) (*connect.Response[agentsv1.UpdateAgentResponse], error) {
	h.updateRequests = append(h.updateRequests, req.Msg)
	for _, agent := range h.agents {
		if agent.GetId() != req.Msg.GetId() {
			continue
		}
		if req.Msg.Description != nil {
			agent.Description = req.Msg.GetDescription()
		}
		if req.Msg.Model != nil {
			agent.Model = req.Msg.GetModel()
		}
		if req.Msg.SystemPrompt != nil {
			agent.SystemPrompt = req.Msg.GetSystemPrompt()
		}
		if req.Msg.GetClearTools() {
			agent.Tools = nil
		} else if req.Msg.Tools != nil {
			agent.Tools = req.Msg.GetTools()
		}
		if req.Msg.Config != nil {
			agent.Config = req.Msg.GetConfig()
		}
		if req.Msg.MaxTurns != nil {
			agent.MaxTurns = req.Msg.GetMaxTurns()
		}
		if req.Msg.MaxToolCallsPerTurn != nil {
			agent.MaxToolCallsPerTurn = req.Msg.GetMaxToolCallsPerTurn()
		}
		if req.Msg.Mode != nil {
			agent.Mode = req.Msg.GetMode()
		}
		if req.Msg.MaxSteps != nil {
			agent.MaxSteps = req.Msg.MaxSteps
		}
		if req.Msg.TaskPermissionMode != nil {
			agent.TaskPermissionMode = req.Msg.GetTaskPermissionMode()
		}
		return connect.NewResponse(&agentsv1.UpdateAgentResponse{Agent: &agentsv1.AgentDefinition{Id: agent.GetId()}}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
}

func (h *graphAgentsHandler) ListAgentTriggers(
	_ context.Context,
	_ *connect.Request[agentsv1.ListAgentTriggersRequest],
) (*connect.Response[agentsv1.ListAgentTriggersResponse], error) {
	return connect.NewResponse(&agentsv1.ListAgentTriggersResponse{}), nil
}

func (h *graphAgentsHandler) CreateAgentTrigger(
	_ context.Context,
	_ *connect.Request[agentsv1.CreateAgentTriggerRequest],
) (*connect.Response[agentsv1.CreateAgentTriggerResponse], error) {
	if h.failTriggerCreate {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("trigger unavailable"))
	}
	return connect.NewResponse(&agentsv1.CreateAgentTriggerResponse{}), nil
}

func (h *graphAgentsHandler) ListAgentLinks(
	_ context.Context,
	_ *connect.Request[agentsv1.ListAgentLinksRequest],
) (*connect.Response[agentsv1.ListAgentLinksResponse], error) {
	return connect.NewResponse(&agentsv1.ListAgentLinksResponse{}), nil
}

func agentFromCreateRequest(id string, req *agentsv1.CreateAgentRequest) *agentsv1.AgentDefinition {
	return &agentsv1.AgentDefinition{
		Id:                  id,
		Name:                req.GetName(),
		Description:         req.GetDescription(),
		Model:               req.GetModel(),
		SystemPrompt:        req.GetSystemPrompt(),
		Tools:               req.GetTools(),
		Config:              req.GetConfig(),
		MaxTurns:            req.GetMaxTurns(),
		MaxToolCallsPerTurn: req.GetMaxToolCallsPerTurn(),
		Mode:                req.GetMode(),
		MaxSteps:            req.MaxSteps,
		TaskPermissionMode:  req.GetTaskPermissionMode(),
	}
}

func testRevisionFromRequest(agentID string, req *agentsv1.CreateAgentRevisionRequest) (*agentsv1.AgentRevision, error) {
	files := make([]agentrevision.File, 0, len(req.GetFiles()))
	for _, file := range req.GetFiles() {
		files = append(files, agentrevision.File{Path: file.GetPath(), Mode: file.GetMode(), Content: file.GetContent()})
	}
	functions := make([]agentrevision.Function, 0, len(req.GetFunctions()))
	for _, function := range req.GetFunctions() {
		var parameters map[string]interface{}
		if function.GetParameters() != nil {
			parameters = function.GetParameters().AsMap()
		}
		functions = append(functions, agentrevision.Function{
			Name: function.GetName(), Description: function.GetDescription(), Path: function.GetPath(),
			Export: function.GetExportName(), Runtime: isolation.Runtime(function.GetRuntime()), Parameters: parameters,
		})
	}
	manifest, err := agentrevision.NewManifest(files, functions)
	if err != nil {
		return nil, err
	}
	return &agentsv1.AgentRevision{
		Id: "revision-" + agentID, AgentId: agentID, Number: 1,
		Digest: manifest.Digest, Format: int32(manifest.Format),
	}, nil
}

type graphFunctionsHandler struct {
	functionsconnect.UnimplementedFunctionsServiceHandler
	createRequests []*functionsv1.CreateFunctionRequest
	functions      map[string]*functionsv1.Function
	getMisses      int
}

func (h *graphFunctionsHandler) GetFunctionByName(
	_ context.Context,
	req *connect.Request[functionsv1.GetFunctionByNameRequest],
) (*connect.Response[functionsv1.GetFunctionByNameResponse], error) {
	if h.getMisses > 0 {
		h.getMisses--
		return nil, connect.NewError(connect.CodeNotFound, errors.New("projection pending"))
	}
	if function := h.functions[req.Msg.GetName()]; function != nil {
		return connect.NewResponse(&functionsv1.GetFunctionByNameResponse{Function: function}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
}

func (h *graphFunctionsHandler) CreateFunction(
	_ context.Context,
	req *connect.Request[functionsv1.CreateFunctionRequest],
) (*connect.Response[functionsv1.CreateFunctionResponse], error) {
	h.createRequests = append(h.createRequests, req.Msg)
	if h.functions == nil {
		h.functions = map[string]*functionsv1.Function{}
	}
	function := &functionsv1.Function{
		Id: "created-" + req.Msg.GetName(), Name: req.Msg.GetName(), Description: req.Msg.GetDescription(),
		Mode: req.Msg.GetMode(), Isolated: req.Msg.GetIsolated(), Parameters: req.Msg.GetParameters(), Enabled: true,
	}
	h.functions[function.GetName()] = function
	return connect.NewResponse(&functionsv1.CreateFunctionResponse{Function: &functionsv1.Function{Id: function.GetId(), Name: function.GetName(), Mode: function.GetMode(), Enabled: true}}), nil
}

func newDeployGraphFactory(t *testing.T, agentsHandler agentsconnect.AgentsServiceHandler, functionsHandler functionsconnect.FunctionsServiceHandler) *client.Factory {
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

func TestDeployGraphPreflightsEveryProjectBeforeMutation(t *testing.T) {
	agentsHandler := &graphAgentsHandler{agents: []*agentsv1.AgentDefinition{{
		Id:     "child-id",
		Name:   "child",
		Config: &structpb.Struct{},
	}}}
	functionsHandler := &graphFunctionsHandler{}
	factory := newDeployGraphFactory(t, agentsHandler, functionsHandler)
	project := &agentproject.Project{
		Config:       agentproject.Config{Name: "parent", Model: "model", Permissions: agentproject.Permissions{TaskMode: "ask"}},
		Instructions: "parent",
		ToolFiles:    []agentproject.ToolFile{toolFixture()},
		Subagents: []*agentproject.Project{{
			Config:       agentproject.Config{Name: "child", Model: "model", Permissions: agentproject.Permissions{TaskMode: "ask"}},
			Instructions: "child",
		}},
	}

	err := deployGraph(context.Background(), factory, &bytes.Buffer{}, project, false)
	if err == nil || !strings.Contains(err.Error(), "child") {
		t.Fatalf("deployGraph() error = %v, want child preflight conflict", err)
	}
	if len(functionsHandler.createRequests) != 0 || len(agentsHandler.createRequests) != 0 || len(agentsHandler.updateRequests) != 0 {
		t.Fatalf("remote mutations occurred before preflight completed: functions=%d creates=%d updates=%d", len(functionsHandler.createRequests), len(agentsHandler.createRequests), len(agentsHandler.updateRequests))
	}
}

func TestDeployGraphValidatesEveryProjectConfigBeforeMutation(t *testing.T) {
	agentsHandler := &graphAgentsHandler{agents: []*agentsv1.AgentDefinition{{
		Id:     "parent-id",
		Name:   "parent",
		Config: &structpb.Struct{},
	}}}
	functionsHandler := &graphFunctionsHandler{}
	factory := newDeployGraphFactory(t, agentsHandler, functionsHandler)
	project := &agentproject.Project{
		Config:       agentproject.Config{Name: "parent", Model: "model", Permissions: agentproject.Permissions{TaskMode: "ask"}},
		Instructions: "parent",
		Subagents: []*agentproject.Project{{
			Config: agentproject.Config{
				Name:        "child",
				Model:       "model",
				Permissions: agentproject.Permissions{TaskMode: "ask"},
				Config: map[string]any{
					"sandbox": map[string]any{"network_mode": "unrestricted"},
				},
			},
			Instructions: "child",
			ToolFiles:    []agentproject.ToolFile{toolFixture()},
		}},
	}

	err := deployGraph(context.Background(), factory, &bytes.Buffer{}, project, true)
	if err == nil || !strings.Contains(err.Error(), "child") || !strings.Contains(err.Error(), "network_mode") {
		t.Fatalf("deployGraph() error = %v, want child network mode validation error", err)
	}
	if len(functionsHandler.createRequests) != 0 || len(agentsHandler.createRequests) != 0 || len(agentsHandler.updateRequests) != 0 {
		t.Fatalf("remote mutations occurred before config validation completed: functions=%d creates=%d updates=%d", len(functionsHandler.createRequests), len(agentsHandler.createRequests), len(agentsHandler.updateRequests))
	}
}

func TestDeployGraphDoesNotStampPartialTriggerFailure(t *testing.T) {
	agentsHandler := &graphAgentsHandler{failTriggerCreate: true}
	functionsHandler := &graphFunctionsHandler{}
	factory := newDeployGraphFactory(t, agentsHandler, functionsHandler)
	project := &agentproject.Project{
		Config: agentproject.Config{
			Name:        "support",
			Model:       "model",
			Permissions: agentproject.Permissions{TaskMode: "ask"},
			Triggers:    []agentproject.Trigger{{Name: "incoming", Type: "webhook"}},
		},
		Instructions: "help",
	}

	err := deployGraph(context.Background(), factory, &bytes.Buffer{}, project, false)
	if err == nil {
		t.Fatal("deployGraph() succeeded despite trigger failure")
	}
	if len(agentsHandler.createRequests) != 1 {
		t.Fatalf("CreateAgent calls = %d, want 1", len(agentsHandler.createRequests))
	}
	if agentsHandler.createRequests[0].GetConfig().GetFields()[projectMetaKey] != nil {
		t.Fatal("CreateAgent carried a success stamp before triggers converged")
	}
	for _, req := range agentsHandler.updateRequests {
		if req.GetConfig().GetFields()[projectMetaKey] != nil {
			t.Fatal("UpdateAgent wrote a success stamp after partial failure")
		}
	}
}
