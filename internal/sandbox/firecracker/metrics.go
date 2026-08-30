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
