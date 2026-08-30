package sandbox

import (
	"context"
	"testing"
)

type namedBackend struct {
	Backend
	name string
}

func (b namedBackend) Name() string { return b.name }

type explicitCapabilitiesBackend struct {
	namedBackend
}

func (b explicitCapabilitiesBackend) RunnerCapabilities() RunnerCapabilities {
	return RunnerCapabilities{
		Target:    "custom-runner",
		Placement: RunnerPlacementCluster,
		Health:    RunnerHealthRemoteAgent,
		Features:  RunnerFeatures{GitImport: true},
	}
}

type portBackend struct {
	namedBackend
}

func (b portBackend) ExposePort(context.Context, string, int, string) (int, error) {
	return 0, nil
}

func (b portBackend) UnexposePort(context.Context, string, int) error {
	return nil
}

func (b portBackend) DetectListeningPorts(context.Context, string) ([]ListeningPort, error) {
	return nil, nil
}

func TestCapabilitiesForBackendExplicitProvider(t *testing.T) {
	caps := CapabilitiesForBackend(explicitCapabilitiesBackend{namedBackend: namedBackend{name: "ignored"}})
	if caps.Target != "custom-runner" {
		t.Fatalf("target = %q, want custom-runner", caps.Target)
	}
	if caps.Placement != RunnerPlacementCluster {
		t.Fatalf("placement = %q, want %q", caps.Placement, RunnerPlacementCluster)
	}
	if caps.Health != RunnerHealthRemoteAgent {
		t.Fatalf("health = %q, want %q", caps.Health, RunnerHealthRemoteAgent)
	}
	if !caps.Features.GitImport {
		t.Fatal("git import capability was not preserved")
	}
	if caps.Features.PortExposure {
		t.Fatal("unexpected port exposure capability")
	}
}

func TestCapabilitiesForBackendInterfaceFallback(t *testing.T) {
	caps := CapabilitiesForBackend(portBackend{namedBackend: namedBackend{name: "legacy"}})
	if caps.Target != "legacy" {
		t.Fatalf("target = %q, want legacy", caps.Target)
	}
	if !caps.Features.PortExposure {
		t.Fatal("port exposure capability was not inferred")
	}
	if !caps.Features.PortDetection {
		t.Fatal("port detection capability was not inferred")
	}
	if caps.Placement != RunnerPlacementUnknown {
		t.Fatalf("placement = %q, want unknown", caps.Placement)
	}
}

func TestCapabilitiesForBackendNil(t *testing.T) {
	caps := CapabilitiesForBackend(nil)
	if caps.Target != "" {
		t.Fatalf("target = %q, want empty", caps.Target)
	}
	if caps.Placement != RunnerPlacementUnknown {
		t.Fatalf("placement = %q, want unknown", caps.Placement)
	}
	if caps.Health != RunnerHealthUnknown {
		t.Fatalf("health = %q, want unknown", caps.Health)
	}
}
