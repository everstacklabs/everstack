package trigger

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestPostgresStoreListTriggersHandlesNullableColumns(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	store := NewPostgresStore(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{
		"id", "tenant_id", "agent_id", "name", "trigger_type", "enabled",
		"cron_expression", "cron_timezone", "webhook_secret_hash", "webhook_path",
		"event_source_agent_id", "event_type", "event_filter", "input_template",
		"max_retries", "retry_delay_seconds", "timeout_seconds", "max_concurrent",
		"consecutive_failures", "circuit_state", "circuit_opened_at",
		"created_at", "updated_at", "workflow_id",
	}).AddRow(
		"trigger-1", "tenant-1", "agent-1", "legacy cron", "cron", true,
		nil, nil, nil, nil,
		nil, nil, nil, nil,
		nil, nil, nil, nil,
		nil, nil, nil,
		now, now, nil,
	)
	mock.ExpectQuery(`SELECT \* FROM agent_triggers WHERE agent_id = \$1 AND tenant_id = \$2 ORDER BY created_at DESC`).
		WithArgs("agent-1", "tenant-1").
		WillReturnRows(rows)

	triggers, err := store.ListTriggers(context.Background(), "agent-1", "tenant-1")
	if err != nil {
		t.Fatalf("ListTriggers() error = %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("ListTriggers() returned %d triggers, want 1", len(triggers))
	}
	got := triggers[0]
	if got.CronExpression != "" || got.WebhookPath != "" || got.EventSourceAgentID != "" || got.WorkflowID != "" {
		t.Fatalf("nullable strings were not normalized: %+v", got)
	}
	if got.CronTimezone != "UTC" || got.RetryDelaySeconds != 60 || got.TimeoutSeconds != 300 || got.MaxConcurrent != 1 {
		t.Fatalf("nullable defaults were not restored: %+v", got)
	}
	if got.CircuitState != CircuitClosed {
		t.Fatalf("CircuitState = %q, want %q", got.CircuitState, CircuitClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// Trigger executions are listed only for the tenant that owns the trigger.
func TestListExecutionsFiltersByTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	store := NewPostgresStore(sqlx.NewDb(rawDB, "sqlmock"))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM agent_trigger_executions WHERE trigger_id = $1 AND tenant_id = $2")).
		WithArgs("trg-1", "tenant-a").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("FROM agent_trigger_executions WHERE trigger_id = $1 AND tenant_id = $2")).
		WithArgs("trg-1", "tenant-a", 50, 0).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, _, err := store.ListExecutions(context.Background(), "trg-1", "tenant-a", 0, 0); err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListEventTriggersFiltersBySourceTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	store := NewPostgresStore(sqlx.NewDb(rawDB, "sqlmock"))

	query := regexp.QuoteMeta(`SELECT * FROM agent_triggers
		WHERE trigger_type = 'event' AND enabled = TRUE AND circuit_state != 'open'
		AND event_source_agent_id = $1::UUID AND event_type = $2 AND tenant_id = $3`)
	mock.ExpectQuery(query).
		WithArgs("agent-1", "session.end", "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := store.ListEventTriggers(context.Background(), "agent-1", "session.end", "tenant-a"); err != nil {
		t.Fatalf("ListEventTriggers: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteTriggerReturnsNotFoundForAnotherTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	store := NewPostgresStore(sqlx.NewDb(rawDB, "sqlmock"))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM agent_triggers WHERE id = $1 AND tenant_id = $2`)).
		WithArgs("trigger-1", "tenant-b").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.DeleteTrigger(context.Background(), "trigger-1", "tenant-b"); !errors.Is(err, ErrTriggerNotFound) {
		t.Fatalf("DeleteTrigger error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
