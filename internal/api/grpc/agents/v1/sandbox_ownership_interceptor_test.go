package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

func ownershipTestManager() *sandbox.SandboxManager {
	m := &sandbox.SandboxManager{}
	m.SeedInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", Name: "web", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a"}},
		"sbx-b": {ID: "sbx-b", Name: "web", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-b"}},
	})
	return m
}

// TestSandboxOwnershipInterceptor exercises the ConnectRPC ownership
// interceptor: in enforce mode a caller cannot reach another tenant's sandbox
// (by id or colliding name) and 404s; the owner passes; in audit mode a
// cross-tenant call is allowed through (logged only).
func TestSandboxOwnershipInterceptor(t *testing.T) {
	t.Parallel()

	okNext := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&agentspb.StopSandboxResponse{}), nil
	}

	call := func(s *Server, tenant, sandboxID string) (connect.AnyResponse, error) {
		ic := s.sandboxOwnershipInterceptor()
		wrapped := ic.WrapUnary(okNext)
		ctx := contextkeys.WithTenantID(context.Background(), tenant)
		req := connect.NewRequest(&agentspb.StopSandboxRequest{SandboxId: sandboxID})
		return wrapped(ctx, req)
	}

	t.Run("enforce: owner passes", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: true}
		if _, err := call(s, "tenant-b", "sbx-b"); err != nil {
			t.Fatalf("owner should pass, got %v", err)
		}
	})

	t.Run("enforce: cross-tenant exact id is 404", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: true}
		_, err := call(s, "tenant-a", "sbx-b")
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("cross-tenant exact id should be NotFound, got %v", err)
		}
	})

	t.Run("enforce: colliding name resolves only within tenant", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: true}
		// "web" exists in both tenants; tenant-a must get its own, not be denied.
		if _, err := call(s, "tenant-a", "web"); err != nil {
			t.Fatalf("tenant-a should resolve its own 'web', got %v", err)
		}
	})

	t.Run("audit: cross-tenant allowed through (logged only)", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: false}
		if _, err := call(s, "tenant-a", "sbx-b"); err != nil {
			t.Fatalf("audit mode must not reject, got %v", err)
		}
	})

	t.Run("default constructor enforces", func(t *testing.T) {
		s := CreateServer()
		s.SetSandboxManager(ownershipTestManager())
		_, err := call(s, "tenant-a", "sbx-b")
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("default server should enforce ownership, got %v", err)
		}
	})
}
