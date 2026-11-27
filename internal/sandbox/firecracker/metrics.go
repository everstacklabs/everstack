package firecracker

// Prometheus metrics for the firecracker backend. Registered via
// promauto at package init so the collectors show up in the default
// registry — services/firecracker-agent serves that registry on
// :9091/metrics.
//
// Label budget: state labels (provisioning/booting/healthy/...) are
// small and bounded (<10 cardinality). Per-VM IDs intentionally NOT
// in labels — that explodes cardinality on a busy host (1000s of
// VMs created+destroyed per day). When per-VM observability is
// needed, the log line at the transition is the source of truth.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// vmTransitions counts every successful state transition.
	// Useful for: detecting unusual churn (lots of healthy→unhealthy
	// = sick host or sick workload), monitoring boot success rate
	// (booting→healthy ÷ provisioning→booting), spotting cleanup
	// failures (terminating→terminated drift).
	vmTransitions = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "vm_transitions_total",
		Help:      "Count of validated VM lifecycle transitions. Labels: from, to.",
	}, []string{"from", "to"})

	// vmTransitionsRejected counts transition attempts the state
	// machine refused. Nonzero values indicate a code path calling
	// Transition() with the wrong intent — programmer bug, not a
	// race or expected condition.
	// guestDenyGuardOK is 1 while the last guest deny guard assert
	// succeeded and 0 while it did not. The agent demotes its readiness
	// after two consecutive failures (ready_gate in the agent); this
	// gauge is what the alert watches so a node provisioning without
	// the guard is visible before, and independently of, that demotion.
	guestDenyGuardOK = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "guest_deny_guard_ok",
		Help:      "1 when the guest private-destination deny guard was verified in place by the last assert, 0 otherwise.",
	})

	// guestDenyGuardAsserts counts every assert of the guard by outcome.
	guestDenyGuardAsserts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "guest_deny_guard_asserts_total",
		Help:      "Guest deny guard asserts (periodic and on VM create). Labels: outcome (ok, error).",
	}, []string{"outcome"})

	vmTransitionsRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "vm_transitions_rejected_total",
		Help:      "Count of rejected (invalid-edge) VM lifecycle transition attempts. Labels: from, to.",
	}, []string{"from", "to"})

	// healthProbeTotal counts every HTTP /health probe outcome.
	// Pass/fail ratio over a 5min window is the operator's primary
	// signal for "are the in-guest agents healthy across the fleet."
	healthProbeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "vm_health_probe_total",
		Help:      "Count of agent /health probes by outcome. Labels: result=pass|fail.",
	}, []string{"result"})

	// workdirReaperTotal — Phase 2 reconciler outcomes.
	workdirReaperTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "workdir_reaper_total",
		Help:      "Count of workdir entries by reconciler outcome. Labels: outcome=live|in_flight|reaped|error.",
	}, []string{"outcome"})

	// workdirBytesFreed cumulative bytes freed by the workdir
	// reconciler. Together with reaper outcome counters, this
	// surfaces the cost of leaked VMs: a sudden jump in
	// bytes_freed_total without a matching ops outage means the
	// reconciler caught a leak before users felt it.
	workdirBytesFreed = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "workdir_reaper_bytes_freed_total",
		Help:      "Total bytes freed by the workdir reconciler across all sweeps.",
	})
)

// recordTransition is the metric side of LifecycleState.Transition.
// Internal to the package so callers can't bypass — every transition
// goes through the validated path or doesn't get counted.
func recordTransition(from, to Lifecycle) {
	vmTransitions.WithLabelValues(from.String(), to.String()).Inc()
}

func recordTransitionRejected(from, to Lifecycle) {
	vmTransitionsRejected.WithLabelValues(from.String(), to.String()).Inc()
}

// recordHealthProbe is called from HealthMonitor for every tick. The
// caller passes ok = true when /health returned 204 within timeout.
func recordHealthProbe(ok bool) {
	if ok {
		healthProbeTotal.WithLabelValues("pass").Inc()
	} else {
		healthProbeTotal.WithLabelValues("fail").Inc()
	}
}

// recordWorkdirOutcome accumulates the reconciler's sweep results.
// Phase 2 currently logs the counts; here we expose them to the
// scrape endpoint so a dashboard can show "reaper activity over time"
// without log aggregation.
func recordWorkdirOutcome(outcome string, n int) {
	if n <= 0 {
		return
	}
	workdirReaperTotal.WithLabelValues(outcome).Add(float64(n))
}

func recordWorkdirBytesFreed(bytes int64) {
	if bytes <= 0 {
		return
	}
	workdirBytesFreed.Add(float64(bytes))
}

var (
	// rootfsGCFiles counts rootfs GC decisions that removed (or, in dry
	// run, would remove) a file, plus sweep and delete errors.
	rootfsGCFiles = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "rootfs_gc_files_total",
		Help:      "Rootfs GC outcomes. Labels: outcome=deleted|would_delete|error.",
	}, []string{"outcome"})

	rootfsGCBytesFreed = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "rootfs_gc_bytes_freed_total",
		Help:      "Allocated bytes of rootfs files deleted by the rootfs GC.",
	})

	// rootfsDirBytes is this environment's share of the VM store: the
	// allocated size of the files in FC_ROOTFS_DIR after the last sweep.
	// Reflinked extents shared with another env are counted in both.
	rootfsDirBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "everstack",
		Subsystem: "fcagent",
		Name:      "rootfs_dir_bytes",
		Help:      "Allocated bytes of the files in FC_ROOTFS_DIR at the last rootfs GC sweep.",
	})
)

// Pre-create every outcome series at 0 so increase() sees the first
// error; a labelled counter that appears already at 1 is invisible to it.
func init() {
	for _, outcome := range []string{"deleted", "would_delete", "error"} {
		rootfsGCFiles.WithLabelValues(outcome)
	}
}

func recordRootfsGCOutcome(outcome string, n int) {
	if n <= 0 {
		return
	}
	rootfsGCFiles.WithLabelValues(outcome).Add(float64(n))
}

func recordRootfsGCBytesFreed(bytes int64) {
	if bytes <= 0 {
		return
	}
	rootfsGCBytesFreed.Add(float64(bytes))
}

func setRootfsDirBytes(bytes int64) {
	rootfsDirBytes.Set(float64(bytes))
}
