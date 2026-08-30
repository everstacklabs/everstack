package fcagent

import (
	"context"
	"net"
	"testing"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	sandboxfcagent "github.com/everstacklabs/everstack/internal/sandbox/fcagent"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
)

// stubAgentServer is a minimal in-process FirecrackerAgent that answers
// only InvokeFunction, so the gateway backend can be driven over a real
// gRPC transport without KVM.
type stubAgentServer struct {
	fcpb.UnimplementedFirecrackerAgentServer
	handler func(*fcpb.InvokeFunctionRequest) (*fcpb.InvokeFunctionResult, error)
}

func (s *stubAgentServer) InvokeFunction(_ context.Context, req *fcpb.InvokeFunctionRequest) (*fcpb.InvokeFunctionResult, error) {
	return s.handler(req)
}

func startStubAgent(t *testing.T, h func(*fcpb.InvokeFunctionRequest) (*fcpb.InvokeFunctionResult, error)) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	fcpb.RegisterFirecrackerAgentServer(srv, &stubAgentServer{handler: h})
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), srv.Stop
}

func newTestBackend(t *testing.T, service string) *Backend {
	t.Helper()
	b, err := New(Config{
		Config: isolation.DefaultConfig(),
		Agent:  sandboxfcagent.Config{Service: service},
	})
	if err != nil {
		t.Fatalf("New backend: %v", err)
	}
	return b
}

// TestExecute_RoundTrip drives the full gateway -> gRPC -> agent -> result
// path in-process: env_key derivation, request marshaling, TryEach agent
// selection, and result mapping.
func TestExecute_RoundTrip(t *testing.T) {
	var got *fcpb.InvokeFunctionRequest
	addr, stop := startStubAgent(t, func(req *fcpb.InvokeFunctionRequest) (*fcpb.InvokeFunctionResult, error) {
		got = req
		return &fcpb.InvokeFunctionResult{
			Success:    true,
			ResultJson: `"hi"`,
			ColdStart:  true,
			DurationMs: 7,
		}, nil
	})
	defer stop()

	// Static two-entry list (both the same addr) so discovery uses the
	// static path rather than DNS.
	b := newTestBackend(t, addr+","+addr)
	defer func() { _ = b.Stop(context.Background()) }()
	_ = b.Start(context.Background())

	res, err := b.Execute(context.Background(), isolation.ExecutionRequest{
		FunctionID: "fn1",
		TenantID:   "t1",
		Runtime:    isolation.RuntimeNodeJS20,
		Code:       "export default () => 'hi'",
		Arguments:  map[string]interface{}{"a": 1},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success || res.Result != "hi" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got == nil {
		t.Fatalf("agent never received the request")
	}
	if got.EnvKey == "" {
		t.Fatalf("gateway did not compute an env_key")
	}
	if got.Runtime != "nodejs20" || got.FunctionId != "fn1" || got.TenantId != "t1" {
		t.Fatalf("request fields not propagated: %+v", got)
	}
	if got.ArgsJson == "" {
		t.Fatalf("arguments not serialized")
	}
}

// TestExecute_FailsOverOnUnavailable proves a down agent (connection
// refused -> Unavailable) fails over to a healthy one. The static list is
// ordered [bad, good] and TryEach deterministically starts at index 0 on
// the first call, so the bad target is tried first.
func TestExecute_FailsOverOnUnavailable(t *testing.T) {
	goodAddr, stop := startStubAgent(t, func(*fcpb.InvokeFunctionRequest) (*fcpb.InvokeFunctionResult, error) {
		return &fcpb.InvokeFunctionResult{Success: true, ResultJson: `1`}, nil
	})
	defer stop()

	// 127.0.0.1:1 is a closed port — dialing yields Unavailable.
	b := newTestBackend(t, "127.0.0.1:1,"+goodAddr)
	defer func() { _ = b.Stop(context.Background()) }()
	_ = b.Start(context.Background())

	res, err := b.Execute(context.Background(), isolation.ExecutionRequest{
		FunctionID: "fn1",
		Runtime:    isolation.RuntimeNodeJS20,
		Code:       "x",
	})
	if err != nil {
		t.Fatalf("expected failover to the healthy agent, got error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success via failover, got %+v", res)
	}
}

// TestExecute_UnsupportedRuntime is rejected at the gateway without a
// round-trip.
func TestExecute_UnsupportedRuntime(t *testing.T) {
	called := false
	addr, stop := startStubAgent(t, func(*fcpb.InvokeFunctionRequest) (*fcpb.InvokeFunctionResult, error) {
		called = true
		return &fcpb.InvokeFunctionResult{Success: true}, nil
	})
	defer stop()

	b := newTestBackend(t, addr+","+addr)
	defer func() { _ = b.Stop(context.Background()) }()

	res, err := b.Execute(context.Background(), isolation.ExecutionRequest{
		FunctionID: "fn1",
		Runtime:    isolation.Runtime("cobol"),
		Code:       "x",
	})
	if err != nil {
		t.Fatalf("unsupported runtime should return a result, not an error: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure for unsupported runtime")
	}
	if called {
		t.Fatalf("unsupported runtime must not reach the agent")
	}
}
