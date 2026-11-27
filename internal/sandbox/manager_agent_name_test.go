package sandbox

import (
	"context"
	"testing"

	"golang.org/x/sync/semaphore"
)

type agentNameCaptureBackend struct {
	Backend
	config InstanceConfig
}

func (b *agentNameCaptureBackend) Name() string { return "capture" }

func (b *agentNameCaptureBackend) Create(_ context.Context, id string, config InstanceConfig) (*Instance, error) {
	b.config = config
	return &Instance{ID: id, Name: config.Name, Status: StatusRunning, Config: config}, nil
}

func TestGetOrCreateTrooperDropsReservedDisplayName(t *testing.T) {
	backend := &agentNameCaptureBackend{}
	m := &SandboxManager{
		backend:            backend,
		globalConfig:       GlobalSandboxConfig{DefaultImage: DefaultDevImage},
		instances:          map[string]*Instance{},
		instancesBySandbox: map[string]*Instance{},
		troopers:           map[string]*Instance{},
		createSem:          semaphore.NewWeighted(1),
	}
	cfg := DefaultSandboxConfig()
	cfg.Enabled = true
	cfg.Name = "wks_display-name"

	inst, err := m.GetOrCreateTrooper(context.Background(), "agent-123456789", "trp-agent-123456789", "tenant-a", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := backend.config.Name, "trooper-agent-12"; got != want {
		t.Fatalf("backend name = %q, want %q", got, want)
	}
	if inst.Name != backend.config.Name {
		t.Fatalf("instance name = %q, backend name = %q", inst.Name, backend.config.Name)
	}
}
