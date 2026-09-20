package eval_runner

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/robfig/cron/v3"
)

func testEvalScheduler() *Scheduler {
	return &Scheduler{parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)}
}

func TestComputeNextRunUsesTimezoneAcrossDST(t *testing.T) {
	next, err := testEvalScheduler().computeNextRun(
		"0 9 * * *",
		"Europe/Dublin",
		time.Date(2026, 3, 28, 9, 30, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestComputeNextRunRejectsInvalidTimezone(t *testing.T) {
	if _, err := testEvalScheduler().computeNextRun("0 9 * * *", "Mars/Olympus", time.Now()); err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestComputeNextRunRejectsImpossibleSchedule(t *testing.T) {
	if _, err := testEvalScheduler().computeNextRun("0 0 31 2 *", "UTC", time.Now()); err == nil {
		t.Fatal("expected impossible schedule error")
	}
}

func TestCreateScheduleRejectsInvalidOrImpossibleTimingBeforeInsert(t *testing.T) {
	for _, rec := range []*EvalScheduleRecord{
		{CronExpression: "0 * * * *", Timezone: "Mars/Olympus"},
		{CronExpression: "0 0 31 2 *", Timezone: "UTC"},
	} {
		if err := CreateSchedule(context.Background(), nil, rec); err == nil {
			t.Fatalf("expected timing validation error for %+v", rec)
		}
	}
}

func TestExecuteScheduleInsertsExactScheduleProvenance(t *testing.T) {
	db, mock, err := sqlmock.New()
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
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO eval_runs (id, tenant_id, dataset_id, schedule_id, name, status,")).
		WithArgs(sqlmock.AnyArg(), "tenant-1", "dataset-1", "schedule-42", sqlmock.AnyArg(), "agent", "agent-1", sqlmock.AnyArg(), sched.ScorerConfigIDs, "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE eval_schedules SET last_run_at = $2, next_run_at = $3, updated_at = NOW() WHERE id = $1")).
		WithArgs("schedule-42", now, time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := scheduler.executeSchedule(context.Background(), sched, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteScheduleInvalidTimezoneDisablesWithoutCreatingRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scheduler := testEvalScheduler()
	scheduler.db = sqlx.NewDb(db, "sqlmock")
	mock.ExpectExec(regexp.QuoteMeta("UPDATE eval_schedules SET enabled = FALSE, updated_at = NOW() WHERE id = $1")).
		WithArgs("schedule-invalid").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err = scheduler.executeSchedule(context.Background(), scheduleRow{
		ID: "schedule-invalid", CronExpression: "0 * * * *", Timezone: "Mars/Olympus",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
