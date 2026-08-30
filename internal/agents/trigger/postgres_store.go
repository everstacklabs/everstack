package trigger

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// PostgresStore implements Store using PostgreSQL.
type PostgresStore struct {
	db *sqlx.DB
}

// postgresTriggerRow mirrors the nullable PostgreSQL representation while
// keeping Trigger as the non-null domain model consumed by the runtime and API.
// Non-applicable trigger fields are intentionally stored as NULL.
type postgresTriggerRow struct {
	Trigger
	CronExpression      sql.NullString `db:"cron_expression"`
	CronTimezone        sql.NullString `db:"cron_timezone"`
	WebhookSecretHash   sql.NullString `db:"webhook_secret_hash"`
	WebhookPath         sql.NullString `db:"webhook_path"`
	EventSourceAgentID  sql.NullString `db:"event_source_agent_id"`
	EventType           sql.NullString `db:"event_type"`
	EventFilter         []byte         `db:"event_filter"`
	WorkflowID          sql.NullString `db:"workflow_id"`
	InputTemplate       sql.NullString `db:"input_template"`
	MaxRetries          sql.NullInt64  `db:"max_retries"`
	RetryDelaySeconds   sql.NullInt64  `db:"retry_delay_seconds"`
	TimeoutSeconds      sql.NullInt64  `db:"timeout_seconds"`
	MaxConcurrent       sql.NullInt64  `db:"max_concurrent"`
	ConsecutiveFailures sql.NullInt64  `db:"consecutive_failures"`
	CircuitState        sql.NullString `db:"circuit_state"`
	CircuitOpenedAt     sql.NullTime   `db:"circuit_opened_at"`
}

func (r postgresTriggerRow) toTrigger() *Trigger {
	t := r.Trigger
	t.CronExpression = nullStringValue(r.CronExpression, "")
	t.CronTimezone = nullStringValue(r.CronTimezone, "UTC")
	t.WebhookSecretHash = nullStringValue(r.WebhookSecretHash, "")
	t.WebhookPath = nullStringValue(r.WebhookPath, "")
	t.EventSourceAgentID = nullStringValue(r.EventSourceAgentID, "")
	t.EventType = nullStringValue(r.EventType, "")
	t.EventFilter = append([]byte(nil), r.EventFilter...)
	t.WorkflowID = nullStringValue(r.WorkflowID, "")
	t.InputTemplate = nullStringValue(r.InputTemplate, "")
	t.MaxRetries = nullIntValue(r.MaxRetries, 0)
	t.RetryDelaySeconds = nullIntValue(r.RetryDelaySeconds, 60)
	t.TimeoutSeconds = nullIntValue(r.TimeoutSeconds, 300)
	t.MaxConcurrent = nullIntValue(r.MaxConcurrent, 1)
	t.ConsecutiveFailures = nullIntValue(r.ConsecutiveFailures, 0)
	t.CircuitState = CircuitState(nullStringValue(r.CircuitState, string(CircuitClosed)))
	if r.CircuitOpenedAt.Valid {
		openedAt := r.CircuitOpenedAt.Time
		t.CircuitOpenedAt = &openedAt
	} else {
		t.CircuitOpenedAt = nil
	}
	return &t
}

func nullStringValue(value sql.NullString, fallback string) string {
	if value.Valid {
		return value.String
	}
	return fallback
}

func nullIntValue(value sql.NullInt64, fallback int) int {
	if value.Valid {
		return int(value.Int64)
	}
	return fallback
}

func materializeTriggers(rows []postgresTriggerRow) []*Trigger {
	triggers := make([]*Trigger, len(rows))
	for i, row := range rows {
		triggers[i] = row.toTrigger()
	}
	return triggers
}

// NewPostgresStore creates a new Postgres-backed trigger store.
func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// ─── CRUD ─────────────────────────────────────────────────────────────

func (s *PostgresStore) CreateTrigger(ctx context.Context, t *Trigger) error {
	q := `
		INSERT INTO agent_triggers (
			tenant_id, agent_id, name, trigger_type, enabled,
			cron_expression, cron_timezone,
			webhook_secret_hash, webhook_path,
			event_source_agent_id, event_type, event_filter,
			input_template, max_retries, retry_delay_seconds, timeout_seconds, max_concurrent,
			workflow_id
		) VALUES (
			$1, $2, $3, $4, $5,
			NULLIF($6, ''), NULLIF($7, ''),
			NULLIF($8, ''), NULLIF($9, ''),
			NULLIF($10, '')::UUID, NULLIF($11, ''), $12,
			$13, $14, $15, $16, $17,
			NULLIF($18, '')::UUID
		) RETURNING id, created_at, updated_at`

	return s.db.QueryRowxContext(ctx, q,
		t.TenantID, t.AgentID, t.Name, t.Type, t.Enabled,
		t.CronExpression, t.CronTimezone,
		t.WebhookSecretHash, t.WebhookPath,
		t.EventSourceAgentID, t.EventType, t.EventFilter,
		t.InputTemplate, t.MaxRetries, t.RetryDelaySeconds, t.TimeoutSeconds, t.MaxConcurrent,
		t.WorkflowID,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func (s *PostgresStore) GetTrigger(ctx context.Context, id, tenantID string) (*Trigger, error) {
	var row postgresTriggerRow
	q := `SELECT * FROM agent_triggers WHERE id = $1 AND tenant_id = $2`
	if err := s.db.GetContext(ctx, &row, q, id, tenantID); err != nil {
		return nil, fmt.Errorf("get trigger: %w", err)
	}
	return row.toTrigger(), nil
}

func (s *PostgresStore) ListTriggers(ctx context.Context, agentID, tenantID string) ([]*Trigger, error) {
	var rows []postgresTriggerRow
	q := `SELECT * FROM agent_triggers WHERE agent_id = $1 AND tenant_id = $2 ORDER BY created_at DESC`
	if err := s.db.SelectContext(ctx, &rows, q, agentID, tenantID); err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	return materializeTriggers(rows), nil
}

func (s *PostgresStore) UpdateTrigger(ctx context.Context, t *Trigger) error {
	q := `
		UPDATE agent_triggers SET
			name = $2,
			enabled = $3,
			cron_expression = NULLIF($4, ''),
			cron_timezone = NULLIF($5, ''),
			webhook_path = NULLIF($6, ''),
			event_source_agent_id = NULLIF($7, '')::UUID,
			event_type = NULLIF($8, ''),
			event_filter = $9,
			input_template = $10,
			max_retries = $11,
			retry_delay_seconds = $12,
			timeout_seconds = $13,
			max_concurrent = $14,
			updated_at = NOW()
		WHERE id = $1 AND tenant_id = $15
		RETURNING updated_at`
	return s.db.QueryRowxContext(ctx, q,
		t.ID, t.Name, t.Enabled,
		t.CronExpression, t.CronTimezone,
		t.WebhookPath,
		t.EventSourceAgentID, t.EventType, t.EventFilter,
		t.InputTemplate, t.MaxRetries, t.RetryDelaySeconds, t.TimeoutSeconds, t.MaxConcurrent,
		t.TenantID,
	).Scan(&t.UpdatedAt)
}

func (s *PostgresStore) DeleteTrigger(ctx context.Context, id, tenantID string) error {
	q := `DELETE FROM agent_triggers WHERE id = $1 AND tenant_id = $2`
	_, err := s.db.ExecContext(ctx, q, id, tenantID)
	return err
}

// ─── Cron Queries ─────────────────────────────────────────────────────

func (s *PostgresStore) ListEnabledCronTriggers(ctx context.Context) ([]*Trigger, error) {
	var rows []postgresTriggerRow
	q := `SELECT * FROM agent_triggers WHERE trigger_type = 'cron' AND enabled = TRUE AND circuit_state != 'open'`
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("list cron triggers: %w", err)
	}
	return materializeTriggers(rows), nil
}

// ─── Webhook Queries ──────────────────────────────────────────────────

func (s *PostgresStore) GetTriggerByWebhookPath(ctx context.Context, path string) (*Trigger, error) {
	var row postgresTriggerRow
	q := `SELECT * FROM agent_triggers WHERE webhook_path = $1 AND trigger_type = 'webhook' AND enabled = TRUE`
	if err := s.db.GetContext(ctx, &row, q, path); err != nil {
		return nil, fmt.Errorf("get trigger by webhook path: %w", err)
	}
	return row.toTrigger(), nil
}

// ─── Event Queries ────────────────────────────────────────────────────

func (s *PostgresStore) ListEventTriggers(ctx context.Context, sourceAgentID, eventType string) ([]*Trigger, error) {
	var rows []postgresTriggerRow
	q := `SELECT * FROM agent_triggers
		WHERE trigger_type = 'event' AND enabled = TRUE AND circuit_state != 'open'
		AND event_source_agent_id = $1::UUID AND event_type = $2`
	if err := s.db.SelectContext(ctx, &rows, q, sourceAgentID, eventType); err != nil {
		return nil, fmt.Errorf("list event triggers: %w", err)
	}
	return materializeTriggers(rows), nil
}

// ─── Circuit Breaker ──────────────────────────────────────────────────

func (s *PostgresStore) IncrementFailures(ctx context.Context, id string) (int, error) {
	var count int
	q := `UPDATE agent_triggers SET consecutive_failures = consecutive_failures + 1, updated_at = NOW()
		WHERE id = $1 RETURNING consecutive_failures`
	if err := s.db.QueryRowxContext(ctx, q, id).Scan(&count); err != nil {
		return 0, fmt.Errorf("increment failures: %w", err)
	}
	return count, nil
}

func (s *PostgresStore) ResetFailures(ctx context.Context, id string) error {
	q := `UPDATE agent_triggers SET consecutive_failures = 0, updated_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, q, id)
	return err
}

func (s *PostgresStore) OpenCircuit(ctx context.Context, id string) error {
	q := `UPDATE agent_triggers SET circuit_state = 'open', circuit_opened_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, q, id)
	return err
}

func (s *PostgresStore) HalfOpenCircuit(ctx context.Context, id string) error {
	q := `UPDATE agent_triggers SET circuit_state = 'half_open', updated_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, q, id)
	return err
}

func (s *PostgresStore) CloseCircuit(ctx context.Context, id string) error {
	q := `UPDATE agent_triggers SET circuit_state = 'closed', consecutive_failures = 0, circuit_opened_at = NULL, updated_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, q, id)
	return err
}

// ─── Executions ───────────────────────────────────────────────────────

func (s *PostgresStore) RecordExecution(ctx context.Context, e *Execution) error {
	q := `
		INSERT INTO agent_trigger_executions (
			tenant_id, trigger_id, session_id, status, trigger_payload,
			input_rendered, attempt
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, started_at`
	return s.db.QueryRowxContext(ctx, q,
		e.TenantID, e.TriggerID, e.SessionID, e.Status, e.TriggerPayload,
		e.InputRendered, e.Attempt,
	).Scan(&e.ID, &e.StartedAt)
}

func (s *PostgresStore) CompleteExecution(ctx context.Context, id string, status ExecutionStatus, output, errorMsg string, durationMs int) error {
	now := time.Now()
	q := `
		UPDATE agent_trigger_executions SET
			status = $2,
			output_preview = $3,
			error_message = NULLIF($4, ''),
			duration_ms = $5,
			completed_at = $6
		WHERE id = $1`
	_, err := s.db.ExecContext(ctx, q, id, status, output, errorMsg, durationMs, now)
	return err
}

func (s *PostgresStore) ListExecutions(ctx context.Context, triggerID string, limit, offset int) ([]*Execution, int, error) {
	if limit <= 0 {
		limit = 50
	}

	var total int
	countQ := `SELECT COUNT(*) FROM agent_trigger_executions WHERE trigger_id = $1`
	if err := s.db.GetContext(ctx, &total, countQ, triggerID); err != nil {
		return nil, 0, fmt.Errorf("count executions: %w", err)
	}

	var executions []*Execution
	q := `SELECT * FROM agent_trigger_executions WHERE trigger_id = $1 ORDER BY started_at DESC LIMIT $2 OFFSET $3`
	if err := s.db.SelectContext(ctx, &executions, q, triggerID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("list executions: %w", err)
	}
	return executions, total, nil
}

func (s *PostgresStore) CountRunningExecutions(ctx context.Context, triggerID string) (int, error) {
	var count int
	q := `SELECT COUNT(*) FROM agent_trigger_executions WHERE trigger_id = $1 AND status IN ('pending', 'running')`
	if err := s.db.GetContext(ctx, &count, q, triggerID); err != nil {
		return 0, fmt.Errorf("count running executions: %w", err)
	}
	return count, nil
}
