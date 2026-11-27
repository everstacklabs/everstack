package tools

import (
	"context"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

type agentSandboxNameBackend struct {
	sandbox.Backend
	created sandbox.InstanceConfig
}

func (*agentSandboxNameBackend) Name() string { return "agent-name-test" }

func (*agentSandboxNameBackend) List(context.Context) ([]*sandbox.Instance, error) {
	return nil, nil
}

func (b *agentSandboxNameBackend) Create(_ context.Context, id string, config sandbox.InstanceConfig) (*sandbox.Instance, error) {
	b.created = config
	return &sandbox.Instance{
		ID:        id,
		Name:      config.Name,
		Backend:   b.Name(),
		Status:    sandbox.StatusRunning,
		Config:    config,
		CreatedAt: time.Now(),
	}, nil
}

func (*agentSandboxNameBackend) Destroy(context.Context, string) error { return nil }

func (*agentSandboxNameBackend) Healthy(context.Context) error { return nil }

func (*agentSandboxNameBackend) Stats(context.Context, string) (*sandbox.ContainerStats, error) {
	return nil, nil
}

func TestEnsureSandboxDropsReservedStoredAgentName(t *testing.T) {
	backend := &agentSandboxNameBackend{}
	manager := sandbox.NewManager(backend, sandbox.GlobalSandboxConfig{
		MaxSandboxes: 4,
		DefaultImage: sandbox.DefaultDevImage,
	})
	t.Cleanup(func() {
		if err := manager.DestroyAll(context.Background()); err != nil {
			t.Errorf("manager cleanup: %v", err)
		}
	})
	cfg := sandbox.DefaultSandboxConfig()
	cfg.Enabled = true
	cfg.Name = "sbx_stored"
	sctx := &SandboxSessionContext{
		Manager:   manager,
		SessionID: "session-1",
		TenantID:  "tenant-a",
		AgentID:   "agent-1",
		Config:    cfg,
	}

	inst, err := ensureSandbox(context.Background(), sctx)
	if err != nil {
		t.Fatal(err)
	}
	if backend.created.Name == "" || backend.created.Name == "sbx_stored" {
		t.Fatalf("backend name = %q, want generated name", backend.created.Name)
	}
	if err := sandbox.ValidateSandboxName(backend.created.Name); err != nil {
		t.Fatalf("generated name %q: %v", backend.created.Name, err)
	}
	if inst.Name != backend.created.Name {
		t.Fatalf("instance name = %q, backend name = %q", inst.Name, backend.created.Name)
	}
}
