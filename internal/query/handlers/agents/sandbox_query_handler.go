package agents

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/jmoiron/sqlx"
)

// ============================================================================
// Sandbox Read Models
// ============================================================================

// SandboxInstanceReadModel maps to sandbox_instances table.
type SandboxInstanceReadModel struct {
	ID                string         `db:"id" json:"id"`
	SessionID         string         `db:"session_id" json:"session_id"`
	TenantID          string         `db:"tenant_id" json:"tenant_id"`
	Backend           string         `db:"backend" json:"backend"`
	ContainerID       string         `db:"container_id" json:"container_id"`
	Image             string         `db:"image" json:"image"`
	Status            string         `db:"status" json:"status"`
	Config            []byte         `db:"config" json:"config"`
	InstanceID        sql.NullString `db:"instance_id" json:"instance_id"`
	CreatedAt         string         `db:"created_at" json:"created_at"`
	BillingStartedAt  sql.NullTime   `db:"billing_started_at" json:"billing_started_at"`
	BillingEndedAt    sql.NullTime   `db:"billing_ended_at" json:"billing_ended_at"`
	ExpiresAt         sql.NullString `db:"expires_at" json:"expires_at"`
	LastUsedAt        sql.NullString `db:"last_used_at" json:"last_used_at"`
	IdleRetentionSecs sql.NullInt64  `db:"idle_retention_secs" json:"idle_retention_secs"`
	KeepWarm          sql.NullBool   `db:"keep_warm" json:"keep_warm"`
	DestroyedAt       sql.NullString `db:"destroyed_at" json:"destroyed_at"`
	DestroyReason     sql.NullString `db:"destroy_reason" json:"destroy_reason"`
	Error             sql.NullString `db:"error" json:"error"`
	Name              string         `db:"name" json:"name"`
	GitRepoURL        sql.NullString `db:"git_repo_url" json:"git_repo_url"`
	GitBranch         sql.NullString `db:"git_branch" json:"git_branch"`
	GitCommitSHA      sql.NullString `db:"git_commit_sha" json:"git_commit_sha"`
	LifecycleState    sql.NullString `db:"lifecycle_state" json:"lifecycle_state"`
	RevivableUntil    sql.NullString `db:"revivable_until" json:"revivable_until"`
	StoppedAt         sql.NullString `db:"stopped_at" json:"stopped_at"`
	Persistent        sql.NullBool   `db:"persistent" json:"persistent"`
	AgentID           sql.NullString `db:"agent_id" json:"agent_id"`
	ShortCode         sql.NullString `db:"short_code" json:"short_code"`
}

// SandboxExecutionReadModel maps to sandbox_executions table.
type SandboxExecutionReadModel struct {
	ID         string         `db:"id" json:"id"`
	SandboxID  string         `db:"sandbox_id" json:"sandbox_id"`
	SessionID  string         `db:"session_id" json:"session_id"`
	ToolName   sql.NullString `db:"tool_name" json:"tool_name"`
	ToolCallID sql.NullString `db:"tool_call_id" json:"tool_call_id"`
	Language   sql.NullString `db:"language" json:"language"`
	Command    string         `db:"command" json:"command"`
	ExitCode   int            `db:"exit_code" json:"exit_code"`
	Stdout     sql.NullString `db:"stdout" json:"stdout"`
	Stderr     sql.NullString `db:"stderr" json:"stderr"`
	DurationMs int64          `db:"duration_ms" json:"duration_ms"`
	TimedOut   bool           `db:"timed_out" json:"timed_out"`
	CreatedAt  string         `db:"created_at" json:"created_at"`
}

// ============================================================================
// Query Types
// ============================================================================

// ListSandboxInstancesQuery retrieves sandbox instances for a tenant.
type ListSandboxInstancesQuery struct {
	query.BaseQuery
	TenantID string  `json:"tenant_id"`
	Status   *string `json:"status,omitempty"`
	QLimit   int     `json:"limit,omitempty"`
	QOffset  int     `json:"offset,omitempty"`
}

func NewListSandboxInstancesQuery(tenantID string, status *string, limit, offset int) *ListSandboxInstancesQuery {
	return &ListSandboxInstancesQuery{TenantID: tenantID, Status: status, QLimit: limit, QOffset: offset}
}

func (q ListSandboxInstancesQuery) QueryType() string { return "ListSandboxInstances" }
func (q ListSandboxInstancesQuery) Validate() error   { return nil }

// ListSandboxExecutionsQuery retrieves executions for a sandbox.
type ListSandboxExecutionsQuery struct {
	query.BaseQuery
	TenantID  string `json:"tenant_id"`
	SandboxID string `json:"sandbox_id"`
	QLimit    int    `json:"limit,omitempty"`
	QOffset   int    `json:"offset,omitempty"`
}

func NewListSandboxExecutionsQuery(tenantID, sandboxID string, limit, offset int) *ListSandboxExecutionsQuery {
	return &ListSandboxExecutionsQuery{TenantID: tenantID, SandboxID: sandboxID, QLimit: limit, QOffset: offset}
}

func (q ListSandboxExecutionsQuery) QueryType() string { return "ListSandboxExecutions" }
func (q ListSandboxExecutionsQuery) Validate() error   { return nil }

// ============================================================================
// Query Handlers
// ============================================================================

// ListSandboxInstancesQueryHandler handles ListSandboxInstances queries.
type ListSandboxInstancesQueryHandler struct{ db *sqlx.DB }

func NewListSandboxInstancesQueryHandler(db *sqlx.DB) *ListSandboxInstancesQueryHandler {
	return &ListSandboxInstancesQueryHandler{db: db}
}

func (h *ListSandboxInstancesQueryHandler) QueryType() string { return "ListSandboxInstances" }

func (h *ListSandboxInstancesQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListSandboxInstancesQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListSandboxInstancesQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list sandbox instances query")

	// Bound the query so a slow scan can't pile up under FE polling. The
	// admin sandboxes page polls this every 1.5–5s; without a deadline a
	// single slow call backs up callers in the pgx pool and the page never
	// paints. 3s is generous for an indexed scan; anything slower is a
	// real DB problem that should surface as an error.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Apply a default LIMIT when the caller didn't supply one. Both
	// instances-tab.tsx and overview-tab.tsx call useSandboxInstances()
	// with no opts, which lands here as QLimit == 0 and used to skip the
	// LIMIT clause entirely — turning a routine list into a full table
	// scan as soon as the table grew.
	const defaultLimit = 100
	const maxLimit = 500
	if qry.QLimit <= 0 {
		qry.QLimit = defaultLimit
	} else if qry.QLimit > maxLimit {
		qry.QLimit = maxLimit
	}

	if qry.TenantID == "" {
		return &SandboxInstancesResult{Instances: []SandboxInstanceReadModel{}, Total: 0}, nil
	}

	queryStr := `SELECT
		id,
		session_id,
		tenant_id,
		backend,
		container_id,
		image,
		status,
		config,
		instance_id,
		created_at,
		billing_started_at,
		billing_ended_at,
		expires_at,
		last_used_at,
		idle_retention_secs,
		keep_warm,
		destroyed_at,
		destroy_reason,
		error,
		name,
		git_repo_url,
		git_branch,
		git_commit_sha,
		lifecycle_state,
		revivable_until,
		stopped_at,
		persistent,
		agent_id,
		short_code
	FROM sandbox_instances
	WHERE tenant_id = $1`
	args := []interface{}{qry.TenantID}
	argIndex := 2

	if qry.Status != nil {
		queryStr += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, *qry.Status)
		argIndex++
	}

	queryStr += " ORDER BY created_at DESC"

	if qry.QLimit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.QLimit)
		argIndex++
	}

	if qry.QOffset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.QOffset)
	}

	var out []SandboxInstanceReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list sandbox instances: %w", err)
	}

	countQuery := `SELECT COUNT(*) FROM sandbox_instances WHERE tenant_id = $1`
	countArgs := []interface{}{qry.TenantID}
	countArgIndex := 2
	if qry.Status != nil {
		countQuery += fmt.Sprintf(" AND status = $%d", countArgIndex)
		countArgs = append(countArgs, *qry.Status)
	}
	var total int
	_ = h.db.GetContext(ctx, &total, countQuery, countArgs...)

	return &SandboxInstancesResult{Instances: out, Total: total}, nil
}

// SandboxInstancesResult wraps instance results with total count.
type SandboxInstancesResult struct {
	Instances []SandboxInstanceReadModel `json:"instances"`
	Total     int                        `json:"total"`
}

// ListSandboxExecutionsQueryHandler handles ListSandboxExecutions queries.
type ListSandboxExecutionsQueryHandler struct{ db *sqlx.DB }

func NewListSandboxExecutionsQueryHandler(db *sqlx.DB) *ListSandboxExecutionsQueryHandler {
	return &ListSandboxExecutionsQueryHandler{db: db}
}

func (h *ListSandboxExecutionsQueryHandler) QueryType() string { return "ListSandboxExecutions" }

func (h *ListSandboxExecutionsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListSandboxExecutionsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListSandboxExecutionsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"sandbox_id", qry.SandboxID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list sandbox executions query")

	if qry.TenantID == "" {
		return &SandboxExecutionsResult{Executions: []SandboxExecutionReadModel{}, Total: 0}, nil
	}

	queryStr := `SELECT se.* FROM sandbox_executions se
		WHERE se.sandbox_id = $1 AND se.tenant_id = $2`
	args := []interface{}{qry.SandboxID, qry.TenantID}
	argIndex := 3

	queryStr += " ORDER BY se.created_at DESC"

	if qry.QLimit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.QLimit)
		argIndex++
	}

	if qry.QOffset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.QOffset)
	}

	var out []SandboxExecutionReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list sandbox executions: %w", err)
	}

	var total int
	_ = h.db.GetContext(ctx, &total, `
		SELECT COUNT(*) FROM sandbox_executions
		WHERE sandbox_id = $1 AND tenant_id = $2
	`, qry.SandboxID, qry.TenantID)

	return &SandboxExecutionsResult{Executions: out, Total: total}, nil
}

// SandboxExecutionsResult wraps execution results with total count.
type SandboxExecutionsResult struct {
	Executions []SandboxExecutionReadModel `json:"executions"`
	Total      int                         `json:"total"`
}

// ============================================================================
// Sandbox Events Read Model & Query
// ============================================================================

// SandboxEventReadModel maps to sandbox_events table.
type SandboxEventReadModel struct {
	ID         int64          `db:"id" json:"id"`
	SandboxID  string         `db:"sandbox_id" json:"sandbox_id"`
	SessionID  string         `db:"session_id" json:"session_id"`
	TenantID   string         `db:"tenant_id" json:"tenant_id"`
	EventType  string         `db:"event_type" json:"event_type"`
	Message    sql.NullString `db:"message" json:"message"`
	Metadata   []byte         `db:"metadata" json:"metadata"`
	DurationMs sql.NullInt64  `db:"duration_ms" json:"duration_ms"`
	Error      sql.NullString `db:"error" json:"error"`
	CreatedAt  string         `db:"created_at" json:"created_at"`
}

// ListSandboxEventsQuery retrieves lifecycle events for a sandbox.
type ListSandboxEventsQuery struct {
	query.BaseQuery
	TenantID  string  `json:"tenant_id"`
	SandboxID string  `json:"sandbox_id"`
	EventType *string `json:"event_type,omitempty"`
	QLimit    int     `json:"limit,omitempty"`
	QOffset   int     `json:"offset,omitempty"`
}

func NewListSandboxEventsQuery(tenantID, sandboxID string, eventType *string, limit, offset int) *ListSandboxEventsQuery {
	return &ListSandboxEventsQuery{TenantID: tenantID, SandboxID: sandboxID, EventType: eventType, QLimit: limit, QOffset: offset}
}

func (q ListSandboxEventsQuery) QueryType() string { return "ListSandboxEvents" }
func (q ListSandboxEventsQuery) Validate() error   { return nil }

// ListSandboxEventsQueryHandler handles ListSandboxEvents queries.
type ListSandboxEventsQueryHandler struct{ db *sqlx.DB }

func NewListSandboxEventsQueryHandler(db *sqlx.DB) *ListSandboxEventsQueryHandler {
	return &ListSandboxEventsQueryHandler{db: db}
}

func (h *ListSandboxEventsQueryHandler) QueryType() string { return "ListSandboxEvents" }

func (h *ListSandboxEventsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListSandboxEventsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListSandboxEventsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"sandbox_id", qry.SandboxID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list sandbox events query")

	if qry.TenantID == "" {
		return &SandboxEventsResult{Events: []SandboxEventReadModel{}, Total: 0}, nil
	}

	queryStr := `SELECT se.* FROM sandbox_events se
		WHERE se.sandbox_id = $1 AND se.tenant_id = $2`
	args := []interface{}{qry.SandboxID, qry.TenantID}
	argIndex := 3

	if qry.EventType != nil {
		queryStr += fmt.Sprintf(" AND se.event_type = $%d", argIndex)
		args = append(args, *qry.EventType)
		argIndex++
	}

	queryStr += " ORDER BY se.created_at DESC"

	if qry.QLimit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.QLimit)
		argIndex++
	}

	if qry.QOffset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.QOffset)
	}

	var out []SandboxEventReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list sandbox events: %w", err)
	}

	countQuery := `SELECT COUNT(*) FROM sandbox_events se
		WHERE se.sandbox_id = $1 AND se.tenant_id = $2`
	countArgs := []interface{}{qry.SandboxID, qry.TenantID}
	countArgIndex := 3
	if qry.EventType != nil {
		countQuery += fmt.Sprintf(" AND se.event_type = $%d", countArgIndex)
		countArgs = append(countArgs, *qry.EventType)
	}
	var total int
	_ = h.db.GetContext(ctx, &total, countQuery, countArgs...)

	return &SandboxEventsResult{Events: out, Total: total}, nil
}

// SandboxEventsResult wraps event results with total count.
type SandboxEventsResult struct {
	Events []SandboxEventReadModel `json:"events"`
	Total  int                     `json:"total"`
}

// ============================================================================
// Sandbox Port Mappings Read Model & Query
// ============================================================================

// SandboxPortMappingReadModel maps to sandbox_ports table.
type SandboxPortMappingReadModel struct {
	ID            int64          `db:"id" json:"id"`
	SandboxID     string         `db:"sandbox_id" json:"sandbox_id"`
	SessionID     string         `db:"session_id" json:"session_id"`
	TenantID      string         `db:"tenant_id" json:"tenant_id"`
	Port          int            `db:"port" json:"port"`
	Protocol      string         `db:"protocol" json:"protocol"`
	Subdomain     string         `db:"subdomain" json:"subdomain"`
	HostPort      sql.NullInt64  `db:"host_port" json:"host_port"`
	BackendTarget sql.NullString `db:"backend_target" json:"backend_target"`
	Status        string         `db:"status" json:"status"`
	CreatedAt     string         `db:"created_at" json:"created_at"`
	ClosedAt      sql.NullString `db:"closed_at" json:"closed_at"`
}

// ============================================================================
// Sandbox Crons & Webhooks Read Models
// ============================================================================

// SandboxCronReadModel maps to sandbox_crons table.
type SandboxCronReadModel struct {
	ID              int64          `db:"id" json:"id"`
	TenantID        string         `db:"tenant_id" json:"tenant_id"`
	SandboxID       string         `db:"sandbox_id" json:"sandbox_id"`
	SessionID       string         `db:"session_id" json:"session_id"`
	Name            string         `db:"name" json:"name"`
	Schedule        string         `db:"schedule" json:"schedule"`
	Command         string         `db:"command" json:"command"`
	WorkDir         string         `db:"work_dir" json:"work_dir"`
	TimeoutSeconds  int            `db:"timeout_seconds" json:"timeout_seconds"`
	Enabled         bool           `db:"enabled" json:"enabled"`
	LastRunAt       sql.NullString `db:"last_run_at" json:"last_run_at"`
	NextRunAt       sql.NullString `db:"next_run_at" json:"next_run_at"`
	RunCount        int            `db:"run_count" json:"run_count"`
	ErrorCount      int            `db:"error_count" json:"error_count"`
	LastError       sql.NullString `db:"last_error" json:"last_error"`
	AutoRecreate    bool           `db:"auto_recreate" json:"auto_recreate"`
	SandboxConfig   []byte         `db:"sandbox_config" json:"sandbox_config"`
	CreatedAt       string         `db:"created_at" json:"created_at"`
	UpdatedAt       string         `db:"updated_at" json:"updated_at"`
	ChannelConfigID sql.NullString `db:"channel_config_id" json:"channel_config_id,omitempty"`
	ChannelRef      sql.NullString `db:"channel_ref" json:"channel_ref,omitempty"`
	ThreadRef       sql.NullString `db:"thread_ref" json:"thread_ref,omitempty"`
	NotifyMessage   sql.NullString `db:"notify_message" json:"notify_message,omitempty"`
}

// SandboxWebhookReadModel maps to sandbox_webhooks table.
type SandboxWebhookReadModel struct {
	ID              int64          `db:"id" json:"id"`
	TenantID        string         `db:"tenant_id" json:"tenant_id"`
	SandboxID       string         `db:"sandbox_id" json:"sandbox_id"`
	SessionID       string         `db:"session_id" json:"session_id"`
	Name            string         `db:"name" json:"name"`
	Path            string         `db:"path" json:"path"`
	Secret          string         `db:"secret" json:"-"`
	Command         string         `db:"command" json:"command"`
	WorkDir         string         `db:"work_dir" json:"work_dir"`
	TimeoutSeconds  int            `db:"timeout_seconds" json:"timeout_seconds"`
	Enabled         bool           `db:"enabled" json:"enabled"`
	RateLimitRPM    int            `db:"rate_limit_rpm" json:"rate_limit_rpm"`
	LastTriggeredAt sql.NullString `db:"last_triggered_at" json:"last_triggered_at"`
	TriggerCount    int            `db:"trigger_count" json:"trigger_count"`
	ErrorCount      int            `db:"error_count" json:"error_count"`
	LastError       sql.NullString `db:"last_error" json:"last_error"`
	AutoRecreate    bool           `db:"auto_recreate" json:"auto_recreate"`
	SandboxConfig   []byte         `db:"sandbox_config" json:"sandbox_config"`
	CreatedAt       string         `db:"created_at" json:"created_at"`
	UpdatedAt       string         `db:"updated_at" json:"updated_at"`
}

// SandboxTriggerReadModel maps to sandbox_triggers table.
type SandboxTriggerReadModel struct {
	ID             int64          `db:"id" json:"id"`
	TriggerType    string         `db:"trigger_type" json:"trigger_type"`
	TriggerID      int64          `db:"trigger_id" json:"trigger_id"`
	SandboxID      string         `db:"sandbox_id" json:"sandbox_id"`
	ExecutionID    sql.NullString `db:"execution_id" json:"execution_id"`
	Status         string         `db:"status" json:"status"`
	Error          sql.NullString `db:"error" json:"error"`
	DurationMs     sql.NullInt64  `db:"duration_ms" json:"duration_ms"`
	WebhookMethod  sql.NullString `db:"webhook_method" json:"webhook_method"`
	WebhookHeaders []byte         `db:"webhook_headers" json:"webhook_headers"`
	WebhookBody    sql.NullString `db:"webhook_body" json:"webhook_body"`
	CreatedAt      string         `db:"created_at" json:"created_at"`
}
