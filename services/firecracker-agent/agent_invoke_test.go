package main

import (
	"context"
	"testing"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestAgentServer(t *testing.T, fake *fakeEnvBackend) *AgentServer {
	t.Helper()
	m := NewFunctionEnvManager(fake, "/tmp/function", nil, time.Minute)
	t.Cleanup(m.Stop)
	s := NewAgentServer(nil, "test-node")
	s.SetFunctionEnvManager(m)
	return s
}

func TestAgentServer_InvokeFunction_HappyPath(t *testing.T) {
	fake := newFakeEnvBackend()
	s := newTestAgentServer(t, fake)

	res, err := s.InvokeFunction(context.Background(), &fcpb.InvokeFunctionRequest{
		FunctionId: "fn1",
		TenantId:   "t1",
		RequestId:  "r1",
		EnvKey:     "key-1",
		Runtime:    "nodejs20",
		Code:       "export default () => 'ok'",
		ArgsJson:   `{"a":1}`,
		TimeoutMs:  5000,
		MemoryMb:   512,
		Vcpus:      1,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success: %+v", res)
	}
	// fake returns {"__result__":"ok"}, so the result marshals back to "ok".
	if res.ResultJson != `"ok"` {
		t.Fatalf("expected result_json \"ok\", got %q", res.ResultJson)
	}
	if !res.ColdStart {
		t.Fatalf("first invocation should be a cold start")
	}
}

func TestAgentServer_InvokeFunction_Defaults(t *testing.T) {
	fake := newFakeEnvBackend()
	s := newTestAgentServer(t, fake)

	// Zero sizing / empty network / empty env_key: the handler must fill
	// defaults and synthesize an env_key rather than fail.
	res, err := s.InvokeFunction(context.Background(), &fcpb.InvokeFunctionRequest{
		FunctionId: "fn1",
		TenantId:   "t1",
		Runtime:    "nodejs20",
		Code:       "x",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success with defaulted sizing: %+v", res)
	}
	if got := fake.createdIDs(); len(got) != 1 {
		t.Fatalf("expected one env created, got %v", got)
	}
}

func TestAgentServer_InvokeFunction_UnsupportedRuntime(t *testing.T) {
	fake := newFakeEnvBackend()
	s := newTestAgentServer(t, fake)

	res, err := s.InvokeFunction(context.Background(), &fcpb.InvokeFunctionRequest{
		FunctionId: "fn1",
		Runtime:    "haskell",
		Code:       "x",
	})
	if err != nil {
		t.Fatalf("unsupported runtime should return a result, not an error: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure for unsupported runtime")
	}
	if got := fake.createdIDs(); len(got) != 0 {
		t.Fatalf("no env should be created for an unsupported runtime, got %v", got)
	}
}

func TestAgentServer_InvokeFunction_NotReady(t *testing.T) {
	// No FunctionEnvManager installed -> Unavailable, not a panic.
	s := NewAgentServer(nil, "test-node")
	_, err := s.InvokeFunction(context.Background(), &fcpb.InvokeFunctionRequest{
		FunctionId: "fn1",
		Runtime:    "nodejs20",
		Code:       "x",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable before the runtime is ready, got %v", err)
	}
}
