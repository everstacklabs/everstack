package lifecycle

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

type committedRow struct {
	Lifecycle      string         `db:"lifecycle_state"`
	Status         string         `db:"status"`
	Desired        string         `db:"desired_state"`
	ArchiveRef     sql.NullString `db:"workspace_archive_ref"`
	SnapshotRef    sql.NullString `db:"workspace_snapshot_ref"`
	ReconcileAfter time.Time      `db:"reconcile_after"`
	ErrorReason    sql.NullString `db:"error_reason"`
}

func loadCommitted(t *testing.T, repo *Repository, id string) committedRow {
	t.Helper()
	var c committedRow
	if err := repo.db.Get(&c, `SELECT lifecycle_state, status, desired_state, workspace_archive_ref,
		workspace_snapshot_ref, reconcile_after, error_reason FROM sandbox_instances WHERE id = $1`, id); err != nil {
		t.Fatalf("load row: %v", err)
	}
	return c
}

// A wake requested while the stop was uploading must not be dropped when
// the stop commits: the row goes straight to reviving and keeps the ref of
// the tarball the stop just uploaded.
func TestPGWakeRequestedDuringStopIsActedOn(t *testing.T) {
	db := pgscratch.New(t, "sandbox_pending_wake")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	ctx := context.Background()

	seedSandbox(t, repo, "sbx_w", sbxOrgA, StateStopping)
	if _, err := db.Exec(`UPDATE sandbox_instances SET desired_state = 'sleeping', reconcile_locked_by = 'leader-1' WHERE id = 'sbx_w'`); err != nil {
		t.Fatal(err)
	}
	// Claimed copy as Step saw it.
	claimed := newRow(StateStopping, DesireSleeping)
	claimed.ID = "sbx_w"

	// The user asks for a wake while ExecuteStop runs. Lifecycle is
	// stopping, so SetDesiredState only records the desire.
	if err := repo.SetDesiredState(ctx, "sbx_w", DesireRunning); err != nil {
		t.Fatal(err)
	}

	const ref = "objstore:v1:tenants/x/sandboxes/sbx_w/workspace/g.tar.gz#sha256=aa&size=1"
	next := claimed
	next.LifecycleState = StateSleeping
	next.Status = "stopped"
	next.WorkspaceArchiveRef = nullable(ref)
	next.ReconcileAfter = time.Now().Add(24 * time.Hour)
	if err := repo.ApplyTransition(ctx, "leader-1", next); err != nil {
		t.Fatalf("ApplyTransition: %v", err)
	}

	got := loadCommitted(t, repo, "sbx_w")
	if got.Lifecycle != StateReviving || got.Status != StateReviving || got.Desired != DesireRunning {
		t.Fatalf("committed (%s/%s desired %s), want reviving/reviving desired running", got.Lifecycle, got.Status, got.Desired)
	}
	if got.ArchiveRef.String != ref {
		t.Fatalf("archive ref = %v, want the just-uploaded %q", got.ArchiveRef, ref)
	}
	if time.Until(got.ReconcileAfter) > time.Minute {
		t.Fatalf("reconcile_after = %s, want due now so the wake runs immediately", got.ReconcileAfter)
	}
}

// Without a concurrent request the claimed desire stands: a stop commits
// sleeping and stays there.
func TestPGStopWithoutPendingRequestStaysSleeping(t *testing.T) {
	db := pgscratch.New(t, "sandbox_plain_stop")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)

	seedSandbox(t, repo, "sbx_s", sbxOrgA, StateStopping)
	if _, err := db.Exec(`UPDATE sandbox_instances SET desired_state = 'sleeping' WHERE id = 'sbx_s'`); err != nil {
		t.Fatal(err)
	}
	next := newRow(StateSleeping, DesireSleeping)
	next.ID = "sbx_s"
	next.Status = "stopped"
	next.ReconcileAfter = time.Now().Add(24 * time.Hour)
	if err := repo.ApplyTransition(context.Background(), "", next); err != nil {
		t.Fatal(err)
	}
	if got := loadCommitted(t, repo, "sbx_s"); got.Lifecycle != StateSleeping || got.Desired != DesireSleeping {
		t.Fatalf("committed (%s desired %s), want sleeping/sleeping", got.Lifecycle, got.Desired)
	}
}

// A refused sleep deliberately overrides desired_state to running and
// carries the customer notice.
func TestPGRefusedSleepOverridesDesiredWithNotice(t *testing.T) {
	db := pgscratch.New(t, "sandbox_refused_sleep")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)

	seedSandbox(t, repo, "sbx_r", sbxOrgA, StateStopping)
	if _, err := db.Exec(`UPDATE sandbox_instances SET desired_state = 'sleeping' WHERE id = 'sbx_r'`); err != nil {
		t.Fatal(err)
	}
	next := newRow(StateRunning, DesireRunning)
	next.ID = "sbx_r"
	next.OverrideDesired = true
	next.Notice = "too large"
	next.ReconcileAfter = time.Now().Add(24 * time.Hour)
	if err := repo.ApplyTransition(context.Background(), "", next); err != nil {
		t.Fatal(err)
	}
	got := loadCommitted(t, repo, "sbx_r")
	if got.Lifecycle != StateRunning || got.Desired != DesireRunning || got.ErrorReason.String != "too large" {
		t.Fatalf("committed %+v, want running/running with the notice", got)
	}
}

// A terminate requested during a create is no longer overwritten when the
// create commits; the row goes on to terminating.
func TestPGTerminateRequestedDuringCreateIsActedOn(t *testing.T) {
	db := pgscratch.New(t, "sandbox_pending_term")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	ctx := context.Background()

	seedSandbox(t, repo, "sbx_t", sbxOrgA, StateCreating)
	if err := repo.SetDesiredState(ctx, "sbx_t", DesireTerminated); err != nil {
		t.Fatal(err)
	}
	next := newRow(StateRunning, DesireRunning)
	next.ID = "sbx_t"
	next.ReconcileAfter = time.Now().Add(24 * time.Hour)
	if err := repo.ApplyTransition(ctx, "", next); err != nil {
		t.Fatal(err)
	}
	if got := loadCommitted(t, repo, "sbx_t"); got.Lifecycle != StateTerminating || got.Desired != DesireTerminated {
		t.Fatalf("committed (%s desired %s), want terminating/terminated", got.Lifecycle, got.Desired)
	}
}

func TestPGApplyTransitionLostLease(t *testing.T) {
	db := pgscratch.New(t, "sandbox_lost_lease")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	seedSandbox(t, repo, "sbx_l", sbxOrgA, StateStopping)
	if _, err := db.Exec(`UPDATE sandbox_instances SET reconcile_locked_by = 'other' WHERE id = 'sbx_l'`); err != nil {
		t.Fatal(err)
	}
	next := newRow(StateSleeping, DesireSleeping)
	next.ID = "sbx_l"
	if err := repo.ApplyTransition(context.Background(), "leader-1", next); err != ErrLeaseLost {
		t.Fatalf("err = %v, want ErrLeaseLost", err)
	}
}

func runIDOf(t *testing.T, repo *Repository, id string) (incarnation, run string) {
	t.Helper()
	if err := repo.db.QueryRow(`SELECT workspace_incarnation, workspace_run_id FROM sandbox_instances WHERE id = $1`, id).
		Scan(&incarnation, &run); err != nil {
		t.Fatal(err)
	}
	return incarnation, run
}

// Every start of a VM run gets a new run id, so a tarball written by this
// run's stop is never confused with the one the wake consumed. Parking or
// sleeping keeps the run id the stop wrote under.
func TestPGRunIDChangesOnlyWhenARunStarts(t *testing.T) {
	db := pgscratch.New(t, "sandbox_run_id")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	ctx := context.Background()

	seedSandbox(t, repo, "sbx_r", sbxOrgA, StateReviving)
	inc0, run0 := runIDOf(t, repo, "sbx_r")

	next := newRow(StateRunning, DesireRunning)
	next.ID = "sbx_r"
	next.ReconcileAfter = time.Now().Add(time.Hour)
	if err := repo.ApplyTransition(ctx, "", next); err != nil {
		t.Fatal(err)
	}
	inc1, run1 := runIDOf(t, repo, "sbx_r")
	if run1 == run0 || inc1 != inc0 {
		t.Fatalf("wake: run %s -> %s, incarnation %s -> %s; want a new run in the same incarnation", run0, run1, inc0, inc1)
	}

	// Parked running row: same run.
	if err := repo.ApplyTransition(ctx, "", next); err != nil {
		t.Fatal(err)
	}
	if _, run := runIDOf(t, repo, "sbx_r"); run != run1 {
		t.Fatal("re-applying running started a new run")
	}

	// Sleep keeps the run the stop uploaded under.
	if _, err := db.Exec(`UPDATE sandbox_instances SET lifecycle_state = 'stopping', desired_state = 'sleeping' WHERE id = 'sbx_r'`); err != nil {
		t.Fatal(err)
	}
	sleep := newRow(StateSleeping, DesireSleeping)
	sleep.ID = "sbx_r"
	if err := repo.ApplyTransition(ctx, "", sleep); err != nil {
		t.Fatal(err)
	}
	if _, run := runIDOf(t, repo, "sbx_r"); run != run1 {
		t.Fatal("sleeping changed the run id")
	}
}

// Terminating enqueues the incarnation's tarballs for durable deletion in
// the same transaction.
func TestPGTerminateEnqueuesWorkspacePurge(t *testing.T) {
	db := pgscratch.New(t, "sandbox_purge_enqueue")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	ctx := context.Background()

	seedSandbox(t, repo, "wks_agent", sbxOrgA, StateTerminating)
	inc, _ := runIDOf(t, repo, "wks_agent")
	next := newRow(StateTerminated, DesireTerminated)
	next.ID = "wks_agent"
	for i := 0; i < 2; i++ { // idempotent
		if err := repo.ApplyTransition(ctx, "", next); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var gotInc, gotTenant string
	if err := db.QueryRow(`SELECT COUNT(*), MAX(incarnation), MAX(tenant_id) FROM sandbox_workspace_purges WHERE sandbox_id = 'wks_agent'`).
		Scan(&n, &gotInc, &gotTenant); err != nil {
		t.Fatal(err)
	}
	if n != 1 || gotInc != inc || gotTenant != sbxOrgA {
		t.Fatalf("purges = %d (%s, %s), want one for incarnation %s", n, gotInc, gotTenant, inc)
	}
}
