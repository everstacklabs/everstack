package firecracker

// Per-VM health probing over the guest's HTTP control plane.
//
// Why HTTP-over-TAP instead of vsock for liveness:
//
// E2B, the most directly comparable production sandbox provider, probes
// envd over the guest's TAP IP at 20s × 100ms (packages/orchestrator/
// pkg/sandbox/checks.go in e2b-dev/infra). Liveness lives on its own
// transport — completely independent of the vsock channel that carries
// commands. This makes "vsock wedged" and "agent dead" two distinct,
// independently observable failure modes:
//
//   - HTTP probe succeeds, vsock fails → transport is broken, retry
//     vsock or quarantine the host. The agent itself is fine.
//   - HTTP probe fails, vsock works → the agent has hung or crashed;
//     destroy + recreate the VM.
//   - Both fail → the VM is gone (kernel panic, OOM); fail loud.
//
// Our previous architecture conflated all three behind a single vsock
// channel, which is why one wedged vsock socket cascaded into "every
// new sandbox is unusable" during the 2026-05-20 incident.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// healthProbePort is the port the in-guest sandbox-agent's HTTP control
// plane listens on. Hardcoded in lockstep with cmd/sandbox-agent/main.go
// (httpListenPort). Don't change one without the other.
const healthProbePort = 8080

// healthProbePath is the liveness endpoint. 204 No Content = alive.
const healthProbePath = "/health"

// healthProbeTimeout is the per-probe budget. 100ms is e2b's value —
// long enough that a healthy agent on a loaded node always answers,
// short enough that a hung agent flips state in one tick instead of
// holding the loop hostage waiting on TCP keepalive.
const healthProbeTimeout = 100 * time.Millisecond

// healthProbeInterval is how often the per-VM loop runs. 20s is e2b's
// value — balances responsiveness (an agent that dies is detected
// within one tick) against per-VM CPU cost at scale.
const healthProbeInterval = 20 * time.Second

// readinessProbeBudget caps the total wait when checking a freshly-
// booted VM for the first time. The agent isn't expected to be up
// at t=0 (it's racing against the network setup we just did via
// vsock), so we retry with exponential backoff until either /health
// answers 204 or we hit the budget.
const readinessProbeBudget = 10 * time.Second

// ProbeAgentHealth runs a single HTTP GET against the in-guest agent's
// /health endpoint. Returns nil iff the response is 204 No Content
// within healthProbeTimeout. Used both for one-shot readiness checks
// (during VM provisioning) and as the inner step of the continuous
// HealthMonitor loop.
func ProbeAgentHealth(ctx context.Context, guestIP string) error {
	if guestIP == "" {
		return errors.New("ProbeAgentHealth: empty guestIP")
	}

	probeCtx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()

	// Fresh client per probe — no connection pooling. Connection reuse
	// would mask "TCP works to a stale socket but the agent isn't
	// actually serving" (the kernel buffers a SYN-ACK on the LISTENing
	// socket even when the process is wedged). A fresh dial every tick
	// trades some byte overhead for honest "can I reach the agent right
	// now" semantics. Matches e2b.
	tr := &http.Transport{
		DisableKeepAlives: true,
		DialContext: (&net.Dialer{
			Timeout: healthProbeTimeout,
		}).DialContext,
	}
	client := &http.Client{Transport: tr, Timeout: healthProbeTimeout}

	url := fmt.Sprintf("http://%s:%d%s", guestIP, healthProbePort, healthProbePath)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("probe %s: unexpected status %d (want 204)", url, resp.StatusCode)
	}
	return nil
}

// WaitForAgentReady polls the in-guest agent's /health endpoint until
// it returns 204, or readinessProbeBudget elapses. Used during VM
// provisioning to gate "VM is healthy" on the agent actually answering
// — replacing the vsock-Exec-based self-test that produced false
// failures whenever vsock wedged.
//
// Returns nil on first successful probe. Otherwise wraps the last
// probe error.
func WaitForAgentReady(ctx context.Context, guestIP string) error {
	deadline := time.Now().Add(readinessProbeBudget)
	backoff := 100 * time.Millisecond
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ProbeAgentHealth(ctx, guestIP); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agent did not become ready within %s: %w", readinessProbeBudget, lastErr)
		}
		// Capped exponential — 100ms, 200ms, 400ms, 800ms, then 1s.
		// Keeps the probe responsive at startup but doesn't hammer
		// the agent if it needs a moment.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < time.Second {
			backoff *= 2
			if backoff > time.Second {
				backoff = time.Second
			}
		}
	}
}

// HealthMonitor runs a continuous liveness loop for one VM. Started
// when the VM is marked ready, stopped when it's destroyed.
//
// State is held in an atomic bool so callers can read "healthy?"
// without taking the monitor's internal lock. Edge transitions
// (healthy→unhealthy, unhealthy→healthy) are logged exactly once at
// the edge — not on every tick — so logs don't drown the actual
// signal during a long outage.
type HealthMonitor struct {
	vmID     string
	guestIP  string
	interval time.Duration
	timeout  time.Duration

	healthy atomic.Bool
	cancel  context.CancelFunc
	done    chan struct{}

	// guards transitions so the edge log fires exactly once per edge,
	// even when a probe completes concurrently with Stop().
	mu sync.Mutex

	// transitionHook is called on every healthy↔unhealthy edge so
	// callers (the MicroVM's Lifecycle state machine) can mirror the
	// flip without having to poll IsHealthy. The bool arg is the
	// NEW state. Called with the monitor's mu held — keep it cheap
	// and don't re-enter HealthMonitor methods inside the hook.
	transitionHook func(healthy bool)
}

// NewHealthMonitor builds a monitor without starting it. The caller
// invokes Start to spin up the loop. Defaults match the e2b values
// (20s × 100ms); override is via package-level constants for now —
// per-VM tuning becomes a follow-up when we have data showing it
// matters.
func NewHealthMonitor(vmID, guestIP string) *HealthMonitor {
	m := &HealthMonitor{
		vmID:     vmID,
		guestIP:  guestIP,
		interval: healthProbeInterval,
		timeout:  healthProbeTimeout,
		done:     make(chan struct{}),
	}
	// Initial state is healthy — the caller only constructs the
	// monitor after WaitForAgentReady has succeeded, so the first
	// tick failing should be treated as a transition.
	m.healthy.Store(true)
	return m
}

// Start spins up the polling goroutine. Idempotent: calling Start
// twice on the same monitor logs and returns the existing one's
// state without leaking goroutines.
func (m *HealthMonitor) Start(parent context.Context) {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		logger.WithFields("vm_id", m.vmID).
			Warn("firecracker_health: Start called twice; ignoring")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.mu.Unlock()

	go m.run(ctx)
}

func (m *HealthMonitor) run(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	logger.WithFields("vm_id", m.vmID, "guest_ip", m.guestIP, "interval", m.interval.String()).
		Info("firecracker_health: monitor started")

	for {
		select {
		case <-ctx.Done():
			logger.WithFields("vm_id", m.vmID).
				Info("firecracker_health: monitor stopped")
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

// tick runs one probe and records any edge transition. Pulled out so
// tests can call it directly without spinning up the goroutine loop.
func (m *HealthMonitor) tick(ctx context.Context) {
	err := ProbeAgentHealth(ctx, m.guestIP)
	recordHealthProbe(err == nil)

	m.mu.Lock()
	defer m.mu.Unlock()

	wasHealthy := m.healthy.Load()
	isHealthy := err == nil

	if wasHealthy == isHealthy {
		return // no edge, no log
	}

	m.healthy.Store(isHealthy)
	if isHealthy {
		logger.WithFields("vm_id", m.vmID, "guest_ip", m.guestIP).
			Info("firecracker_health: agent recovered")
	} else {
		logger.WithFields("vm_id", m.vmID, "guest_ip", m.guestIP, "error", err.Error()).
			Warn("firecracker_health: agent unhealthy")
	}
	if m.transitionHook != nil {
		m.transitionHook(isHealthy)
	}
}

// SetTransitionHook registers a callback fired on every healthy↔
// unhealthy edge transition. Wired by the MicroVM at Start time so
// state-machine transitions and metric counters stay in lockstep
// with the underlying probe signal.
func (m *HealthMonitor) SetTransitionHook(hook func(healthy bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transitionHook = hook
}

// IsHealthy returns the latest known health state. Lock-free read
// suitable for hot paths (e.g. scheduler placement decisions, lifecycle
// gates). May lag the actual VM state by up to one tick interval.
func (m *HealthMonitor) IsHealthy() bool {
	return m.healthy.Load()
}

// Stop cancels the polling goroutine and blocks until it has exited.
// Idempotent and safe to call from any goroutine.
func (m *HealthMonitor) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	<-m.done
}
