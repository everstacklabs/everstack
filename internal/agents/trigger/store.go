package trigger

import "context"

// Store defines the persistence interface for agent triggers.
type Store interface {
	// CRUD
	CreateTrigger(ctx context.Context, t *Trigger) error
	GetTrigger(ctx context.Context, id, tenantID string) (*Trigger, error)
	ListTriggers(ctx context.Context, agentID, tenantID string) ([]*Trigger, error)
	UpdateTrigger(ctx context.Context, t *Trigger) error
	DeleteTrigger(ctx context.Context, id, tenantID string) error

	// Cron queries — system-internal cross-tenant scan (RunWithBypass site for future RLS)
	ListEnabledCronTriggers(ctx context.Context) ([]*Trigger, error)

	// Webhook queries
	GetTriggerByWebhookPath(ctx context.Context, path string) (*Trigger, error)

	// Event queries
	ListEventTriggers(ctx context.Context, sourceAgentID, eventType string) ([]*Trigger, error)

	// Circuit breaker
	IncrementFailures(ctx context.Context, id string) (int, error)
	ResetFailures(ctx context.Context, id string) error
	OpenCircuit(ctx context.Context, id string) error
	HalfOpenCircuit(ctx context.Context, id string) error
	CloseCircuit(ctx context.Context, id string) error

	// Executions
	RecordExecution(ctx context.Context, e *Execution) error
	CompleteExecution(ctx context.Context, id string, status ExecutionStatus, output, errorMsg string, durationMs int) error
	ListExecutions(ctx context.Context, triggerID string, limit, offset int) ([]*Execution, int, error)

	// Concurrency check
	CountRunningExecutions(ctx context.Context, triggerID string) (int, error)
}
