package openaiapi

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
)

type partialProvisionExecutor struct{}

func (partialProvisionExecutor) Provision(context.Context, executor.ProvisionRequest) (*executor.Environment, error) {
	return &executor.Environment{SandboxID: "sbx-partial", Status: executor.StatusFailed, Error: "setup failed"}, errors.New("setup failed; cleanup failed")
}
func (partialProvisionExecutor) Status(context.Context, executor.Ownership, string) (*executor.Environment, error) {
	return nil, nil
}
func (partialProvisionExecutor) Observe(context.Context, executor.ObserveRequest) (*executor.Environment, error) {
	return nil, nil
}
func (partialProvisionExecutor) Stop(context.Context, executor.Ownership, string) error { return nil }
func (partialProvisionExecutor) Delete(context.Context, executor.Ownership, string) error {
	return nil
}
func (partialProvisionExecutor) Relinquish(context.Context, executor.Ownership, string) error {
	return nil
}
func (partialProvisionExecutor) DeleteReference(context.Context, executor.Ownership, string, string) error {
	return nil
}
func (partialProvisionExecutor) Reconcile(context.Context, executor.ReconcileRequest) (*executor.Environment, error) {
	return nil, nil
}

func TestExecutorAdapterPreservesPartialProvisionStateWithError(t *testing.T) {
	state, err := (ExecutorAdapter{Service: partialProvisionExecutor{}, BackendName: "firecracker"}).Provision(context.Background(), EnvironmentProvisionRequest{})
	if err == nil {
		t.Fatal("expected provision error")
	}
	if state == nil || state.SandboxID != "sbx-partial" || state.Status != string(executor.StatusFailed) || state.Error != "setup failed" {
		t.Fatalf("partial state = %#v", state)
	}
}
