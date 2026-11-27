package v1

import (
	"context"
	"errors"
	"io"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

type ownedSessionQueryBus struct{}

const manualSandboxSessionID = "manual_12345678-1234-1234-1234-123456789abc"

func (ownedSessionQueryBus) Execute(_ context.Context, q query.Query) (interface{}, error) {
	lookup, ok := q.(*agentsquery.GetSessionByIDQuery)
	if !ok || lookup.ID != "session-1" || lookup.TenantID != ownerTenant {
		return &query.Response{Data: nil}, nil
	}
	return &query.Response{Data: &agentsquery.SessionWithTurns{Session: agentsquery.AgentSessionReadModel{
		ID:       lookup.ID,
		TenantID: lookup.TenantID,
		Status:   "active",
	}}}, nil
}

func (ownedSessionQueryBus) RegisterHandler(query.QueryHandler) {}

type countingSandboxBackend struct {
	destroys  int
	stats     int
	exposes   int
	unexpose  int
	detects   int
	destroyed bool
}

func (*countingSandboxBackend) Name() string { return "test" }
func (*countingSandboxBackend) Create(context.Context, string, sandbox.InstanceConfig) (*sandbox.Instance, error) {
	return nil, errors.New("not implemented")
}
func (*countingSandboxBackend) Exec(context.Context, string, sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	return nil, errors.New("not implemented")
}
func (*countingSandboxBackend) WriteFile(context.Context, string, string, []byte) error {
	return errors.New("not implemented")
}
func (*countingSandboxBackend) ReadFile(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}
func (*countingSandboxBackend) ListFiles(context.Context, string, string) ([]sandbox.FileInfo, error) {
	return nil, errors.New("not implemented")
}
func (b *countingSandboxBackend) Destroy(context.Context, string) error {
	b.destroys++
	b.destroyed = true
	return nil
}
func (b *countingSandboxBackend) Status(context.Context, string) (*sandbox.Instance, error) {
	if b.destroyed {
		return nil, sandbox.ErrSandboxNotRunning
	}
	return &sandbox.Instance{Status: sandbox.StatusRunning}, nil
}
func (*countingSandboxBackend) DescribePending(context.Context, string) string { return "" }
func (*countingSandboxBackend) Healthy(context.Context) error                  { return nil }
func (*countingSandboxBackend) Logs(context.Context, string, sandbox.LogsOptions) (io.ReadCloser, error) {
	return io.NopCloser(nilReader{}), nil
}
func (b *countingSandboxBackend) Stats(context.Context, string) (*sandbox.ContainerStats, error) {
	b.stats++
	return &sandbox.ContainerStats{}, nil
}
func (*countingSandboxBackend) Shell(context.Context, string, []string) (*sandbox.ShellSession, error) {
	return nil, errors.New("not implemented")
}
func (*countingSandboxBackend) List(context.Context) ([]*sandbox.Instance, error) { return nil, nil }
func (b *countingSandboxBackend) ExposePort(context.Context, string, int, string) (int, error) {
	b.exposes++
	return 18080, nil
}
func (b *countingSandboxBackend) UnexposePort(context.Context, string, int) error {
	b.unexpose++
	return nil
}
func (b *countingSandboxBackend) DetectListeningPorts(context.Context, string) ([]sandbox.ListeningPort, error) {
	b.detects++
	return []sandbox.ListeningPort{{Port: 8080, Protocol: "tcp"}}, nil
}

type nilReader struct{}

func (nilReader) Read([]byte) (int, error) { return 0, io.EOF }

func newSessionOperationServer(tenantID string) (*Server, *countingSandboxBackend, *recordingCommandBus, context.Context) {
	backend := &countingSandboxBackend{}
	mgr := sandbox.NewManager(backend, sandbox.GlobalSandboxConfig{})
	inst := &sandbox.Instance{
		ID:             "sandbox-1",
		Status:         sandbox.StatusRunning,
		LifecycleState: sandbox.LifecycleRunning,
		Config: sandbox.InstanceConfig{
			TenantID:    ownerTenant,
			SessionID:   "session-1",
			NetworkMode: sandbox.NetworkAllow,
		},
	}
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{"session-1": inst})
	mgr.SeedInstancesForTest(map[string]*sandbox.Instance{"sandbox-1": inst})
	commandBus := &recordingCommandBus{}
	ctx := cqrs.WithSystem(sessionPrincipal(tenantID), &cqrs.System{
		QueryBus:   ownedSessionQueryBus{},
		CommandBus: commandBus,
	})
	return &Server{sandboxMgr: mgr}, backend, commandBus, ctx
}

func newSandboxOperationServer(tenantID string) (*Server, *countingSandboxBackend, context.Context) {
	backend := &countingSandboxBackend{}
	mgr := sandbox.NewManager(backend, sandbox.GlobalSandboxConfig{})
	inst := &sandbox.Instance{
		ID:             "sandbox-1",
		Status:         sandbox.StatusRunning,
		LifecycleState: sandbox.LifecycleRunning,
		Config: sandbox.InstanceConfig{
			TenantID:    ownerTenant,
			SessionID:   manualSandboxSessionID,
			NetworkMode: sandbox.NetworkAllow,
		},
	}
	mgr.SeedSessionsForTest(map[string]*sandbox.Instance{manualSandboxSessionID: inst})
	mgr.SeedInstancesForTest(map[string]*sandbox.Instance{"sandbox-1": inst})
	ctx := cqrs.WithSystem(sessionPrincipal(tenantID), &cqrs.System{QueryBus: ownedSessionQueryBus{}})
	return &Server{sandboxMgr: mgr}, backend, ctx
}

func (b *countingSandboxBackend) operationCount() int {
	return b.destroys + b.stats + b.exposes + b.unexpose + b.detects
}

func TestSessionMutationHandlersRequireOwnedSession(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server, context.Context) error
	}{
		{
			name: "cancel",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.CancelSession(ctx, connect.NewRequest(&agentspb.CancelSessionRequest{SessionId: "session-1"}))
				return err
			},
		},
		{
			name: "complete",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.CompleteSession(ctx, connect.NewRequest(&agentspb.CompleteSessionRequest{SessionId: "session-1"}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" another tenant gets not found", func(t *testing.T) {
			s, backend, commandBus, ctx := newSessionOperationServer(otherTenant)
			wantNotFound(t, tt.name, tt.call(s, ctx))
			if commandBus.dispatched != 0 || backend.operationCount() != 0 {
				t.Fatalf("dispatches = %d, backend operations = %d", commandBus.dispatched, backend.operationCount())
			}
		})

		t.Run(tt.name+" owner completes the operation", func(t *testing.T) {
			s, backend, commandBus, ctx := newSessionOperationServer(ownerTenant)
			if err := tt.call(s, ctx); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if commandBus.dispatched != 1 || backend.destroys != 1 {
				t.Fatalf("dispatches = %d, destroys = %d", commandBus.dispatched, backend.destroys)
			}
		})
	}
}

func TestSandboxSessionOperationsRequireOwnedSession(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server, context.Context) error
	}{
		{
			name: "destroy",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.DestroySandbox(ctx, connect.NewRequest(&agentspb.DestroySandboxRequest{SessionId: manualSandboxSessionID}))
				return err
			},
		},
		{
			name: "stats",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.GetSandboxStats(ctx, connect.NewRequest(&agentspb.GetSandboxStatsRequest{SessionId: manualSandboxSessionID}))
				return err
			},
		},
		{
			name: "expose port",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.ExposePort(ctx, connect.NewRequest(&agentspb.ExposePortRequest{SessionId: manualSandboxSessionID, Port: 8080, Protocol: "tcp"}))
				return err
			},
		},
		{
			name: "unexpose port",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.UnexposePort(ctx, connect.NewRequest(&agentspb.UnexposePortRequest{SessionId: manualSandboxSessionID, Port: 8080}))
				return err
			},
		},
		{
			name: "list exposed ports",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.ListExposedPorts(ctx, connect.NewRequest(&agentspb.ListExposedPortsRequest{SessionId: manualSandboxSessionID}))
				return err
			},
		},
		{
			name: "detect listening ports",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.DetectListeningPorts(ctx, connect.NewRequest(&agentspb.DetectListeningPortsRequest{SessionId: manualSandboxSessionID}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" another tenant gets not found", func(t *testing.T) {
			s, backend, ctx := newSandboxOperationServer(otherTenant)
			wantNotFound(t, tt.name, tt.call(s, ctx))
			if backend.operationCount() != 0 {
				t.Fatalf("backend operations = %d", backend.operationCount())
			}
		})

		t.Run(tt.name+" owner completes the operation", func(t *testing.T) {
			s, _, ctx := newSandboxOperationServer(ownerTenant)
			if err := tt.call(s, ctx); err != nil {
				t.Fatalf("owner call: %v", err)
			}
		})
	}
}
