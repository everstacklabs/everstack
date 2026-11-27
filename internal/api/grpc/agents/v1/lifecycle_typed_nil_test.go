package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// Without a lifecycle reconciler the server holds a nil repository. Stop,
// revive and terminate must report that cleanly, never panic.
func TestLifecycleCallsWithoutAReconcilerDoNotPanic(t *testing.T) {
	s := &Server{}
	ctx := context.Background()
	calls := map[string]func() error{
		"stop": func() error {
			_, err := s.StopSandbox(ctx, connect.NewRequest(&agentspb.StopSandboxRequest{SandboxId: "sbx-a"}))
			return err
		},
		"revive": func() error {
			_, err := s.ReviveSandbox(ctx, connect.NewRequest(&agentspb.ReviveSandboxRequest{SandboxId: "sbx-a"}))
			return err
		},
		"terminate": func() error {
			_, err := s.TerminateSandbox(ctx, connect.NewRequest(&agentspb.TerminateSandboxRequest{SandboxId: "sbx-a"}))
			return err
		},
	}
	for name, call := range calls {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s panicked: %v", name, r)
				}
			}()
			if err := call(); err == nil {
				t.Fatalf("%s succeeded with no lifecycle service configured", name)
			}
		}()
	}
}
