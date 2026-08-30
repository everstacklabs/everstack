package trigger

import (
	"encoding/json"
	"time"
)

// TriggerType represents the kind of trigger.
type TriggerType string

const (
	TriggerCron    TriggerType = "cron"
	TriggerWebhook TriggerType = "webhook"
	TriggerEvent   TriggerType = "event"
)

// CircuitState represents the circuit breaker state.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// ExecutionStatus represents the state of a trigger execution.
type ExecutionStatus string

const (
	StatusPending   ExecutionStatus = "pending"
	StatusRunning   ExecutionStatus = "running"
	StatusCompleted ExecutionStatus = "completed"
	StatusFailed    ExecutionStatus = "failed"
	StatusTimeout   ExecutionStatus = "timeout"
	StatusSkipped   ExecutionStatus = "skipped"
)

// Trigger represents a scheduled, webhook, or event-driven agent trigger.
type Trigger struct {
	ID       string      `db:"id" json:"id"`
	TenantID string      `db:"tenant_id" json:"tenant_id"`
	AgentID  string      `db:"agent_id" json:"agent_id"`
	Name     string      `db:"name" json:"name"`
	Type     TriggerType `db:"trigger_type" json:"trigger_type"`
	Enabled  bool        `db:"enabled" json:"enabled"`

	// Cron config
	CronExpression string `db:"cron_expression" json:"cron_expression,omitempty"`
	CronTimezone   string `db:"cron_timezone" json:"cron_timezone,omitempty"`

	// Webhook config
	WebhookSecretHash string `db:"webhook_secret_hash" json:"-"`
	WebhookPath       string `db:"webhook_path" json:"webhook_path,omitempty"`

	// Event config
	EventSourceAgentID string          `db:"event_source_agent_id" json:"event_source_agent_id,omitempty"`
	EventType          string          `db:"event_type" json:"event_type,omitempty"`
	EventFilter        json.RawMessage `db:"event_filter" json:"event_filter,omitempty"`

	// Workflow targeting (optional — when set, fires a workflow execution instead of an agent session)
	WorkflowID string `db:"workflow_id" json:"workflow_id,omitempty"`

	// Execution config
	InputTemplate     string `db:"input_template" json:"input_template,omitempty"`
	MaxRetries        int    `db:"max_retries" json:"max_retries"`
	RetryDelaySeconds int    `db:"retry_delay_seconds" json:"retry_delay_seconds"`
	TimeoutSeconds    int    `db:"timeout_seconds" json:"timeout_seconds"`
	MaxConcurrent     int    `db:"max_concurrent" json:"max_concurrent"`

	// Circuit breaker
	ConsecutiveFailures int          `db:"consecutive_failures" json:"consecutive_failures"`
	CircuitState        CircuitState `db:"circuit_state" json:"circuit_state"`
	CircuitOpenedAt     *time.Time   `db:"circuit_opened_at" json:"circuit_opened_at,omitempty"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// Execution represents a single trigger execution record.
type Execution struct {
	ID             string          `db:"id" json:"id"`
	TenantID       string          `db:"tenant_id" json:"tenant_id"`
	TriggerID      string          `db:"trigger_id" json:"trigger_id"`
	SessionID      *string         `db:"session_id" json:"session_id,omitempty"`
	Status         ExecutionStatus `db:"status" json:"status"`
	TriggerPayload json.RawMessage `db:"trigger_payload" json:"trigger_payload,omitempty"`
	InputRendered  string          `db:"input_rendered" json:"input_rendered,omitempty"`
	OutputPreview  string          `db:"output_preview" json:"output_preview,omitempty"`
	ErrorMessage   string          `db:"error_message" json:"error_message,omitempty"`
	Attempt        int             `db:"attempt" json:"attempt"`
	DurationMs     int             `db:"duration_ms" json:"duration_ms"`
	StartedAt      time.Time       `db:"started_at" json:"started_at"`
	CompletedAt    *time.Time      `db:"completed_at" json:"completed_at,omitempty"`
}
