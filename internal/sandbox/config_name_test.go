package sandbox

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sync/semaphore"
)

func TestValidateSandboxNameRule(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "frontend"},
		{name: "database"},
		{name: "postgres"},
		{name: "notebook"},
		{name: "AbCdEf23"},
		{name: "sbx_x", wantErr: true},
		{name: "WKS_x", wantErr: true},
		{name: "fn-runtime"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSandboxName(test.name)
			if errors.Is(err, ErrReservedSandboxName) != test.wantErr {
				t.Fatalf("ValidateSandboxName(%q) error = %v, wantErr = %v", test.name, err, test.wantErr)
			}
		})
	}
}

func TestAgentConfigDropsReservedSandboxNameAtProvisioning(t *testing.T) {
	cfg, err := ParseSandboxConfig(map[string]interface{}{
		"sandbox": map[string]interface{}{"enabled": true, "name": "sbx_stored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "sbx_stored" {
		t.Fatalf("parsed name = %q, want stored value", cfg.Name)
	}
	if got := DropReservedAgentSandboxName(cfg, "agent-1"); got.Name != "" {
		t.Fatalf("provisioning name = %q, want generated-name fallback", got.Name)
	}
}

func TestAgentConfigKeepsOrdinarySandboxName(t *testing.T) {
	cfg := DropReservedAgentSandboxName(SandboxConfig{Name: "notebook"}, "agent-1")
	if cfg.Name != "notebook" {
		t.Fatalf("provisioning name = %q, want notebook", cfg.Name)
	}
}

func TestGetOrCreateRejectsReservedSandboxName(t *testing.T) {
	m := &SandboxManager{
		backend:            &agentNameCaptureBackend{},
		instances:          map[string]*Instance{},
		instancesBySandbox: map[string]*Instance{},
		createSem:          semaphore.NewWeighted(1),
		registry:           NewLocalRegistry(),
	}
	_, err := m.GetOrCreate(context.Background(), "session-1", "tenant-a", SandboxConfig{Name: "sbx_reserved", Image: DefaultDevImage})
	if !errors.Is(err, ErrReservedSandboxName) {
		t.Fatalf("error = %v, want ErrReservedSandboxName", err)
	}
}
