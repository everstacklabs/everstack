package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestLifecycleServiceUsesReconcilerWhenConfigured(t *testing.T) {
	t.Parallel()

	repo := &fakeLifecycleRepo{}
	mgr := &fakeLifecycleManager{}
	svc := NewLifecycleService(repo, mgr)

	res, err := svc.Stop(context.Background(), "sbx-a")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !res.Success || repo.desiredID != "sbx-a" || repo.desiredState != sandboxlc.DesireSleeping || mgr.stopCalled {
		t.Fatalf("unexpected stop routing: res=%+v repo=%+v mgr=%+v", res, repo, mgr)
	}

	res, err = svc.Terminate(context.Background(), "sbx-b")
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if !res.Success || repo.desiredID != "sbx-b" || repo.desiredState != sandboxlc.DesireTerminated || mgr.terminateCalled {
		t.Fatalf("unexpected terminate routing: res=%+v repo=%+v mgr=%+v", res, repo, mgr)
	}
}

func TestLifecycleServiceReviveReturnsReconcilerRow(t *testing.T) {
	t.Parallel()

	now := time.Now()
	repo := &fakeLifecycleRepo{row: sandboxlc.Row{
		ID:             "sbx-a",
		Backend:        "firecracker",
		ContainerID:    sql.NullString{String: "vm-a", Valid: true},
		Image:          "alpine",
		Name:           "web",
		LifecycleState: sandbox.LifecycleReviving,
		CreatedAt:      now,
	}}
	res, err := NewLifecycleService(repo, nil).Revive(context.Background(), "sbx-a")
	if err != nil {
		t.Fatalf("revive: %v", err)
	}
	if repo.desiredState != sandboxlc.DesireRunning || res.Instance == nil || res.Instance.ID != "sbx-a" || res.Instance.Status != "pending" {
		t.Fatalf("unexpected revive result: res=%+v repo=%+v", res, repo)
	}
}

func TestLifecycleServiceUsesLegacyManagerWhenNoRepo(t *testing.T) {
	t.Parallel()

	now := time.Now()
	mgr := &fakeLifecycleManager{revive: &sandbox.Instance{
		ID:             "sbx-a",
		Backend:        "docker",
		ContainerID:    "container-a",
		Config:         sandbox.InstanceConfig{Image: "alpine"},
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
		LifecycleState: sandbox.LifecycleRunning,
		AgentHealthy:   true,
	}}
	svc := NewLifecycleService(nil, mgr)
	if _, err := svc.Stop(context.Background(), "sbx-a"); err != nil {
		t.Fatalf("legacy stop: %v", err)
	}
	res, err := svc.Revive(context.Background(), "sbx-a")
	if err != nil {
		t.Fatalf("legacy revive: %v", err)
	}
	if !mgr.stopCalled || res.Instance == nil || res.Instance.Status != "running" || !res.Instance.AgentHealthy {
		t.Fatalf("unexpected legacy result: res=%+v mgr=%+v", res, mgr)
	}
}

func TestLifecycleServiceDestroyPurgesPersistedRow(t *testing.T) {
	t.Parallel()

	mgr := &fakeLifecycleManager{lookup: &sandbox.Instance{ID: "sbx-a"}}
	res, err := NewLifecycleService(nil, mgr).Destroy(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if !res.Success || res.Message != "sandbox record deleted" || mgr.purgedSandboxID != "sbx-a" || mgr.destroyCalled {
		t.Fatalf("unexpected destroy purge path: res=%+v mgr=%+v", res, mgr)
	}
}

func TestLifecycleServiceDestroyFallsBackToLegacyDestroy(t *testing.T) {
	t.Parallel()

	mgr := &fakeLifecycleManager{}
	res, err := NewLifecycleService(nil, mgr).Destroy(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if !res.Success || res.Message != "sandbox destroyed" || !mgr.destroyCalled {
		t.Fatalf("unexpected destroy fallback path: res=%+v mgr=%+v", res, mgr)
	}
}

func TestLifecycleServiceRecreateCopiesExistingConfig(t *testing.T) {
	t.Parallel()

	now := time.Now()
	mgr := &fakeLifecycleManager{
		config: &sandbox.InstanceConfig{
			Image:          "node:20",
			CPULimit:       2,
			MemoryMB:       2048,
			DiskMB:         4096,
			TimeoutSeconds: 60,
			NetworkMode:    sandbox.NetworkAllow,
			AllowedHosts:   []string{"example.com"},
			EnvVars:        map[string]string{"A": "B"},
		},
		recreate: &sandbox.Instance{ID: "sbx-new", Backend: "docker", Config: sandbox.InstanceConfig{Image: "node:20"}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	inst, err := NewLifecycleService(nil, mgr).Recreate(context.Background(), RecreateRequest{
		SandboxID: "sbx-old",
		SessionID: "sess-new",
		TenantID:  "tenant-a",
	})
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if inst.ID != "sbx-new" || mgr.recreateSessionID != "sess-new" || mgr.recreateTenantID != "tenant-a" {
		t.Fatalf("unexpected recreate result: inst=%+v mgr=%+v", inst, mgr)
	}
	if mgr.recreateConfig.Image != "node:20" || mgr.recreateConfig.MemoryMB != 2048 || mgr.recreateConfig.NetworkMode != "allow" {
		t.Fatalf("unexpected recreated config: %+v", mgr.recreateConfig)
	}
}

func TestLifecycleServiceRecreateValidatesInputs(t *testing.T) {
	t.Parallel()

	_, err := NewLifecycleService(nil, &fakeLifecycleManager{}).Recreate(context.Background(), RecreateRequest{SessionID: "sess-a", TenantID: "tenant-a"})
	if err == nil {
		t.Fatal("expected missing sandbox id error")
	}
	_, err = NewLifecycleService(nil, &fakeLifecycleManager{}).Recreate(context.Background(), RecreateRequest{SandboxID: "sbx-a", TenantID: "tenant-a"})
	if !errors.Is(err, ErrLifecycleSessionIDRequired) {
		t.Fatalf("expected missing session id error, got %v", err)
	}
}

func TestLifecycleServiceRequiresBackend(t *testing.T) {
	t.Parallel()

	_, err := NewLifecycleService(nil, nil).Stop(context.Background(), "sbx-a")
	if !errors.Is(err, ErrLifecycleNotConfigured) {
		t.Fatalf("expected not configured, got %v", err)
	}
}

type fakeLifecycleRepo struct {
	desiredID    string
	desiredState string
	row          sandboxlc.Row
}

func (r *fakeLifecycleRepo) SetDesiredState(_ context.Context, id, desired string) error {
	r.desiredID = id
	r.desiredState = desired
	return nil
}

func (r *fakeLifecycleRepo) GetByID(_ context.Context, _ string) (sandboxlc.Row, error) {
	return r.row, nil
}

type fakeLifecycleManager struct {
	stopCalled        bool
	terminateCalled   bool
	destroyCalled     bool
	purgedSandboxID   string
	revive            *sandbox.Instance
	lookup            *sandbox.Instance
	config            *sandbox.InstanceConfig
	recreate          *sandbox.Instance
	recreateSessionID string
	recreateTenantID  string
	recreateConfig    sandbox.SandboxConfig
}

func (m *fakeLifecycleManager) StopSandbox(_ context.Context, _ string) error {
	m.stopCalled = true
	return nil
}

func (m *fakeLifecycleManager) ReviveSandbox(_ context.Context, _ string) (*sandbox.Instance, error) {
	return m.revive, nil
}

func (m *fakeLifecycleManager) TerminateSandbox(_ context.Context, _ string) error {
	m.terminateCalled = true
	return nil
}

func (m *fakeLifecycleManager) LookupInstanceBySession(_ context.Context, _ string) (*sandbox.Instance, error) {
	return m.lookup, nil
}

func (m *fakeLifecycleManager) PurgeTerminatedSandbox(_ context.Context, sandboxID string) error {
	m.purgedSandboxID = sandboxID
	return nil
}

func (m *fakeLifecycleManager) Destroy(_ context.Context, _ string) error {
	m.destroyCalled = true
	return nil
}

func (m *fakeLifecycleManager) GetInstanceConfig(_ context.Context, _ string) (*sandbox.InstanceConfig, string, error) {
	if m.config == nil {
		return nil, "", errors.New("not found")
	}
	return m.config, "", nil
}

func (m *fakeLifecycleManager) GetOrCreate(_ context.Context, sessionID, tenantID string, config sandbox.SandboxConfig) (*sandbox.Instance, error) {
	m.recreateSessionID = sessionID
	m.recreateTenantID = tenantID
	m.recreateConfig = config
	return m.recreate, nil
}
