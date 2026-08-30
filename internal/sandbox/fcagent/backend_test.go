package fcagent

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestSeedRouteRestoresRouteTable(t *testing.T) {
	b := &FCAgentBackend{routes: make(map[string]string)}

	b.SeedRoute("wks-agent", "10.0.0.7:9090")

	b.mu.RLock()
	got := b.routes["wks-agent"]
	b.mu.RUnlock()
	if got != "10.0.0.7:9090" {
		t.Fatalf("SeedRoute target = %q, want %q", got, "10.0.0.7:9090")
	}
}

func TestSeedRouteIgnoresEmptyValues(t *testing.T) {
	b := &FCAgentBackend{routes: make(map[string]string)}

	b.SeedRoute("", "10.0.0.7:9090")
	b.SeedRoute("wks-agent", "")

	if len(b.routes) != 0 {
		t.Fatalf("SeedRoute stored empty route values: %#v", b.routes)
	}
}

func TestRouteForMissingRouteReturnsTypedError(t *testing.T) {
	b := &FCAgentBackend{
		discovery: &Discovery{},
		routes:    make(map[string]string),
	}

	_, _, err := b.routeFor(context.Background(), "wks-missing")
	if !errors.Is(err, sandbox.ErrSandboxRouteMissing) {
		t.Fatalf("routeFor err = %v, want ErrSandboxRouteMissing", err)
	}
}

func TestBackendTargetUsesOwningAgentHostAndExposedPort(t *testing.T) {
	b := &FCAgentBackend{
		portMappings: map[string]map[int]*remotePortMapping{
			"sbx-1": {
				3000: {Target: "10.0.0.7:9090", HostPort: 41000, Protocol: "tcp"},
			},
		},
	}

	got, err := b.BackendTarget(context.Background(), "sbx-1", 3000)
	if err != nil {
		t.Fatalf("BackendTarget error: %v", err)
	}
	if got != "10.0.0.7:41000" {
		t.Fatalf("BackendTarget = %q, want 10.0.0.7:41000", got)
	}
}

func TestBackendTargetMissingMapping(t *testing.T) {
	b := &FCAgentBackend{portMappings: make(map[string]map[int]*remotePortMapping)}

	if _, err := b.BackendTarget(context.Background(), "missing", 3000); err == nil {
		t.Fatal("BackendTarget missing mapping expected error")
	}
}
