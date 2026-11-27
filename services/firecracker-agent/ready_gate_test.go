package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func healthStatus(t *testing.T, h *health.Server, name string) healthpb.HealthCheckResponse_ServingStatus {
	t.Helper()
	resp, err := h.Check(context.Background(), &healthpb.HealthCheckRequest{Service: name})
	if err != nil {
		t.Fatalf("health check %q: %v", name, err)
	}
	return resp.GetStatus()
}

func TestReadyGate_RequiresPreflightAndGuard(t *testing.T) {
	ready := &readiness{}
	h := health.NewServer()
	h.SetServingStatus(readinessName, healthpb.HealthCheckResponse_NOT_SERVING)
	g := newReadyGate(ready, h)

	if ready.IsReady() || g.isReady() {
		t.Fatal("must start not ready")
	}

	g.setPreflight(true)
	if ready.IsReady() {
		t.Fatal("preflight alone must not make the agent ready; the guest deny guard is not in place")
	}
	// Before the guard was ever verified, failures do not need to
	// accumulate: the agent simply stays not ready.
	g.reportGuard(false)
	if ready.IsReady() {
		t.Fatal("must stay not ready while the guard has never been verified")
	}
	if got := healthStatus(t, h, readinessName); got != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("readiness health = %v, want NOT_SERVING", got)
	}

	g.reportGuard(true)
	if !ready.IsReady() || !g.isReady() {
		t.Fatal("preflight + guard must make the agent ready")
	}
	if got := healthStatus(t, h, readinessName); got != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("readiness health = %v, want SERVING", got)
	}

	// One failed assert (an xtables lock race, say) is tolerated.
	g.reportGuard(false)
	if !ready.IsReady() {
		t.Fatal("a single failed guard assert must not demote the agent")
	}
	// A success in between resets the count.
	g.reportGuard(true)
	g.reportGuard(false)
	if !ready.IsReady() {
		t.Fatal("non-consecutive failures must not demote the agent")
	}

	// Two consecutive failures demote, regardless of preflight.
	g.reportGuard(false)
	if ready.IsReady() {
		t.Fatal("two consecutive failed guard asserts must demote the agent")
	}
	if got := healthStatus(t, h, ""); got != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("overall health = %v, want NOT_SERVING", got)
	}
	// Further failures keep it demoted without flapping.
	g.reportGuard(false)
	if ready.IsReady() {
		t.Fatal("still demoted")
	}

	// A single success restores; repeated identical reports are no-ops.
	g.reportGuard(true)
	g.reportGuard(true)
	g.setPreflight(true)
	if !ready.IsReady() {
		t.Fatal("recovered guard must restore readiness")
	}

	// Losing preflight demotes too.
	g.setPreflight(false)
	if ready.IsReady() {
		t.Fatal("a failed preflight must demote the agent")
	}
}

// A rejected call names the gate that is red, not always "preflight".
func TestReadyGate_RejectionNamesTheRedGate(t *testing.T) {
	ready := &readiness{}
	h := health.NewServer()
	g := newReadyGate(ready, h)

	if got := ready.notReadyError().Error(); !strings.Contains(got, "preflight not yet passed") {
		t.Fatalf("before any gate reports: %q, want the preflight reason", got)
	}
	g.setPreflight(true)
	got := ready.notReadyError().Error()
	if !strings.Contains(got, "guest deny guard not in place") || strings.Contains(got, "preflight") {
		t.Fatalf("preflight green, guard red: %q, want only the guard reason", got)
	}
	g.reportGuard(true)
	g.setPreflight(false)
	got = ready.notReadyError().Error()
	if !strings.Contains(got, "preflight not yet passed") || strings.Contains(got, "guard") {
		t.Fatalf("guard green, preflight red: %q, want only the preflight reason", got)
	}
}
