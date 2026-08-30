package v1

import (
	"database/sql"
	"testing"
	"time"

	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

func TestSandboxReadModelIsStalePending(t *testing.T) {
	oldPending := &agentsquery.SandboxInstanceReadModel{
		Status:    string(sandbox.StatusPending),
		CreatedAt: time.Now().Add(-11 * time.Minute).Format(time.RFC3339Nano),
		LifecycleState: sql.NullString{
			Valid:  true,
			String: "pending",
		},
	}
	if !sandboxReadModelIsStalePending(oldPending, 10*time.Minute) {
		t.Fatal("expected old pending sandbox row to be stale")
	}

	recentPending := &agentsquery.SandboxInstanceReadModel{
		Status:    string(sandbox.StatusPending),
		CreatedAt: time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano),
	}
	if sandboxReadModelIsStalePending(recentPending, 10*time.Minute) {
		t.Fatal("expected recent pending sandbox row to remain in-flight")
	}

	oldReviving := &agentsquery.SandboxInstanceReadModel{
		Status:    string(sandbox.StatusPending),
		CreatedAt: time.Now().Add(-11 * time.Minute).Format(time.RFC3339Nano),
		LifecycleState: sql.NullString{
			Valid:  true,
			String: sandbox.LifecycleReviving,
		},
	}
	if sandboxReadModelIsStalePending(oldReviving, 10*time.Minute) {
		t.Fatal("expected non-create lifecycle states to be left alone")
	}
}

func TestSandboxInstanceToProtoNormalizesPublicLifecycleState(t *testing.T) {
	inst := &sandbox.Instance{
		ID:             "sbx_live",
		Status:         sandbox.StatusStopped,
		LifecycleState: "sleeping",
		CreatedAt:      time.Now(),
		Backend:        "firecracker",
	}

	got := sandboxInstanceToProto(inst)
	if got.LifecycleState != sandbox.PublicLifecycleStopped {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, sandbox.PublicLifecycleStopped)
	}
	if got.Backend != "isolated" {
		t.Fatalf("Backend = %q, want isolated", got.Backend)
	}
}

func TestSandboxReadModelToProtoNormalizesPublicLifecycleState(t *testing.T) {
	rm := &agentsquery.SandboxInstanceReadModel{
		ID:        "sbx_read",
		Status:    string(sandbox.StatusPending),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
		LifecycleState: sql.NullString{
			Valid:  true,
			String: sandbox.LifecycleReviving,
		},
	}

	got := sandboxInstanceReadModelToProto(rm)
	if got.LifecycleState != sandbox.PublicLifecycleRestoring {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, sandbox.PublicLifecycleRestoring)
	}
}

func TestSandboxReadModelToProtoFallsBackToStatusLifecycleState(t *testing.T) {
	rm := &agentsquery.SandboxInstanceReadModel{
		ID:        "sbx_status",
		Status:    string(sandbox.StatusStopped),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}

	got := sandboxInstanceReadModelToProto(rm)
	if got.LifecycleState != sandbox.PublicLifecycleStopped {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, sandbox.PublicLifecycleStopped)
	}
}

func TestLifecycleRowToProtoNormalizesPublicLifecycleState(t *testing.T) {
	row := &sandboxlc.Row{
		ID:             "sbx_row",
		Status:         sandboxlc.StateTerminating,
		LifecycleState: sandboxlc.StateTerminating,
		CreatedAt:      time.Now(),
	}

	got := lifecycleRowToProto(row)
	if got.LifecycleState != sandbox.PublicLifecycleDeleting {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, sandbox.PublicLifecycleDeleting)
	}
	if got.Status != agentspb.SandboxStatus_SANDBOX_STATUS_STOPPED {
		t.Fatalf("Status = %v, want STOPPED", got.Status)
	}
}

func TestLifecycleInstanceToProtoNormalizesPublicLifecycleState(t *testing.T) {
	inst := &sandboxcp.LifecycleInstance{
		ID:             "sbx_lifecycle",
		Status:         string(sandbox.StatusPending),
		LifecycleState: sandbox.LifecycleReviving,
		CreatedAt:      time.Now(),
	}

	got := lifecycleInstanceToProto(inst)
	if got.LifecycleState != sandbox.PublicLifecycleRestoring {
		t.Fatalf("LifecycleState = %q, want %q", got.LifecycleState, sandbox.PublicLifecycleRestoring)
	}
	if got.Status != agentspb.SandboxStatus_SANDBOX_STATUS_PENDING {
		t.Fatalf("Status = %v, want PENDING", got.Status)
	}
}

func TestSandboxStatusFromStringTreatsStoppingAsPending(t *testing.T) {
	if got := sandboxStatusFromString("stopping"); got != agentspb.SandboxStatus_SANDBOX_STATUS_PENDING {
		t.Fatalf("Status = %v, want PENDING", got)
	}
}

func TestPublicSandboxEventStatusNormalizesInternalStates(t *testing.T) {
	tests := map[string]string{
		"stopping":    "pending",
		"reviving":    "pending",
		"restoring":   "pending",
		"archiving":   "stopped",
		"terminating": "stopped",
		"terminated":  "stopped",
		"deleted":     "stopped",
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			if got := publicSandboxEventStatus(raw); got != want {
				t.Fatalf("publicSandboxEventStatus(%q) = %q, want %q", raw, got, want)
			}
		})
	}
}
