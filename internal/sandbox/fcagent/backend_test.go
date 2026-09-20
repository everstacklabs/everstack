package fcagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ambiguousExecServer struct {
	fcpb.UnimplementedFirecrackerAgentServer
	calls       atomic.Int32
	statusCalls atomic.Int32
}

func (s *ambiguousExecServer) Exec(context.Context, *fcpb.ExecRequest) (*fcpb.ExecResult, error) {
	if s.calls.Add(1) == 1 {
		return nil, status.Error(codes.Unavailable, "response lost after execution started")
	}
	return &fcpb.ExecResult{ExitCode: 0, Stdout: "recovered"}, nil
}
func (s *ambiguousExecServer) Status(context.Context, *fcpb.StatusRequest) (*fcpb.SandboxInstance, error) {
	s.statusCalls.Add(1)
	return &fcpb.SandboxInstance{Id: "sbx-test"}, nil
}
func TestExecDoesNotReplayAmbiguousTransportFailureAndNextCallRediscovers(t *testing.T) {
	server := &ambiguousExecServer{}
	backend := startFakeAgent(t, server)
	_, err := backend.Exec(context.Background(), "sbx-test", sandbox.ExecRequest{Command: []string{"side-effect"}})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("error = %v", err)
	}
	if calls := server.calls.Load(); calls != 1 {
		t.Fatalf("command executed %d times", calls)
	}
	backend.mu.RLock()
	_, routeStillCached := backend.routes["sbx-test"]
	backend.mu.RUnlock()
	if routeStillCached {
		t.Fatal("recoverable Exec failure left stale route cached")
	}

	result, err := backend.Exec(context.Background(), "sbx-test", sandbox.ExecRequest{Command: []string{"retry-by-caller"}})
	if err != nil {
		t.Fatalf("subsequent Exec failed after route invalidation: %v", err)
	}
	if result.Stdout != "recovered" {
		t.Fatalf("subsequent Exec stdout = %q, want recovered", result.Stdout)
	}
	if calls := server.calls.Load(); calls != 2 {
		t.Fatalf("command executed %d times total, want one call per request", calls)
	}
	if calls := server.statusCalls.Load(); calls != 1 {
		t.Fatalf("route discovery Status calls = %d, want 1", calls)
	}
}

func TestSeedRouteRestoresRouteTable(t *testing.T) {
	b := &FCAgentBackend{routes: make(map[string]string)}

	b.SeedRoute("wks-agent", "10.0.0.7:9090")

	b.mu.RLock()
	got := b.routes["wks-agent"]
	b.mu.RUnlock()
	if got != "10.0.0.7:9090" {
		t.Fatalf("SeedRoute target = %q, want %q", got, "10.0.0.7:9090")
	}
}

func TestSeedRouteIgnoresEmptyValues(t *testing.T) {
	b := &FCAgentBackend{routes: make(map[string]string)}

	b.SeedRoute("", "10.0.0.7:9090")
	b.SeedRoute("wks-agent", "")

	if len(b.routes) != 0 {
		t.Fatalf("SeedRoute stored empty route values: %#v", b.routes)
	}
}

func TestRouteForMissingRouteReturnsTypedError(t *testing.T) {
	b := &FCAgentBackend{
		discovery: &Discovery{},
		routes:    make(map[string]string),
	}

	_, _, err := b.routeFor(context.Background(), "wks-missing")
	if !errors.Is(err, sandbox.ErrSandboxRouteMissing) {
		t.Fatalf("routeFor err = %v, want ErrSandboxRouteMissing", err)
	}
}

func TestBackendTargetUsesOwningAgentHostAndExposedPort(t *testing.T) {
	b := &FCAgentBackend{
		portMappings: map[string]map[int]*remotePortMapping{
			"sbx-1": {
				3000: {Target: "10.0.0.7:9090", HostPort: 41000, Protocol: "tcp"},
			},
		},
	}

	got, err := b.BackendTarget(context.Background(), "sbx-1", 3000)
	if err != nil {
		t.Fatalf("BackendTarget error: %v", err)
	}
	if got != "10.0.0.7:41000" {
		t.Fatalf("BackendTarget = %q, want 10.0.0.7:41000", got)
	}
}

func TestBackendTargetMissingMapping(t *testing.T) {
	b := &FCAgentBackend{portMappings: make(map[string]map[int]*remotePortMapping)}

	if _, err := b.BackendTarget(context.Background(), "missing", 3000); err == nil {
		t.Fatal("BackendTarget missing mapping expected error")
	}
}
