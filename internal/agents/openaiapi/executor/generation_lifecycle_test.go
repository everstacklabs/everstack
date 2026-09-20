package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStaleBindingCallbacksDoNotMutateReplacement(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	manager := New(newFakeBackend(owner))
	old := &binding{
		environment: Environment{LocalSessionID: "local-1", SandboxID: "sbx-test", Status: StatusPending},
		owner:       owner,
		cancel:      func() {},
	}
	replacement := &binding{
		environment: Environment{LocalSessionID: "local-1", SandboxID: "sbx-test", Status: StatusPending},
		owner:       owner,
		cancel:      func() {},
	}
	manager.bindings["local-1"] = replacement

	manager.failIfPending(old, "stale timeout")
	manager.setFailed(old, "stale executor exit")

	if replacement.environment.Status != StatusPending || replacement.environment.Error != "" {
		t.Fatalf("stale callback mutated replacement: %+v", replacement.environment)
	}
}

type retirementBackend struct {
	*fakeBackend
	destroyStarted chan struct{}
	releaseDestroy chan struct{}
}

func (b *retirementBackend) Destroy(ctx context.Context, owner Ownership, id, key string) error {
	select {
	case <-b.destroyStarted:
	default:
		close(b.destroyStarted)
	}
	select {
	case <-b.releaseDestroy:
		return b.fakeBackend.Destroy(ctx, owner, id, key)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestReconcileDoesNotReplaceBindingCommittedToTimeoutCleanup(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := &retirementBackend{
		fakeBackend:    newFakeBackend(owner),
		destroyStarted: make(chan struct{}),
		releaseDestroy: make(chan struct{}),
	}
	manager := New(backend)
	old := &binding{
		environment: Environment{
			LocalSessionID:        "local-1",
			SandboxID:             backend.instance.ID,
			UpstreamEnvironmentID: "env-1",
			Status:                StatusPending,
		},
		owner:               owner,
		remoteURL:           "https://api.openai.com/v1/environments/remote",
		sandboxKey:          sandboxSessionKey("local-1"),
		cancel:              func() {},
		fenceStartupTimeout: func(context.Context) error { return nil },
	}
	manager.bindings["local-1"] = old

	manager.failIfPending(old, "executor connection timed out")
	<-backend.destroyStarted
	if _, err := manager.Observe(context.Background(), ObserveRequest{
		Ownership: owner, LocalSessionID: "local-1", Status: StatusConnected,
	}); err == nil || !strings.Contains(err.Error(), "cleanup is in progress") {
		t.Fatalf("Observe error = %v, want cleanup in progress", err)
	}
	if old.environment.Status != StatusFailed || old.environment.Error != "executor connection timed out" {
		t.Fatalf("late observation overwrote timeout diagnosis: %+v", old.environment)
	}
	if err := manager.Relinquish(context.Background(), owner, "local-1"); err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	retained := manager.bindings["local-1"]
	manager.mu.RUnlock()
	if retained != old {
		t.Fatal("relinquish removed a binding committed to cleanup")
	}

	_, err := manager.Reconcile(context.Background(), ReconcileRequest{
		Environment:    old.environment,
		Ownership:      owner,
		RemoteURL:      old.remoteURL,
		ExecutorAPIKey: "fresh-key",
	})
	if err == nil || !strings.Contains(err.Error(), "cleanup is in progress") {
		t.Fatalf("Reconcile error = %v, want cleanup in progress", err)
	}
	manager.mu.RLock()
	got := manager.bindings["local-1"]
	manager.mu.RUnlock()
	if got != old {
		t.Fatal("reconcile replaced a binding committed to cleanup")
	}

	close(backend.releaseDestroy)
	<-backend.destroyedSignal
	backend.mu.Lock()
	destroyed := backend.destroyed
	backend.mu.Unlock()
	if destroyed != 1 {
		t.Fatalf("destroyed = %d, want 1", destroyed)
	}
}

func TestStartupTimeoutDoesNotDestroyWhenDurableFenceIsLost(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	manager := New(backend)
	b := &binding{
		environment: Environment{LocalSessionID: "local-1", SandboxID: backend.instance.ID, Status: StatusPending},
		owner:       owner, sandboxKey: sandboxSessionKey("local-1"), cancel: func() {},
		fenceStartupTimeout: func(context.Context) error { return context.Canceled },
	}
	manager.bindings["local-1"] = b
	manager.failIfPending(b, "executor connection timed out")
	select {
	case <-backend.destroyedSignal:
		t.Fatal("destroy ran without a durable timeout fence")
	case <-time.After(50 * time.Millisecond):
	}
}
