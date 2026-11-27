package eval_runner

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/everstacklabs/everstack/internal/enterprise"
)

type schedulerTierStub struct{}

func (schedulerTierStub) PlanTier(context.Context, string) (string, bool) { return "free", true }

// A scheduled run is an eval run: on Cloud, an organization at its monthly
// cap skips the occurrence (no run row, schedule advanced and still enabled)
// instead of slipping past the cap that CreateEvalRun enforces.
func TestExecuteScheduleSkipsTheOccurrenceAtTheMonthlyCap(t *testing.T) {
	if enterprise.IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	enterprise.SetManagedGateway(true)
	enterprise.SetPlanTierResolver(schedulerTierStub{})
	enterprise.SetEnforcementPolicy(true, nil)
	t.Cleanup(func() {
		enterprise.SetManagedGateway(false)
		enterprise.SetPlanTierResolver(nil)
		enterprise.SetShadowEnforcement(true)
	})

	db, mock, err := sqlmock.New()
	mock.MatchExpectationsInOrder(true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scheduler := testEvalScheduler()
	scheduler.db = sqlx.NewDb(db, "sqlmock")
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	sched := scheduleRow{
		ID: "schedule-42", TenantID: "tenant-1", DatasetID: "dataset-1", Name: "Daily eval",
		EvalTargetType: "agent", EvalTargetID: "agent-1", ScorerConfigIDs: pq.StringArray{"scorer-1"},
		CronExpression: "0 * * * *", Timezone: "UTC",
	}
	// Free plan: 5 eval runs a month, 5 already this month. The count runs
	// under the per-organization advisory lock, which is released after.
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_lock($1)")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM eval_runs")).
		WithArgs(sqlmock.AnyArg(), enterprise.MonthStartUTC(now)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE eval_schedules")).
		WithArgs("schedule-42", time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := scheduler.executeSchedule(context.Background(), sched, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected no eval_runs insert at the cap: %v", err)
	}
}
