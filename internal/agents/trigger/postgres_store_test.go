package trigger

import (
	"context"
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
