package fcagent

import (
	"testing"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
)

func TestHealthCache_DefaultsToHealthyBeforeFirstRefresh(t *testing.T) {
	// Construct the cache directly with no entries — simulating the
	// state immediately after StartHealthCache returns but before the
	// first probe completes. The placement gate should NOT block
	// during this warm-up window.
	c := &HealthCache{
		cache:      map[string]*healthEntry{},
		staleAfter: 30 * time.Second,
	}
	if !c.IsHealthy("agent-1") {
		t.Fatal("unknown target should default to healthy during warm-up")
	}
}

func TestHealthCache_HealthyEntryPasses(t *testing.T) {
	c := &HealthCache{
		cache: map[string]*healthEntry{
			"agent-1": {
				health:  &fcpb.NodeHealth{Healthy: true},
				fetched: time.Now(),
			},
		},
		staleAfter: 30 * time.Second,
	}
	if !c.IsHealthy("agent-1") {
		t.Fatal("explicit healthy entry should be eligible")
	}
}

func TestHealthCache_UnhealthyEntryBlocks(t *testing.T) {
	c := &HealthCache{
		cache: map[string]*healthEntry{
			"agent-1": {
				health:  &fcpb.NodeHealth{Healthy: false, Reason: "disk 92%>85%"},
				fetched: time.Now(),
			},
		},
		staleAfter: 30 * time.Second,
	}
	if c.IsHealthy("agent-1") {
		t.Fatal("unhealthy entry should NOT be eligible")
	}
}

func TestHealthCache_StaleEntryBlocks(t *testing.T) {
	// Entry was healthy 5 minutes ago — that's way past stale and
	// suggests the target stopped responding. Should be blocked.
	c := &HealthCache{
		cache: map[string]*healthEntry{
			"agent-1": {
				health:  &fcpb.NodeHealth{Healthy: true},
				fetched: time.Now().Add(-5 * time.Minute),
			},
		},
		staleAfter: 30 * time.Second,
	}
	if c.IsHealthy("agent-1") {
		t.Fatal("stale entry should be blocked regardless of last status")
	}
}

func TestHealthCache_FailedProbeBlocksAfterStale(t *testing.T) {
	// Probe failed (nil health) but the fetched timestamp is fresh —
	// inside the stale window, IsHealthy treats nil as unhealthy
	// because there's nothing to call "alive". Either way blocked.
	c := &HealthCache{
		cache: map[string]*healthEntry{
			"agent-1": {
				health:  nil,
				fetched: time.Now(),
			},
		},
		staleAfter: 30 * time.Second,
	}
	if c.IsHealthy("agent-1") {
		t.Fatal("nil health (probe failed) should be blocked")
	}
}

func TestHealthCache_Snapshot(t *testing.T) {
	want := &fcpb.NodeHealth{Healthy: true, DiskPct: 42}
	c := &HealthCache{
		cache: map[string]*healthEntry{
			"agent-1": {health: want, fetched: time.Now()},
		},
	}
	got := c.Snapshot("agent-1")
	if got == nil || got.GetDiskPct() != 42 {
		t.Fatalf("snapshot mismatch: %+v", got)
	}
	if c.Snapshot("agent-unknown") != nil {
		t.Fatal("snapshot of unknown target should be nil")
	}
}
