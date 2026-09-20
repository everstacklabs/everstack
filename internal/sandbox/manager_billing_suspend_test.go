package sandbox

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// TestSuspendedStatusIsNotSleeping is the load-bearing invariant. Both wake
// paths test for exactly "sleeping": AgentMessageBus auto-wakes on message
// delivery, and the WakeAgent RPC accepts nothing else. If billing parked its
// agents there, a tenant refused compute could resume it by sending a message.
func TestSuspendedStatusIsNotSleeping(t *testing.T) {
	if LifecycleStatusSuspended == "sleeping" {
		t.Fatal("billing suspension must not reuse 'sleeping'; the wake paths would resurrect refused tenants")
	}
	if LifecycleStatusSuspended == "" {
		t.Fatal("suspended status must be a distinct non-empty value")
	}
}

func TestSuspendTenantAgentsSkipsSleepingAndTerminated(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	// Only live agents are parked. A sleeping agent holds no VM, so there is
	// no compute to stop, and overwriting it would lose the fact that the
	// reaper rather than billing put it down.
	mock.ExpectExec(`UPDATE agent_definitions[\s\S]*lifecycle_status IN \('created', 'idle', 'running', 'provisioning'\)`).
		WithArgs("tenant-a", LifecycleStatusSuspended).
		WillReturnResult(sqlmock.NewResult(0, 3))

	m := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	suspended, err := m.SuspendTenantAgents(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("SuspendTenantAgents() error = %v", err)
	}
	if suspended != 3 {
		t.Errorf("suspended = %d, want 3", suspended)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResumeTenantAgentsOnlyReleasesSuspended(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	// Scoped to the suspended status so resuming cannot drag a sleeping agent
	// back into the reconciler's working set and restart the reaper flap.
	mock.ExpectExec(`UPDATE agent_definitions[\s\S]*SET lifecycle_status = 'idle'[\s\S]*lifecycle_status = \$2`).
		WithArgs("tenant-a", LifecycleStatusSuspended).
		WillReturnResult(sqlmock.NewResult(0, 2))

	m := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	resumed, err := m.ResumeTenantAgents(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ResumeTenantAgents() error = %v", err)
	}
	if resumed != 2 {
		t.Errorf("resumed = %d, want 2", resumed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestSuspendAndResumeAreNoOpsWithoutTenant guards the enforcement loop against
// a blank tenant turning into a table-wide UPDATE across every organization.
func TestSuspendAndResumeAreNoOpsWithoutTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	m := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	for _, tenantID := range []string{"", "   "} {
		if n, err := m.SuspendTenantAgents(context.Background(), tenantID); n != 0 || err != nil {
			t.Errorf("SuspendTenantAgents(%q) = (%d, %v), want (0, nil)", tenantID, n, err)
		}
		if n, err := m.ResumeTenantAgents(context.Background(), tenantID); n != 0 || err != nil {
			t.Errorf("ResumeTenantAgents(%q) = (%d, %v), want (0, nil)", tenantID, n, err)
		}
	}
	// No statement should have reached the database at all.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestInstanceIsRunningTreatsLifecycleStateAsAuthoritative is the fix for the
// loop that never terminated. eu-gra-1-prod holds sandbox_instances written in
// May 2026 carrying lifecycle_state='terminated' with status='running'. The old
// selection ORed the two columns, so those rows were picked up as running,
// handed to StopSandbox, and silently no-opped: setDesiredStateSQL has no CASE
// arm leading out of a terminal state, so it leaves both columns untouched and
// still returns success because the row exists. They were then counted as
// stopped and selected again on the next tick, forever — which is what actually
// produced ~8,640 "stopped tenant compute" lines a day, not any resurrection.
func TestInstanceIsRunningTreatsLifecycleStateAsAuthoritative(t *testing.T) {
	for _, tc := range []struct {
		name           string
		lifecycleState string
		status         string
		want           bool
	}{
		{"the prod drift: terminal lifecycle, stale running status", "terminated", "running", false},
		{"genuinely running", "running", "running", true},
		{"lifecycle unset falls back to status", "", "running", true},
		{"lifecycle unset and not running", "", "stopped", false},
		{"stopped lifecycle with stale running status", "stopped", "running", false},
		{"running lifecycle with stale status", "running", "stopped", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := instanceIsRunning(tc.lifecycleState, tc.status); got != tc.want {
				t.Errorf("instanceIsRunning(%q, %q) = %v, want %v", tc.lifecycleState, tc.status, got, tc.want)
			}
		})
	}
}
