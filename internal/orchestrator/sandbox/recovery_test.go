package lifecycle

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// AutoRecover must re-enter the convergence state implied by
// desired_state while INCREMENTING recovery_attempts (so the recovery
// loop stays bounded) — never resetting it the way Recover() does.
func TestAutoRecoverIncrementsRecoveryAttempts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	xdb := sqlx.NewDb(db, "sqlmock")
	repo := NewRepository(xdb)

	// The single UPDATE bumps recovery_attempts by one, resets the
	// per-convergence retry budget, clears the error breadcrumbs, and is
	// guarded to error/failed rows only.
	mock.ExpectExec(`(?s)UPDATE sandbox_instances.*recovery_attempts\s*=\s*recovery_attempts \+ 1.*reconcile_attempts\s*=\s*0.*error_at\s*=\s*NULL.*lifecycle_state IN \('error', 'failed'\)`).
		WithArgs("sbx_test").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.AutoRecover(context.Background(), "sbx_test"); err != nil {
		t.Fatalf("AutoRecover: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

// A row that left the error state between the checker's scan and the
// AutoRecover write affects zero rows and must surface ErrNotFound so the
// checker treats it as benign.
func TestAutoRecoverNoRowReturnsNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	xdb := sqlx.NewDb(db, "sqlmock")
	repo := NewRepository(xdb)

	mock.ExpectExec(`(?s)UPDATE sandbox_instances`).
		WithArgs("sbx_gone").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := repo.AutoRecover(context.Background(), "sbx_gone"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

// The recovery checker must never auto-retry a row whose saved workspace is
// gone: the scan excludes the restore-failed error code.
func TestRecoveryCheckerSkipsRestoreFailedRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	xdb := sqlx.NewDb(db, "sqlmock")
	rc := NewRecoveryChecker(xdb, NewRepository(xdb))

	mock.ExpectBegin()
	mock.ExpectQuery(`pg_try_advisory_xact_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`(?s)FROM sandbox_instances.*lifecycle_state = 'error'.*COALESCE\(error, ''\) NOT LIKE \$5.*COALESCE\(error, ''\) NOT LIKE \$6`).
		WithArgs(rc.MaxAttempts, rc.MaxBackoff.Seconds(), rc.BaseBackoff.Seconds(), rc.BatchLimit,
			"workspace_restore_failed%", "workspace_needs_attention%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "desired_state", "recovery_attempts"}))
	mock.ExpectRollback()

	rc.tick(context.Background())
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
