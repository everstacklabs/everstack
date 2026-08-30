package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	runtimesandbox "github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/jmoiron/sqlx"
)

func TestInsertPendingWithLimitReservesSlotUnderTenantLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewRepository(sqlx.NewDb(db, "sqlmock"))
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).
		WithArgs("sandbox-concurrency:tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT[\s\S]*WHERE id = \$1`).
		WithArgs("sbx_new").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`SELECT COUNT\(\*\)[\s\S]*FROM sandbox_instances`).
		WithArgs("tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectRollback()

	_, err = repo.InsertPendingWithLimit(context.Background(), Row{
		ID:             "sbx_new",
		TenantID:       "tenant-a",
		SessionID:      "new",
		Status:         StatePending,
		LifecycleState: StatePending,
		DesiredState:   DesireRunning,
		ShortCode:      sql.NullString{String: "abc123", Valid: true},
	}, 10)
	if !errors.Is(err, runtimesandbox.ErrConcurrentSandboxLimit) {
		t.Fatalf("InsertPendingWithLimit() error = %v, want ErrConcurrentSandboxLimit", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestApplyTransitionMaintainsLifecycleTimestamps(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	xdb := sqlx.NewDb(db, "sqlmock")
	repo := NewRepository(xdb)
	ctx := context.Background()
	next := newRow(StateSleeping, DesireSleeping)
	next.ReconcileAfter = time.Now().Add(24 * time.Hour)

	// ApplyTransition opens its own tx (lease-guarded write-back); the
	// SQL must stamp stopped_at on sleeping, clear stopped_at and
	// archived_at on running, and clear the error breadcrumbs.
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE sandbox_instances.*error_reason\s*=\s*NULL.*stopped_at\s*=\s*CASE.*WHEN \$2::text = 'sleeping' THEN NOW\(\).*WHEN \$2::text = 'running'\s+THEN NULL.*archived_at\s*=\s*CASE.*WHEN \$2::text = 'archived' THEN NOW\(\).*WHEN \$2::text = 'running'\s+THEN NULL.*reconcile_locked_by = \$11`).
		WithArgs(
			next.ID,
			next.LifecycleState,
			next.Status,
			next.DesiredState,
			next.ContainerID,
			next.AgentTarget,
			next.Error,
			next.ReconcileAfter,
			next.WorkspaceSnapshotRef,
			next.WorkspaceArchiveRef,
			"leader-1",
			sql.NullTime{},
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.ApplyTransition(ctx, "leader-1", next); err != nil {
		t.Fatalf("ApplyTransition: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

// A sandbox can be stopped for hours or days and then revived with the same
// durable identity. The billing window for the revived VM must start when it
// reaches running again, not at the sandbox's original created_at timestamp;
// otherwise the sleeping interval is charged as compute after a gateway
// restart reloads the row from Postgres.
func TestApplyTransitionStartsNewBillingWindowWhenSandboxRuns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	xdb := sqlx.NewDb(db, "sqlmock")
	repo := NewRepository(xdb)
	next := newRow(StateRunning, DesireRunning)
	next.BillingStartedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE sandbox_instances.*billing_started_at\s*=\s*CASE.*WHEN \$2::text = 'running' AND lifecycle_state IS DISTINCT FROM 'running'\s+THEN COALESCE\(\$12, NOW\(\)\).*WHEN \$2::text IN \('sleeping', 'archived', 'terminated'\) THEN NULL.*billing_ended_at\s*=\s*CASE.*WHEN \$2::text = 'running' AND lifecycle_state IS DISTINCT FROM 'running' THEN NULL.*updated_at\s*=\s*NOW\(\)`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.ApplyTransition(context.Background(), "leader-1", next); err != nil {
		t.Fatalf("ApplyTransition: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestApplyTransitionLostLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	xdb := sqlx.NewDb(db, "sqlmock")
	repo := NewRepository(xdb)
	next := newRow(StateSleeping, DesireSleeping)

	// Zero rows affected means another claimer owns the lease now; the
	// result must be discarded with ErrLeaseLost, not committed.
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE sandbox_instances`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err = repo.ApplyTransition(context.Background(), "leader-1", next)
	if err != ErrLeaseLost {
		t.Fatalf("want ErrLeaseLost, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
