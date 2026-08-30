package sandbox

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestDirectCreateLifecycleStateAvoidsReconcilerClaims(t *testing.T) {
	rawDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	manager := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}

	t.Setenv("EVS_SANDBOX_RECONCILER_ENABLED", "true")
	if got := manager.directCreateLifecycleState(); got != "provisioning" {
		t.Fatalf("reconciler-enabled state = %q, want provisioning", got)
	}

	t.Setenv("EVS_SANDBOX_RECONCILER_ENABLED", "false")
	if got := manager.directCreateLifecycleState(); got != "pending" {
		t.Fatalf("legacy state = %q, want pending", got)
	}
}

func TestMarkDestroyedMakesDirectCleanupTerminalForReconciler(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()
	t.Setenv("EVS_SANDBOX_RECONCILER_ENABLED", "true")

	manager := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	mock.ExpectExec(`UPDATE sandbox_instances[\s\S]*desired_state = CASE WHEN [$]6 THEN 'terminated'[\s\S]*lifecycle_state = CASE`).
		WithArgs("stopped", nil, "manual", "sbx-test", true, true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	manager.markDestroyedWithReason("sbx-test", nil, "manual")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestMarkDestroyedQueuesFailedCleanupForTermination(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()
	t.Setenv("EVS_SANDBOX_RECONCILER_ENABLED", "true")

	manager := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	mock.ExpectExec(`UPDATE sandbox_instances[\s\S]*desired_state = CASE WHEN [$]6 THEN 'terminated'[\s\S]*lifecycle_state = CASE`).
		WithArgs("terminating", "backend unavailable", "shutdown", "sbx-test", false, true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	manager.markDestroyedWithReason("sbx-test", errors.New("backend unavailable"), "shutdown")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestPersistRunningReplacementResetsTerminalDesiredState(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer rawDB.Close()

	manager := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	mock.ExpectExec(`ON CONFLICT [(]id[)] DO UPDATE SET[\s\S]*desired_state[\s\S]*THEN 'running'[\s\S]*destroyed_at`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	manager.persistInstance(&Instance{
		ID:             "sbx-test",
		Status:         StatusRunning,
		LifecycleState: LifecycleRunning,
		Backend:        "firecracker-agent",
		CreatedAt:      time.Now().UTC(),
		Config: InstanceConfig{
			SessionID: "session-test",
			TenantID:  "tenant-test",
			Image:     "ghcr.io/everstacklabs/sandbox:node",
		},
	})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
