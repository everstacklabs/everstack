package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/sandbox"
	clipb "github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1"
)

func TestStopSandboxHidesForeignAndMissingSandboxesIdentically(t *testing.T) {
	mgr := &sandbox.SandboxManager{}
	mgr.SeedInstancesForTest(map[string]*sandbox.Instance{
		"sbx-b": {ID: "sbx-b", Config: sandbox.InstanceConfig{TenantID: "tenant-b"}},
	})
	s := &CLIServer{sandboxMgr: mgr}
	ctx := contextkeys.WithUserID(contextkeys.WithTenantID(context.Background(), "tenant-a"), "user-a")

	call := func(id string) error {
		_, err := s.StopSandbox(ctx, connect.NewRequest(&clipb.StopSandboxRequest{SandboxId: id}))
		return err
	}
	foreign := call("sbx-b")
	missing := call("sbx-missing")
	if connect.CodeOf(foreign) != connect.CodeNotFound || connect.CodeOf(missing) != connect.CodeNotFound {
		t.Fatalf("foreign = %v, missing = %v", foreign, missing)
	}
	if foreign.Error() != missing.Error() || foreign.Error() != "not_found: sandbox not found" {
		t.Fatalf("foreign = %q, missing = %q", foreign, missing)
	}
}
