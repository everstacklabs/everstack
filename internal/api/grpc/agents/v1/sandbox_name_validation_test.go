package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/types/known/structpb"
)

func agentConfigWithSandboxName(t *testing.T, name string) *structpb.Struct {
	t.Helper()
	config, err := structpb.NewStruct(map[string]interface{}{
		"sandbox": map[string]interface{}{"name": name},
	})
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestCreateAgentRejectsReservedSandboxName(t *testing.T) {
	for _, name := range []string{"sbx_other-tenant", "WKS_agent"} {
		t.Run(name, func(t *testing.T) {
			commands := &recordingCommandBus{}
			_, err := (&Server{}).CreateAgent(agentMutationContext(ownerTenant, commands), connect.NewRequest(&agentspb.CreateAgentRequest{
				Name:   "agent",
				Model:  "test-model",
				Config: agentConfigWithSandboxName(t, name),
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), sandbox.ErrReservedSandboxName.Error()) {
				t.Fatalf("CreateAgent error = %v, want reserved-name InvalidArgument", err)
			}
			if commands.dispatched != 0 {
				t.Fatalf("commands dispatched = %d", commands.dispatched)
			}
		})
	}
}

func TestUpdateAgentRejectsReservedSandboxName(t *testing.T) {
	for _, name := range []string{"sbx_other-tenant", "WKS_agent"} {
		t.Run(name, func(t *testing.T) {
			commands := &recordingCommandBus{}
			_, err := (&Server{}).UpdateAgent(agentMutationContext(ownerTenant, commands), connect.NewRequest(&agentspb.UpdateAgentRequest{
				Id:     "agent-1",
				Config: agentConfigWithSandboxName(t, name),
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), sandbox.ErrReservedSandboxName.Error()) {
				t.Fatalf("UpdateAgent error = %v, want reserved-name InvalidArgument", err)
			}
			if commands.dispatched != 0 {
				t.Fatalf("commands dispatched = %d", commands.dispatched)
			}
		})
	}
}

func TestCreateSandboxNameRule(t *testing.T) {
	s := &Server{sandboxMgr: &sandbox.SandboxManager{}}
	tests := []struct {
		name     string
		wantCode connect.Code
	}{
		{name: "frontend", wantCode: connect.CodeInternal},
		{name: "database", wantCode: connect.CodeInternal},
		{name: "postgres", wantCode: connect.CodeInternal},
		{name: "notebook", wantCode: connect.CodeInternal},
		{name: "AbCdEf23", wantCode: connect.CodeInternal},
		{name: "sbx_other-tenant", wantCode: connect.CodeInvalidArgument},
		{name: "WKS_agent", wantCode: connect.CodeInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := s.CreateSandbox(verifiedTenantCtx("tenant-a"), connect.NewRequest(&agentspb.CreateSandboxRequest{Name: test.name}))
			if connect.CodeOf(err) != test.wantCode {
				t.Fatalf("CreateSandbox(%q) code = %v, error = %v, want %v", test.name, connect.CodeOf(err), err, test.wantCode)
			}
		})
	}
}

func TestRegisteredRESTCreateSandboxNameRule(t *testing.T) {
	s := &Server{sandboxMgr: &sandbox.SandboxManager{}}
	s.sandboxMgr.SetSandboxBillingResolver(func(string) bool { return false })
	mux := runtime.NewServeMux()
	if err := s.RegisterGateway(context.Background(), mux, "", nil); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		reserved bool
	}{
		{name: "frontend"},
		{name: "AbCdEf23"},
		{name: "sbx_x", reserved: true},
		{name: "WKS_x", reserved: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/sandbox", strings.NewReader(`{"name":"`+test.name+`"}`)).WithContext(verifiedTenantCtx("tenant-a"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound {
				t.Fatal("registered REST create route was not exercised")
			}
			gotReserved := strings.Contains(rec.Body.String(), sandbox.ErrReservedSandboxName.Error())
			if gotReserved != test.reserved {
				t.Fatalf("status = %d, body = %s, reserved = %v", rec.Code, rec.Body.String(), gotReserved)
			}
		})
	}
}

func TestRecreateSandboxRejectsReservedStoredName(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedLiveInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {
			ID:             "sbx-a",
			Name:           "WKS_agent",
			LifecycleState: sandbox.LifecycleRunning,
			Config:         sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "old"},
		},
	})
	s := &Server{sandboxMgr: mgr}
	_, err := s.RecreateSandbox(verifiedTenantCtx("tenant-a"), connect.NewRequest(&agentspb.RecreateSandboxRequest{
		SandboxId: "sbx-a",
		SessionId: "new-session",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), sandbox.ErrReservedSandboxName.Error()) {
		t.Fatalf("RecreateSandbox error = %v, want reserved-name InvalidArgument", err)
	}
}
