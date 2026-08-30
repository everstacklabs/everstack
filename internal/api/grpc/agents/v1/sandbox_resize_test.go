package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

func TestResizeSandboxReturnsUnimplementedUntilRuntimeResizeExists(t *testing.T) {
	t.Parallel()

	mgr := &sandbox.SandboxManager{}
	mgr.SeedInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {
			ID:     "sbx-a",
			Status: sandbox.StatusRunning,
			Config: sandbox.InstanceConfig{TenantID: "tenant-a", CPULimit: 1, MemoryMB: 1024},
		},
	})
	s := &Server{sandboxMgr: mgr}
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")

	_, err := s.ResizeSandbox(ctx, connect.NewRequest(&agentspb.ResizeSandboxRequest{
		SandboxId:     "sbx-a",
		CpuMillicores: 2000,
		MemoryMb:      2048,
	}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("expected resize to be unimplemented, got %v", err)
	}
}

func TestResizeSandboxValidatesRequestBeforeUnsupportedResponse(t *testing.T) {
	t.Parallel()

	s := &Server{sandboxMgr: &sandbox.SandboxManager{}}
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")

	_, err := s.ResizeSandbox(ctx, connect.NewRequest(&agentspb.ResizeSandboxRequest{
		SandboxId:     "sbx-a",
		CpuMillicores: -1,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected invalid argument for negative CPU, got %v", err)
	}
}

func TestResizeSandboxUsesScopedLookup(t *testing.T) {
	t.Parallel()

	mgr := &sandbox.SandboxManager{}
	mgr.SeedInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", Status: sandbox.StatusRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a"}},
	})
	s := &Server{sandboxMgr: mgr}
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-b")

	_, err := s.ResizeSandbox(ctx, connect.NewRequest(&agentspb.ResizeSandboxRequest{
		SandboxId:     "sbx-a",
		CpuMillicores: 2000,
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected scoped not found, got %v", err)
	}
}
