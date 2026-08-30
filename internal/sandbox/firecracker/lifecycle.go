package firecracker

// Per-VM lifecycle state machine. Centralizes what was previously a
// scattered set of vm.Status writes + the Phase 1 vm.Health.healthy
// atomic bool — both of which observed VM state but neither owned it.
//
// Why this matters for reliability: when state changes were ad-hoc,
// "destroyed but tap still attached" and "healthy but agent down" and
// "creating but firecracker process already gone" all looked identical
// at the API boundary — a vm in the map with vm.Status="running".
// Centralizing transitions through a validated state machine forces
// every code path to declare its intent (Transition(from, to)) and
// produces a single audit trail of state changes per VM.
//
// State diagram:
//
//	     LifecycleProvisioning ── create errors ──┐
//	            │                                  │
//	            ▼                                  ▼
//	      LifecycleBooting ──────────────────► LifecycleTerminating
//	            │       ▲                          │       ▲
//	            ▼       │ recover                  ▼       │
//	      LifecycleHealthy ──unhealthy──► LifecycleUnhealthy
//	            │       ▲       │
//	            │       └──recover──┐
//	            ▼                   ▼
//	      LifecycleTerminating ──► LifecycleTerminated
//
// Edge cases worth noting:
//   - Booting → Terminating is allowed (operator-initiated cancel
//     during a slow create).
//   - Unhealthy → Healthy is allowed (transient probe failure recovered).
//   - Any state → Terminating is allowed (forced destroy from
//     anywhere is legitimate).
//   - No transition out of Terminated — the VM is gone, the struct
//     should be dropped from the backend map shortly after.

import (
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Lifecycle is an enum over the per-VM states. Stored as an atomic
// int32 on MicroVM so reads from any goroutine (scheduler, monitor,
// destroy path) are lock-free.
type Lifecycle int32

const (
	// LifecycleUnknown is the zero value — used only briefly during
	// struct construction before the first explicit transition.
	LifecycleUnknown Lifecycle = iota

	// LifecycleProvisioning covers the steps between CreateVM start
	// and the moment InstanceStart is sent: rootfs overlay, vsock
	// socket, network (TAP + iptables + DNS proxy). Failure during
	// this window goes directly to Terminating without ever seeing
	// the guest.
	LifecycleProvisioning

	// LifecycleBooting is "InstanceStart sent, agent hasn't answered
	// /health yet." The vsock guest agent is being seeded (resolv.conf,
	// eth0 up, route insert) and the readiness probe is running.
	LifecycleBooting

	// LifecycleHealthy is the steady state — the in-guest agent's
	// /health endpoint returned 204 at least once and the
	// HealthMonitor's last tick agreed.
	LifecycleHealthy

	// LifecycleUnhealthy is "VM is up but the agent stopped
	// responding." Scheduler treats this like terminated for new
	// placement decisions, but the VM isn't destroyed automatically —
	// it might recover, and forcibly destroying loses user state.
	LifecycleUnhealthy

	// LifecycleTerminating is "Destroy() in flight." All command
	// RPCs against the VM return an error from this state.
	LifecycleTerminating

	// LifecycleTerminated is "VM is gone, work dir removed." Reachable
	// only from Terminating after successful cleanup. The struct
	// should be dropped from b.vms shortly after.
	LifecycleTerminated
)

// String returns the lowercase canonical name. Used as a metric label
// and in structured logs.
func (l Lifecycle) String() string {
	switch l {
	case LifecycleProvisioning:
		return "provisioning"
	case LifecycleBooting:
		return "booting"
	case LifecycleHealthy:
		return "healthy"
	case LifecycleUnhealthy:
		return "unhealthy"
	case LifecycleTerminating:
		return "terminating"
	case LifecycleTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

// validTransitions encodes the allowed edges in the state graph.
// Any transition not present here is rejected by Transition(), which
// surfaces in the log line as "rejected lifecycle transition" and
// stays as a counter in metrics — a useful breadcrumb when a code
// path is calling Transition with the wrong intent.
var validTransitions = map[Lifecycle]map[Lifecycle]bool{
	LifecycleUnknown: {
		LifecycleProvisioning: true,
	},
	LifecycleProvisioning: {
		LifecycleBooting:     true,
		LifecycleTerminating: true,
	},
	LifecycleBooting: {
		LifecycleHealthy:     true,
		LifecycleUnhealthy:   true,
		LifecycleTerminating: true,
	},
	LifecycleHealthy: {
		LifecycleUnhealthy:   true,
		LifecycleTerminating: true,
	},
	LifecycleUnhealthy: {
		LifecycleHealthy:     true,
		LifecycleTerminating: true,
	},
	LifecycleTerminating: {
		LifecycleTerminated: true,
	},
	// LifecycleTerminated: no outbound edges
}

// LifecycleState is the atomic-protected state container embedded in
// MicroVM. Held by value, accessed via methods so callers can't
// accidentally bypass the transition validation.
type LifecycleState struct {
	state atomic.Int32 // stores Lifecycle
}

// Load returns the current state. Lock-free, monotonic-consistent.
// May return Unknown for a freshly-constructed struct that hasn't
// been initialized.
func (s *LifecycleState) Load() Lifecycle {
	return Lifecycle(s.state.Load())
}

// Init sets the initial state without going through transition
// validation. Use only at struct construction time — once the VM
// has been observed by anything else (scheduler, monitor, RPC), all
// further changes must go through Transition.
func (s *LifecycleState) Init(initial Lifecycle) {
	s.state.Store(int32(initial))
}

// Transition attempts to move from the current state to `to`. Returns
// (current, ok). When ok=true the CAS succeeded; current==to.
// When ok=false the transition was rejected — either current is not
// the state the caller assumed, or the from→to edge isn't valid.
//
// Edge transitions are logged at Info level and increment the
// transitions counter. Rejections are logged at Warn and increment
// the rejections counter. Both produce enough breadcrumb for an
// operator to trace what code path tried what.
//
// vmID is included in logs but kept out of high-cardinality metric
// labels — only the from/to states get labeled.
func (s *LifecycleState) Transition(vmID string, to Lifecycle) (Lifecycle, bool) {
	for {
		from := s.Load()
		allowed, ok := validTransitions[from]
		if !ok || !allowed[to] {
			logger.WithFields("vm_id", vmID, "from", from.String(), "to", to.String()).
				Warn("firecracker_lifecycle: rejected transition (invalid edge)")
			recordTransitionRejected(from, to)
			return from, false
		}
		if s.state.CompareAndSwap(int32(from), int32(to)) {
			logger.WithFields("vm_id", vmID, "from", from.String(), "to", to.String()).
				Info("firecracker_lifecycle: transition")
			recordTransition(from, to)
			return to, true
		}
		// CAS failed → someone else moved the state between our
		// Load and Swap. Re-read and retry the validation — the
		// new "from" might still allow the requested "to" (e.g.
		// Booting → Healthy raced against Booting → Healthy:
		// the second caller sees Healthy → Healthy which is a
		// no-op rejection, fine).
	}
}

// MustTransition is Transition with a panic on rejection. Use only in
// code paths where rejection indicates a programmer error (not a race
// or operator-initiated state change). The panic surfaces immediately
// in tests rather than silently producing a half-migrated VM.
func (s *LifecycleState) MustTransition(vmID string, to Lifecycle) {
	if _, ok := s.Transition(vmID, to); !ok {
		panic("firecracker_lifecycle: must-transition rejected: " + s.Load().String() + " → " + to.String())
	}
}

// SetTransitionMetricsTime is an extension hook for future state-
// duration histograms. Phase 4 ships the counters; per-state duration
// requires a "when did we enter this state" timestamp that lives on
// the struct. Added in shape, not yet wired, so the field type stays
// stable when Phase 4b lands. Lint: keep _ assignment until use site
// exists.
var _ = time.Now
