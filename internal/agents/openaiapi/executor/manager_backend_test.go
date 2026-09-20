package executor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

type scopedManagerBackend struct {
	sandbox.Backend
	mu     sync.Mutex
	config sandbox.InstanceConfig
}

func (*scopedManagerBackend) Name() string                                      { return "firecracker-agent" }
func (*scopedManagerBackend) List(context.Context) ([]*sandbox.Instance, error) { return nil, nil }
func (*scopedManagerBackend) Healthy(context.Context) error                     { return nil }
func (b *scopedManagerBackend) Create(_ context.Context, id string, cfg sandbox.InstanceConfig) (*sandbox.Instance, error) {
	b.mu.Lock()
	b.config = cfg
	b.mu.Unlock()
	return &sandbox.Instance{ID: id, ContainerID: "vm", Backend: b.Name(), Status: sandbox.StatusRunning, Config: cfg, CreatedAt: time.Now()}, nil
}

func TestManagerBackendUsesInstanceAsSandboxTenantScope(t *testing.T) {
	backend := &scopedManagerBackend{}
	manager := sandbox.NewManager(backend, sandbox.GlobalSandboxConfig{MaxSandboxes: 1})
	adapter := NewManagerBackend(manager)
	owner := Ownership{OrganizationID: "organization-a", InstanceID: "instance-a"}
	inst, err := adapter.Provision(context.Background(), owner, "openai_a", sandbox.SandboxConfig{Image: "executor"})
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	cfg := backend.config
	backend.mu.Unlock()
	if cfg.TenantID != owner.InstanceID || inst.InstanceID != owner.InstanceID {
		t.Fatalf("sandbox scope tenant=%q instance=%q, want %q", cfg.TenantID, inst.InstanceID, owner.InstanceID)
	}
	if cfg.TenantID == owner.OrganizationID {
		t.Fatal("organization ID leaked into sandbox tenant scope")
	}
}
