package firecracker

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestLifecycle_ValidHappyPath(t *testing.T) {
	// The canonical lifecycle: provisioning → booting → healthy → terminating
	// → terminated. Every transition should succeed and report the new state.
	var s LifecycleState
	s.Init(LifecycleProvisioning)

	steps := []Lifecycle{
		LifecycleBooting,
		LifecycleHealthy,
		LifecycleTerminating,
		LifecycleTerminated,
	}
	for _, to := range steps {
		got, ok := s.Transition("vm-1", to)
		if !ok {
			t.Fatalf("transition to %s rejected from %s", to, s.Load())
		}
		if got != to {
			t.Fatalf("transition returned %s, want %s", got, to)
		}
	}
}

func TestLifecycle_HealthyUnhealthyRoundTrip(t *testing.T) {
	// The HealthMonitor edge-bridge path: a VM bounces between
	// Healthy and Unhealthy as the in-guest agent comes back online.
	// Both directions are valid; the validation table needs to allow
	// each.
	var s LifecycleState
	s.Init(LifecycleHealthy)

	for i := 0; i < 3; i++ {
		if _, ok := s.Transition("vm-1", LifecycleUnhealthy); !ok {
			t.Fatalf("iter %d: healthy → unhealthy rejected", i)
		}
		if _, ok := s.Transition("vm-1", LifecycleHealthy); !ok {
			t.Fatalf("iter %d: unhealthy → healthy rejected", i)
		}
	}
}

func TestLifecycle_TerminateFromAnyLive(t *testing.T) {
	// Operator-initiated destroy must work from any non-terminal
	// state. Verified against each in-flight state.
	for _, from := range []Lifecycle{
		LifecycleProvisioning,
		LifecycleBooting,
		LifecycleHealthy,
		LifecycleUnhealthy,
	} {
		t.Run(from.String(), func(t *testing.T) {
			var s LifecycleState
			s.Init(from)
			if _, ok := s.Transition("vm-1", LifecycleTerminating); !ok {
				t.Fatalf("terminate from %s rejected", from)
			}
		})
	}
}

func TestLifecycle_RejectInvalidEdge(t *testing.T) {
	// Provisioning shouldn't skip straight to Healthy — booting is
	// the gate that confirms the firecracker process started, and
	// skipping it would mean the metric "we never observed boot" is
	// undercounted.
	var s LifecycleState
	s.Init(LifecycleProvisioning)
	if _, ok := s.Transition("vm-1", LifecycleHealthy); ok {
		t.Fatal("provisioning → healthy must be rejected (skips booting)")
	}
	if s.Load() != LifecycleProvisioning {
		t.Fatalf("rejected transition modified state: now %s", s.Load())
	}
}

func TestLifecycle_RejectFromTerminated(t *testing.T) {
	// Terminated is a sink — nothing can move out of it. A leftover
	// reference to a destroyed VM that tries to update state should
	// fail loudly (metric counter increments, log emitted).
	var s LifecycleState
	s.Init(LifecycleTerminated)
	for _, to := range []Lifecycle{
		LifecycleProvisioning,
		LifecycleBooting,
		LifecycleHealthy,
		LifecycleTerminating,
	} {
		if _, ok := s.Transition("vm-1", to); ok {
			t.Fatalf("terminated → %s should be rejected", to)
		}
	}
}

func TestLifecycle_MustTransitionPanicsOnReject(t *testing.T) {
	var s LifecycleState
	s.Init(LifecycleProvisioning)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustTransition with invalid edge should panic")
		}
	}()
	s.MustTransition("vm-1", LifecycleHealthy)
}

func TestMetrics_TransitionsCounterIncrements(t *testing.T) {
	// Reset by reading current value, then make a transition, then
	// verify the counter advanced by 1.
	before := counterValue(t, vmTransitions.WithLabelValues("booting", "healthy"))

	var s LifecycleState
	s.Init(LifecycleBooting)
	if _, ok := s.Transition("vm-metrics", LifecycleHealthy); !ok {
		t.Fatal("transition unexpectedly rejected")
	}
	after := counterValue(t, vmTransitions.WithLabelValues("booting", "healthy"))
	if after-before != 1 {
		t.Fatalf("transitions counter did not advance: before=%g after=%g", before, after)
	}
}

func TestMetrics_RejectedCounterIncrements(t *testing.T) {
	before := counterValue(t, vmTransitionsRejected.WithLabelValues("terminated", "healthy"))

	var s LifecycleState
	s.Init(LifecycleTerminated)
	_, _ = s.Transition("vm-metrics", LifecycleHealthy) // rejected

	after := counterValue(t, vmTransitionsRejected.WithLabelValues("terminated", "healthy"))
	if after-before != 1 {
		t.Fatalf("rejected counter did not advance: before=%g after=%g", before, after)
	}
}

func TestMetrics_HealthProbeCounters(t *testing.T) {
	passBefore := counterValue(t, healthProbeTotal.WithLabelValues("pass"))
	failBefore := counterValue(t, healthProbeTotal.WithLabelValues("fail"))

	recordHealthProbe(true)
	recordHealthProbe(false)
	recordHealthProbe(true)

	if got := counterValue(t, healthProbeTotal.WithLabelValues("pass")); got-passBefore != 2 {
		t.Fatalf("pass counter delta = %g, want 2", got-passBefore)
	}
	if got := counterValue(t, healthProbeTotal.WithLabelValues("fail")); got-failBefore != 1 {
		t.Fatalf("fail counter delta = %g, want 1", got-failBefore)
	}
}

func TestMetrics_WorkdirOutcomes(t *testing.T) {
	reapedBefore := counterValue(t, workdirReaperTotal.WithLabelValues("reaped"))
	bytesBefore := counterValue(t, workdirBytesFreed)

	recordWorkdirOutcome("reaped", 3)
	recordWorkdirOutcome("reaped", 2)
	recordWorkdirBytesFreed(4096)

	if got := counterValue(t, workdirReaperTotal.WithLabelValues("reaped")); got-reapedBefore != 5 {
		t.Fatalf("reaped delta = %g, want 5", got-reapedBefore)
	}
	if got := counterValue(t, workdirBytesFreed); got-bytesBefore != 4096 {
		t.Fatalf("bytes_freed delta = %g, want 4096", got-bytesBefore)
	}

	// recordWorkdirOutcome with n <= 0 is a no-op so callers can
	// hand in fresh zero-result sweeps without skewing counters.
	before := counterValue(t, workdirReaperTotal.WithLabelValues("reaped"))
	recordWorkdirOutcome("reaped", 0)
	recordWorkdirOutcome("reaped", -1)
	if after := counterValue(t, workdirReaperTotal.WithLabelValues("reaped")); after != before {
		t.Fatalf("zero/negative input affected counter: before=%g after=%g", before, after)
	}
}

// counterValue extracts the current float64 value from a Prometheus
// Counter for assertion in tests. The client_golang library doesn't
// expose this directly — collecting through a DTO is the canonical
// way.
func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("counter Write: %v", err)
	}
	if m.Counter == nil {
		t.Fatal("counter Metric has nil Counter field")
	}
	return m.Counter.GetValue()
}
