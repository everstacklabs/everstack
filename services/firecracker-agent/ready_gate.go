package main

import (
	"log"
	"strings"
	"sync"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// readyGate combines the conditions that must all hold before this
// agent accepts VM work, and publishes the result to the interceptors'
// readiness flag and the gRPC health service. Today those conditions
// are the preflight (rootfs, kernel, binary, disk) and the guest deny
// guard: a node whose guard cannot be installed or verified must stop
// taking creates rather than keep logging while provisioning guests
// with a hole in their isolation.
type readyGate struct {
	ready  *readiness
	health *health.Server

	mu            sync.Mutex
	preflight     bool
	guard         bool
	guardFailures int // consecutive failed guard asserts
	published     bool
}

// guardFailureThreshold is how many consecutive failed guard asserts it
// takes to demote readiness. One failure is usually the xtables lock
// held by a concurrent VM create or kube-proxy sync and clears on the
// next tick; demoting on it would flap the node in and out of rotation.
// Two in a row (one minute at the 30s cadence) is a broken guard. A
// single success restores.
const guardFailureThreshold = 2

func newReadyGate(ready *readiness, health *health.Server) *readyGate {
	return &readyGate{ready: ready, health: health}
}

// setPreflight records the preflight result and republishes.
func (g *readyGate) setPreflight(ok bool) { g.set(&g.preflight, ok, "preflight") }

// reportGuard records one guest deny guard assert. Readiness drops only
// after guardFailureThreshold consecutive failures and returns on the
// first success.
func (g *readyGate) reportGuard(ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ok {
		g.guardFailures = 0
		if !g.guard {
			g.guard = true
			g.publishLocked("guest deny guard")
		}
		return
	}
	g.guardFailures++
	if g.guardFailures == 1 {
		log.Printf("firecracker-agent: WARN guest deny guard assert failed (1 of %d before demotion)", guardFailureThreshold)
	}
	if g.guardFailures >= guardFailureThreshold && g.guard {
		g.guard = false
		g.publishLocked("guest deny guard")
	}
}

func (g *readyGate) set(field *bool, ok bool, what string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if *field == ok {
		return
	}
	*field = ok
	g.publishLocked(what)
}

// isReady reports the combined state without side effects.
func (g *readyGate) isReady() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.preflight && g.guard
}

// notReadyReasonLocked names the gates that are still red.
func (g *readyGate) notReadyReasonLocked() string {
	var parts []string
	if !g.preflight {
		parts = append(parts, "preflight not yet passed")
	}
	if !g.guard {
		parts = append(parts, "guest deny guard not in place")
	}
	return strings.Join(parts, "; ")
}

func (g *readyGate) publishLocked(what string) {
	now := g.preflight && g.guard
	g.ready.SetReason(g.notReadyReasonLocked())
	if now == g.published {
		if !now {
			log.Printf("firecracker-agent: %s changed, still NOT_SERVING (preflight=%v guard=%v)", what, g.preflight, g.guard)
		}
		return
	}
	g.published = now
	g.ready.Set(now)
	status := healthpb.HealthCheckResponse_NOT_SERVING
	if now {
		status = healthpb.HealthCheckResponse_SERVING
	}
	g.health.SetServingStatus("", status)
	g.health.SetServingStatus(readinessName, status)
	if now {
		log.Printf("firecracker-agent: %s ok, all gates green, SERVING", what)
	} else {
		log.Printf("firecracker-agent: %s failed, demoted to NOT_SERVING (preflight=%v guard=%v)", what, g.preflight, g.guard)
	}
}
