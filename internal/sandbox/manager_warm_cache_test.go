package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestWarmInstanceCacheNormalizesStoppedLifecycleDrift(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	columns := []string{
		"id", "session_id", "instance_id", "container_id", "backend", "status", "lifecycle_state",
		"created_at", "billing_started_at", "billing_ended_at", "last_used_at", "name", "agent_id", "persistent", "config", "image", "short_code", "agent_target",
	}
	mock.ExpectQuery(`SELECT id, session_id, instance_id, container_id, backend, status, lifecycle_state,\s+created_at, billing_started_at, billing_ended_at, last_used_at, name, agent_id, persistent, config, image, short_code, agent_target\s+FROM sandbox_instances`).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			"sbx-a", "sess-a", nil, "ctr-a", "kubernetes", "running", "stopped",
			time.Now(), nil, nil, nil, "manual", nil, false, []byte(`{"image":"alpine","tenant_id":"tenant-a","session_id":"sess-a"}`), "alpine", "abc123", nil,
		))

	m := &SandboxManager{
		db:                 sqlx.NewDb(db, "sqlmock"),
		instances:          make(map[string]*Instance),
		instancesBySandbox: make(map[string]*Instance),
	}
	m.warmInstanceCache()

	inst := m.instances["sess-a"]
	if inst == nil {
		t.Fatal("expected warmed instance")
	}
	if inst.LifecycleState != LifecycleStopped {
		t.Fatalf("LifecycleState = %q, want %q", inst.LifecycleState, LifecycleStopped)
	}
	if inst.Status != StatusStopped {
		t.Fatalf("Status = %q, want %q", inst.Status, StatusStopped)
	}
	if inst.LastUsedAt.IsZero() {
		t.Fatal("LastUsedAt should be fail-safe populated when DB value is NULL")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestOpenRecoveredBillingWindowStartsAtObservationNotCreation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	createdAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	m := &SandboxManager{db: sqlx.NewDb(db, "sqlmock")}
	mock.ExpectQuery(`(?s)UPDATE sandbox_instances.*SET billing_started_at = \$2.*RETURNING billing_started_at`).
		WithArgs("sbx-restored", observedAt).
		WillReturnRows(sqlmock.NewRows([]string{"billing_started_at"}).AddRow(observedAt))

	startedAt, err := m.openRecoveredBillingWindow(context.Background(), "sbx-restored", observedAt)
	if err != nil {
		t.Fatalf("open recovered billing window: %v", err)
	}
	if startedAt.Equal(createdAt) {
		t.Fatal("recovered billing window used sandbox creation time")
	}
	if !startedAt.Equal(observedAt) {
		t.Fatalf("billing start = %s, want observation time %s", startedAt, observedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
