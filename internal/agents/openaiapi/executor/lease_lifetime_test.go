package executor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

type lifetimeBackend struct {
	mu             sync.Mutex
	owner          Ownership
	instance       *sandbox.Instance
	verifyStarted  chan struct{}
	releaseVerify  chan struct{}
	executorStarts int
	destroys       int
}

func (b *lifetimeBackend) Provision(context.Context, Ownership, string, sandbox.SandboxConfig) (*sandbox.Instance, error) {
	return b.instance, nil
}
func (b *lifetimeBackend) VerifyOwnership(context.Context, Ownership, string) error { return nil }
func (b *lifetimeBackend) Exec(ctx context.Context, _ Ownership, _ string, c Command) (*sandbox.ExecResult, error) {
	if len(c.Argv) > 1 && c.Argv[1] == "--version" {
		select {
		case <-b.verifyStarted:
		default:
			close(b.verifyStarted)
		}
		select {
		case <-b.releaseVerify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &sandbox.ExecResult{Stdout: "codex-cli " + ExecutorVersion, ExitCode: 0}, nil
	}
	b.mu.Lock()
	b.executorStarts++
	b.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*lifetimeBackend) ReadFile(context.Context, Ownership, string, string) ([]byte, error) {
	return nil, nil
}
func (*lifetimeBackend) WriteFile(context.Context, Ownership, string, string, []byte) error {
	return nil
}
func (*lifetimeBackend) ListFiles(context.Context, Ownership, string, string) ([]sandbox.FileInfo, error) {
	return nil, nil
}
func (b *lifetimeBackend) Destroy(context.Context, Ownership, string, string) error {
	b.mu.Lock()
	b.destroys++
	b.mu.Unlock()
	return nil
}

func TestLeaseLossFencesInFlightProvisionAndReconcile(t *testing.T) {
	for _, operation := range []string{"provision", "reconcile"} {
		t.Run(operation, func(t *testing.T) {
			owner := Ownership{OrganizationID: "org", InstanceID: "instance"}
			backend := &lifetimeBackend{owner: owner, instance: &sandbox.Instance{ID: "sbx-session", Status: sandbox.StatusRunning}, verifyStarted: make(chan struct{}), releaseVerify: make(chan struct{})}
			manager := New(backend)
			lifetime, cancelLifetime := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				if operation == "provision" {
					_, err := manager.Provision(context.Background(), ProvisionRequest{Ownership: owner, LocalSessionID: "session", UpstreamEnvironmentID: "environment", RemoteURL: "https://api.openai.com/environment", ExecutorAPIKey: "executor", Image: LocalExecutorImage, Lifetime: lifetime})
					done <- err
					return
				}
				_, err := manager.Reconcile(context.Background(), ReconcileRequest{Ownership: owner, Environment: Environment{SandboxID: "sbx-session", LocalSessionID: "session", UpstreamEnvironmentID: "environment"}, RemoteURL: "https://api.openai.com/environment", ExecutorAPIKey: "executor", Lifetime: lifetime})
				done <- err
			}()
			select {
			case <-backend.verifyStarted:
			case <-time.After(time.Second):
				t.Fatal("verification did not start")
			}
			cancelLifetime()
			close(backend.releaseVerify)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("operation succeeded after lease loss")
				}
			case <-time.After(time.Second):
				t.Fatal("operation did not stop")
			}
			time.Sleep(10 * time.Millisecond)
			backend.mu.Lock()
			starts := backend.executorStarts
			destroys := backend.destroys
			backend.mu.Unlock()
			if starts != 0 {
				t.Fatalf("executor starts=%d after lease loss", starts)
			}
			if operation == "provision" && destroys != 1 {
				t.Fatalf("provision cleanup destroys=%d, want 1", destroys)
			}
		})
	}
}
