package fcagent

import (
	"testing"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
)

// makeStaticDiscovery returns a Discovery wired with a static target
// list so eligibleTargets() can be exercised without DNS / dialing.
func makeStaticDiscovery(t *testing.T, targets ...string) *Discovery {
	t.Helper()
	return &Discovery{
		static:  append([]string(nil), targets...),
		targets: append([]string(nil), targets...),
	}
}

func TestLoadBalancer_NoHealthCache_ReturnsAllTargets(t *testing.T) {
	// No HealthCache wired → backward-compatible path: every target
	// is eligible, round-robin selects from the full list.
	disc := makeStaticDiscovery(t, "a:9090", "b:9090", "c:9090")
	lb := NewLoadBalancer(disc)

	got := lb.eligibleTargets()
	if len(got) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(got))
	}
}

func TestLoadBalancer_HealthCacheFiltersUnhealthy(t *testing.T) {
	disc := makeStaticDiscovery(t, "a:9090", "b:9090", "c:9090")
	now := time.Now()
	hc := &HealthCache{
		cache: map[string]*healthEntry{
			"a:9090": {health: &fcpb.NodeHealth{Healthy: true}, fetched: now},
			"b:9090": {health: &fcpb.NodeHealth{Healthy: false, Reason: "disk 92%>85%"}, fetched: now},
			"c:9090": {health: &fcpb.NodeHealth{Healthy: true}, fetched: now},
		},
		staleAfter: 30 * time.Second,
	}
	lb := NewLoadBalancerWithHealth(disc, hc)

	got := lb.eligibleTargets()
	if len(got) != 2 {
		t.Fatalf("expected 2 healthy targets, got %d: %v", len(got), got)
	}
	for _, g := range got {
		if g == "b:9090" {
			t.Fatalf("unhealthy target b:9090 leaked into eligible set: %v", got)
		}
	}
}

func TestLoadBalancer_AllDegraded_FallsBackToFullList(t *testing.T) {
	// When every target reports unhealthy, returning an empty list
	// would fail every create. We instead return the full list with
	// a loud log line, so traffic keeps flowing while the operator
	// investigates. Better one extra retry than total starvation.
	disc := makeStaticDiscovery(t, "a:9090", "b:9090")
	now := time.Now()
	hc := &HealthCache{
		cache: map[string]*healthEntry{
			"a:9090": {health: &fcpb.NodeHealth{Healthy: false}, fetched: now},
			"b:9090": {health: &fcpb.NodeHealth{Healthy: false}, fetched: now},
		},
		staleAfter: 30 * time.Second,
	}
	lb := NewLoadBalancerWithHealth(disc, hc)

	got := lb.eligibleTargets()
	if len(got) != 2 {
		t.Fatalf("all-degraded fallback should return full list, got %d: %v", len(got), got)
	}
}

func TestLoadBalancer_UnknownTargetTreatedAsHealthy(t *testing.T) {
	// Discovery has 3 targets; the cache only knows about 2 of them
	// (the third was just added and hasn't been polled yet). The
	// unknown one should pass the gate so it gets a chance to serve
	// traffic during the warm-up window.
	disc := makeStaticDiscovery(t, "a:9090", "b:9090", "c:9090")
	now := time.Now()
	hc := &HealthCache{
		cache: map[string]*healthEntry{
			"a:9090": {health: &fcpb.NodeHealth{Healthy: true}, fetched: now},
			"b:9090": {health: &fcpb.NodeHealth{Healthy: false}, fetched: now},
			// c:9090 intentionally missing
		},
		staleAfter: 30 * time.Second,
	}
	lb := NewLoadBalancerWithHealth(disc, hc)

	got := lb.eligibleTargets()
	if len(got) != 2 {
		t.Fatalf("expected 2 eligible (a + c), got %d: %v", len(got), got)
	}
	// Verify c:9090 (unknown) IS in the eligible list
	foundC := false
	for _, g := range got {
		if g == "c:9090" {
			foundC = true
			break
		}
	}
	if !foundC {
		t.Fatalf("unknown target c:9090 should default to healthy, got %v", got)
	}
}
