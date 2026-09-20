package executor

import (
	"context"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

type reconnectBackend struct {
	*fakeBackend
	executorStarted chan struct{}
}

func (b *reconnectBackend) Exec(ctx context.Context, owner Ownership, id string, cmd Command) (*sandbox.ExecResult, error) {
	if err := b.VerifyOwnership(ctx, owner, id); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.commands = append(b.commands, cmd)
	if len(cmd.Argv) == 2 && cmd.Argv[0] == "codex" && cmd.Argv[1] == "--version" {
		b.mu.Unlock()
		return &sandbox.ExecResult{Stdout: "codex-cli " + ExecutorVersion, ExitCode: 0}, nil
	}
	select {
	case b.executorStarted <- struct{}{}:
	default:
	}
	b.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func awaitExecutorStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not start")
	}
}

func TestReconcileAfterRelinquishRequiresFreshObservation(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := &reconnectBackend{
		fakeBackend:     newFakeBackend(owner),
		executorStarted: make(chan struct{}, 3),
	}
	manager := New(backend)
	req := ReconcileRequest{
		Ownership: owner,
		Environment: Environment{
			SandboxID:             backend.instance.ID,
			LocalSessionID:        "local-reconnect",
			UpstreamEnvironmentID: "env-reconnect",
			Status:                StatusConnected,
		},
		RemoteURL:      "https://api.openai.com/reconnect",
		ExecutorAPIKey: "executor-secret",
	}
	if _, err := manager.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	awaitExecutorStart(t, backend.executorStarted)
	if _, err := manager.Observe(context.Background(), ObserveRequest{Ownership: owner, LocalSessionID: req.Environment.LocalSessionID, Status: StatusConnected}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Relinquish(context.Background(), owner, req.Environment.LocalSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	awaitExecutorStart(t, backend.executorStarted)

	got, err := manager.Status(context.Background(), owner, req.Environment.LocalSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending {
		t.Fatalf("pre-failover observation marked replacement binding %s, want pending", got.Status)
	}

	if _, err = manager.Observe(context.Background(), ObserveRequest{Ownership: owner, LocalSessionID: req.Environment.LocalSessionID, Status: StatusConnected}); err != nil {
		t.Fatal(err)
	}
	got, err = manager.Status(context.Background(), owner, req.Environment.LocalSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusConnected {
		t.Fatalf("post-reconcile observation left replacement binding %s, want connected", got.Status)
	}
	if err := manager.Delete(context.Background(), owner, req.Environment.LocalSessionID); err != nil {
		t.Fatal(err)
	}

	backend.mu.Lock()
	commands := append([]Command(nil), backend.commands...)
	backend.mu.Unlock()
	var versions, executors int
	for _, cmd := range commands {
		if len(cmd.Argv) == 2 && cmd.Argv[0] == "codex" && cmd.Argv[1] == "--version" {
			versions++
		} else if len(cmd.Argv) >= 2 && cmd.Argv[0] == "codex" && cmd.Argv[1] == "exec-server" {
			executors++
		}
	}
	if versions != 2 || executors != 2 {
		t.Fatalf("commands contained %d version checks and %d executor starts, want 2 and 2", versions, executors)
	}
	select {
	case <-backend.executorStarted:
		t.Fatal("unexpected extra executor start")
	default:
	}
}
