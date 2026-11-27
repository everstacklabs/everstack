package eval_runner

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func pausedGate(paused ...string) func(context.Context, string) bool {
	set := map[string]bool{}
	for _, id := range paused {
		set[id] = true
	}
	return func(_ context.Context, tenantID string) bool { return !set[tenantID] }
}

func newMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return sqlx.NewDb(raw, "sqlmock"), mock
}

func TestTenantGateNilAndConcurrentSet(t *testing.T) {
	var g tenantGate
	if !g.active(context.Background(), "x") {
		t.Fatal("zero gate must treat tenants as active")
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); g.set(pausedGate("x")) }()
		go func() { defer wg.Done(); _ = g.active(context.Background(), "x") }()
	}
	wg.Wait()
	if g.active(context.Background(), "x") {
		t.Fatal("paused tenant reported active")
	}
	g.set(nil)
	if !g.active(context.Background(), "x") {
		t.Fatal("nil fn must treat tenants as active")
	}
}

func TestExecuteSchedulePausedTenantAdvancesWithoutRun(t *testing.T) {
	db, mock := newMockDB(t)
	s := testEvalScheduler()
	s.db = db
	s.SetTenantActiveFn(pausedGate("tenant-paused"))
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	// Only the next_run_at advance: no INSERT INTO eval_runs, no last_run_at.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE eval_schedules SET next_run_at = $2, updated_at = NOW() WHERE id = $1")).
		WithArgs("schedule-1", time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := s.executeSchedule(context.Background(), scheduleRow{
		ID: "schedule-1", TenantID: "tenant-paused", CronExpression: "0 * * * *", Timezone: "UTC",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteScheduleActiveTenantStillCreatesRun(t *testing.T) {
	db, mock := newMockDB(t)
	s := testEvalScheduler()
	s.db = db
	s.SetTenantActiveFn(pausedGate("someone-else"))
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO eval_runs")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE eval_schedules SET last_run_at = $2, next_run_at = $3")).
		WithArgs("schedule-1", now, time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := s.executeSchedule(context.Background(), scheduleRow{
		ID: "schedule-1", TenantID: "tenant-1", CronExpression: "0 * * * *", Timezone: "UTC",
		ScorerConfigIDs: pq.StringArray{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerTickDefersPausedTenantRun(t *testing.T) {
	db, mock := newMockDB(t)
	r := &Runner{db: db, workerID: "worker-1", leaseTTL: defaultEvalRunLeaseTTL, claimBatch: 10}
	r.SetTenantActiveFn(pausedGate("tenant-paused"))

	mock.ExpectQuery(regexp.QuoteMeta("UPDATE eval_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "lease_epoch"}).AddRow("run-1", int64(3)))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE eval_run_items")).WithArgs("run-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("FROM eval_runs")).WithArgs("run-1", "worker-1", int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "dataset_id", "dataset_version_id", "status",
			"eval_target_type", "eval_target_id", "eval_config", "scorer_config_ids",
		}).AddRow("run-1", "tenant-paused", "ds-1", "", "running", "agent", "a-1", []byte("{}"), "{}"))
	// Returned to pending, lease parked for a minute, no attempt/failure.
	mock.ExpectExec(`(?s)SET status = 'pending',\s+lease_owner = \$4,\s+lease_expires_at = NOW\(\) \+ \(\$5 \* INTERVAL '1 second'\)`).
		WithArgs("run-1", "worker-1", int64(3), pausedLeaseOwner, int64(60)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	r.tick(context.Background())
	// Give any (erroneously) spawned processRun goroutine a chance to hit the
	// mock; it would fail ExpectationsWereMet via unexpected calls.
	time.Sleep(50 * time.Millisecond)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSamplingTickSkipsPausedRuleAndBumpsLastRun(t *testing.T) {
	db, mock := newMockDB(t)
	s := &SamplingScheduler{runner: &Runner{db: db}, leaderKey: "k", stop: make(chan struct{})}
	var mu sync.Mutex
	var checked []string
	s.SetTenantActiveFn(func(ctx context.Context, tenantID string) bool {
		mu.Lock()
		checked = append(checked, tenantID)
		mu.Unlock()
		return pausedGate("tenant-paused")(ctx, tenantID)
	})

	mock.ExpectQuery(regexp.QuoteMeta("pg_try_advisory_lock")).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta("FROM sampling_eval_rules")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).
			AddRow("rule-paused", "tenant-paused").
			AddRow("rule-active", "tenant-active"))
	mock.ExpectExec(`(?s)UPDATE sampling_eval_rules\s+SET last_run_at = NOW\(\)\s+WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs("rule-paused", "tenant-paused").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// rule-active reaches ExecuteSamplingRule, which bails before any DB call
	// because this Runner has no score recorder; crucially it is not bumped.
	mock.ExpectExec(regexp.QuoteMeta("pg_advisory_unlock")).WillReturnResult(sqlmock.NewResult(0, 0))

	s.tick(context.Background())
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 || checked[0] != "tenant-paused" || checked[1] != "tenant-active" {
		t.Fatalf("gate calls = %v", checked)
	}
}

// TestPausedTenantRunDeferredPG verifies the defer SQL against real Postgres:
// the run goes back to pending, is not failed, and is not re-claimable until
// the pause delay elapses. Gated on EVAL_RUNNER_PG_DSN like the lease tests.
func TestPausedTenantRunDeferredPG(t *testing.T) {
	h := newLeaseHarness(t)
	db := h.openDB(t)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	run := h.insertRun(ctx, t, db, "pending", 2)
	r := newLeaseTestRunner(db, "worker-paused")
	r.SetTenantActiveFn(pausedGate(run.TenantID))
	r.tick(ctx)

	var status, owner string
	var parkedFor float64
	var items int
	if err := db.QueryRowContext(ctx, `
		SELECT status, COALESCE(lease_owner, ''),
			EXTRACT(EPOCH FROM (lease_expires_at - NOW())),
			(SELECT COUNT(*) FROM eval_run_items WHERE eval_run_id = $1)
		FROM eval_runs WHERE id = $1
	`, run.ID).Scan(&status, &owner, &parkedFor, &items); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || owner != pausedLeaseOwner || parkedFor < 50 || parkedFor > 61 || items != 0 {
		t.Fatalf("status=%q owner=%q parkedFor=%.1fs items=%d", status, owner, parkedFor, items)
	}

	claims, err := newLeaseTestRunner(db, "worker-other").claimRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("paused run re-claimed immediately: %v", claims)
	}

	// Once the park expires and the tenant is active, it is claimable again.
	if _, err := db.ExecContext(ctx, `UPDATE eval_runs SET lease_expires_at = NOW() - INTERVAL '1 second' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	claims, err = newLeaseTestRunner(db, "worker-other").claimRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].ID != run.ID {
		t.Fatalf("resumed run not claimable: %v", claims)
	}
}
