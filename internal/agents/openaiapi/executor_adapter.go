package openaiapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
)

type ExecutorAdapter struct {
	Service     executor.Service
	BackendName string
}

func (a ExecutorAdapter) ValidateConfiguration(context.Context) error {
	if a.Service == nil {
		return ErrUnavailable
	}
	if !executor.SupportedBackend(a.BackendName) {
		return fmt.Errorf("sandbox backend %q is unsupported for OpenAI self_hosted environments: %w", a.BackendName, ErrUnavailable)
	}
	return nil
}

func (a ExecutorAdapter) Provision(ctx context.Context, r EnvironmentProvisionRequest) (*EnvironmentState, error) {
	e, err := a.Service.Provision(ctx, executor.ProvisionRequest{Ownership: executor.Ownership{OrganizationID: r.OrganizationID, InstanceID: r.InstanceID}, LocalSessionID: r.LocalSessionID, UpstreamEnvironmentID: r.UpstreamEnvironmentID, RemoteURL: r.RemoteURL, ExecutorAPIKey: r.ExecutorAPIKey, WorkspaceDirectory: r.WorkspaceDirectory, Image: r.Image, Lifetime: r.Lifetime, FenceStartupTimeout: r.FenceStartupTimeout})
	if e == nil {
		return nil, err
	}
	return &EnvironmentState{SandboxID: e.SandboxID, Status: string(e.Status), Error: e.Error}, err
}
func (a ExecutorAdapter) Relinquish(ctx context.Context, o Ownership, id string) error {
	return a.Service.Relinquish(ctx, executor.Ownership{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID}, id)
}
func (a ExecutorAdapter) DeleteReference(ctx context.Context, o Ownership, id, sandboxID string) error {
	return a.Service.DeleteReference(ctx, executor.Ownership{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID}, id, sandboxID)
}
func (a ExecutorAdapter) Reconcile(ctx context.Context, r EnvironmentReconcileRequest) (*EnvironmentState, error) {
	e, err := a.Service.Reconcile(ctx, executor.ReconcileRequest{Ownership: executor.Ownership{OrganizationID: r.OrganizationID, InstanceID: r.InstanceID}, RemoteURL: r.RemoteURL, ExecutorAPIKey: r.ExecutorAPIKey, Lifetime: r.Lifetime, FenceStartupTimeout: r.FenceStartupTimeout, Environment: executor.Environment{SandboxID: r.SandboxID, LocalSessionID: r.LocalSessionID, UpstreamEnvironmentID: r.UpstreamEnvironmentID, Status: executor.Status(r.Status)}})
	if err != nil {
		return nil, err
	}
	return &EnvironmentState{SandboxID: e.SandboxID, Status: string(e.Status), Error: e.Error}, nil
}
func (a ExecutorAdapter) Observe(ctx context.Context, r EnvironmentObserveRequest) (*EnvironmentState, error) {
	e, err := a.Service.Observe(ctx, executor.ObserveRequest{Ownership: executor.Ownership{OrganizationID: r.OrganizationID, InstanceID: r.InstanceID}, LocalSessionID: r.LocalSessionID, Status: executor.Status(r.Status), Error: r.Error})
	if errors.Is(err, executor.ErrNotBound) {
		return nil, errEnvironmentNotBound
	}
	if err != nil {
		return nil, err
	}
	return &EnvironmentState{SandboxID: e.SandboxID, Status: string(e.Status), Error: e.Error}, nil
}
