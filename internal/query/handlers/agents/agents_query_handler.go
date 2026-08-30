package agents

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// ============================================================================
// Column Lists
// ============================================================================

// virtualColumner lets a read model declare fields a query computes rather
// than reads off the table, so they stay out of the generated column list.
type virtualColumner interface{ virtualColumns() []string }

// virtualColumns keeps `summary` out of the column list: agent_sessions has no
// such column. ListSessions derives it from the session's first turn and
// aliases it into the result; every other session query leaves it zero.
func (AgentSessionReadModel) virtualColumns() []string { return []string{"summary"} }

// selectList renders the columns a read model maps, taken from its `db` tags
// and optionally prefixed with a table alias.
//
// These handlers used to `SELECT *`. sqlx fails the entire scan when a
// returned column has no destination field, so any additive migration that
// reached the database ahead of the binary took out every agent query against
// that table with `missing destination name <column>`. That is the normal
// ordering during a rolling deploy — the new pod migrates at startup while the
// old pods keep serving — and it is what broke the dev agents pages for six
// days after agent_revisions_20260806000000 landed. Naming the columns means a
// binary only ever asks for what it can scan, so a schema that has moved ahead
// of it is invisible rather than fatal.
func selectList(model interface{}, alias string) string {
	typ := reflect.TypeOf(model)
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	virtual := map[string]bool{}
	if v, ok := model.(virtualColumner); ok {
		for _, name := range v.virtualColumns() {
			virtual[name] = true
		}
	}

	cols := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Tag.Get("db")
		if name == "" || name == "-" || virtual[name] {
			continue
		}
		if alias != "" {
			name = alias + "." + name
		}
		cols = append(cols, name)
	}
	return strings.Join(cols, ", ")
}

// Column lists resolved once at init, one per read model. `sessionColumnsS`
// carries the `s` alias ListSessions needs for its correlated subquery.
var (
	agentColumns          = selectList(AgentDefinitionReadModel{}, "")
	sessionColumns        = selectList(AgentSessionReadModel{}, "")
	sessionColumnsS       = selectList(AgentSessionReadModel{}, "s")
	sessionTurnColumns    = selectList(AgentSessionTurnReadModel{}, "")
	approvalReviewColumns = selectList(ApprovalReviewReadModel{}, "")
	spawnTreeColumns      = selectList(SpawnTreeNodeReadModel{}, "")
	agentLinkColumns      = selectList(AgentLinkReadModel{}, "")
	channelBindingColumns = selectList(AgentChannelBindingReadModel{}, "")
)

// AgentDefinitionColumns exposes the agent_definitions column list for callers
// outside this package that scan into AgentDefinitionReadModel directly.
func AgentDefinitionColumns() string { return agentColumns }

// ============================================================================
// Read Models
// ============================================================================

// AgentDefinitionReadModel maps to agent_definitions table.
type AgentDefinitionReadModel struct {
	ID                  string         `db:"id" json:"id"`
	TenantID            string         `db:"tenant_id" json:"tenant_id"`
	Name                string         `db:"name" json:"name"`
	Description         sql.NullString `db:"description" json:"description"`
	Model               string         `db:"model" json:"model"`
	SystemPrompt        sql.NullString `db:"system_prompt" json:"system_prompt"`
	Tools               pq.StringArray `db:"tools" json:"tools"`
	Config              []byte         `db:"config" json:"config"`
	MaxTurns            int32          `db:"max_turns" json:"max_turns"`
	MaxToolCallsPerTurn int32          `db:"max_tool_calls_per_turn" json:"max_tool_calls_per_turn"`
	Mode                string         `db:"mode" json:"mode"`
	MaxSteps            sql.NullInt32  `db:"max_steps" json:"max_steps"`
	TaskPermissionMode  string         `db:"task_permission_mode" json:"task_permission_mode"`
	Hidden              bool           `db:"hidden" json:"hidden"`
	Color               sql.NullString `db:"color" json:"color"`
	WorkingDirectory    sql.NullString `db:"working_directory" json:"working_directory"`
	MentionAlias        sql.NullString `db:"mention_alias" json:"mention_alias"`
	Enabled             bool           `db:"enabled" json:"enabled"`
	CreatedAt           string         `db:"created_at" json:"created_at"`
	UpdatedAt           string         `db:"updated_at" json:"updated_at"`
	DeletedAt           sql.NullString `db:"deleted_at" json:"deleted_at"`

	// Persistent agent fields
	LifecycleMode         string          `db:"lifecycle_mode" json:"lifecycle_mode"`
	LifecycleStatus       string          `db:"lifecycle_status" json:"lifecycle_status"`
	Icon                  sql.NullString  `db:"icon" json:"icon"`
	SoulMD                string          `db:"soul_md" json:"soul_md"`
	IdentityMD            string          `db:"identity_md" json:"identity_md"`
	UserMD                string          `db:"user_md" json:"user_md"`
	RoleMD                string          `db:"role_md" json:"role_md"`
	SandboxImage          sql.NullString  `db:"sandbox_image" json:"sandbox_image"`
	SandboxCPULimit       sql.NullFloat64 `db:"sandbox_cpu_limit" json:"sandbox_cpu_limit"`
	SandboxMemoryMB       sql.NullInt32   `db:"sandbox_memory_mb" json:"sandbox_memory_mb"`
	SandboxDiskMB         sql.NullInt32   `db:"sandbox_disk_mb" json:"sandbox_disk_mb"`
	SandboxTimeoutSeconds sql.NullInt32   `db:"sandbox_timeout_seconds" json:"sandbox_timeout_seconds"`
	SandboxNetworkMode    sql.NullString  `db:"sandbox_network_mode" json:"sandbox_network_mode"`
	SandboxAllowedHosts   pq.StringArray  `db:"sandbox_allowed_hosts" json:"sandbox_allowed_hosts"`
	SandboxEnvVars        []byte          `db:"sandbox_env_vars" json:"sandbox_env_vars"`
	SandboxSSHEnabled     sql.NullBool    `db:"sandbox_ssh_enabled" json:"sandbox_ssh_enabled"`
	SandboxGitRepoURL     sql.NullString  `db:"sandbox_git_repo_url" json:"sandbox_git_repo_url"`
	SandboxGitBranch      sql.NullString  `db:"sandbox_git_branch" json:"sandbox_git_branch"`
	DBSqlitePath          sql.NullString  `db:"db_sqlite_path" json:"db_sqlite_path"`
	DBLanceDBPath         sql.NullString  `db:"db_lancedb_path" json:"db_lancedb_path"`
	DBRedbPath            sql.NullString  `db:"db_redb_path" json:"db_redb_path"`
	MaxConcurrentWorkers  sql.NullInt32   `db:"max_concurrent_workers" json:"max_concurrent_workers"`
	WorkerPoolConfig      []byte          `db:"worker_pool_config" json:"worker_pool_config"`
	SandboxID             sql.NullString  `db:"sandbox_id" json:"sandbox_id"`
	PrimarySessionID      sql.NullString  `db:"primary_session_id" json:"primary_session_id"`
	ActiveRevisionID      sql.NullString  `db:"active_revision_id" json:"active_revision_id"`
	HasActiveTurn         bool            `db:"has_active_turn" json:"has_active_turn"`
}

// AgentSessionReadModel maps to agent_sessions table.
type AgentSessionReadModel struct {
	ID               string         `db:"id" json:"id"`
	TenantID         string         `db:"tenant_id" json:"tenant_id"`
	AgentID          sql.NullString `db:"agent_id" json:"agent_id"`
	RevisionID       sql.NullString `db:"revision_id" json:"revision_id"`
	Status           string         `db:"status" json:"status"`
	TurnCount        int32          `db:"turn_count" json:"turn_count"`
	TotalTokens      int32          `db:"total_tokens" json:"total_tokens"`
	Metadata         []byte         `db:"metadata" json:"metadata"`
	CreatedAt        string         `db:"created_at" json:"created_at"`
	UpdatedAt        string         `db:"updated_at" json:"updated_at"`
	CompletedAt      sql.NullString `db:"completed_at" json:"completed_at"`
	InstanceID       sql.NullString `db:"instance_id" json:"instance_id"`
	HeartbeatAt      sql.NullString `db:"heartbeat_at" json:"heartbeat_at"`
	Source           string         `db:"source" json:"source"`
	ChannelConfigID  sql.NullString `db:"channel_config_id" json:"channel_config_id"`
	PlatformUserID   sql.NullString `db:"platform_user_id" json:"platform_user_id"`
	PlatformUserName sql.NullString `db:"platform_user_name" json:"platform_user_name"`
	TrooperID        sql.NullString `db:"trooper_id" json:"trooper_id"`
	HibernatedAt     sql.NullString `db:"hibernated_at" json:"hibernated_at"`
	PendingSteers    []byte         `db:"pending_steers" json:"pending_steers"`
	Summary          sql.NullString `db:"summary" json:"summary"`
}

// AgentSessionTurnReadModel maps to agent_session_turns table.
//
// PromptTokens is the inclusive input total; CacheReadInputTokens and
// CacheWriteInputTokens are non-overlapping subsets so the UI can split
// fresh / cache_read / cache_write for billing display.
type AgentSessionTurnReadModel struct {
	ID                    string         `db:"id" json:"id"`
	SessionID             string         `db:"session_id" json:"session_id"`
	TurnNumber            int32          `db:"turn_number" json:"turn_number"`
	Status                string         `db:"status" json:"status"`
	UserInput             sql.NullString `db:"user_input" json:"user_input"`
	AssistantOutput       sql.NullString `db:"assistant_output" json:"assistant_output"`
	ToolCalls             []byte         `db:"tool_calls" json:"tool_calls"`
	Timeline              []byte         `db:"timeline" json:"timeline"`
	PromptTokens          int32          `db:"prompt_tokens" json:"prompt_tokens"`
	CompletionTokens      int32          `db:"completion_tokens" json:"completion_tokens"`
	TotalTokens           int32          `db:"total_tokens" json:"total_tokens"`
	CacheReadInputTokens  int32          `db:"cache_read_input_tokens" json:"cache_read_input_tokens"`
	CacheWriteInputTokens int32          `db:"cache_write_input_tokens" json:"cache_write_input_tokens"`
	LatencyMs             int64          `db:"latency_ms" json:"latency_ms"`
	Error                 sql.NullString `db:"error" json:"error"`
	CreatedAt             string         `db:"created_at" json:"created_at"`
	CompletedAt           sql.NullString `db:"completed_at" json:"completed_at"`
}

// ApprovalReviewReadModel maps to agent_approval_reviews table.
type ApprovalReviewReadModel struct {
	ID               string         `db:"id" json:"id"`
	SessionID        string         `db:"session_id" json:"session_id"`
	TenantID         string         `db:"tenant_id" json:"tenant_id"`
	AgentID          string         `db:"agent_id" json:"agent_id"`
	TurnNumber       int32          `db:"turn_number" json:"turn_number"`
	Iteration        int32          `db:"iteration" json:"iteration"`
	Status           string         `db:"status" json:"status"`
	ToolCalls        []byte         `db:"tool_calls" json:"tool_calls"`
	Decisions        []byte         `db:"decisions" json:"decisions"`
	DefaultAction    string         `db:"default_action" json:"default_action"`
	RequestedAt      string         `db:"requested_at" json:"requested_at"`
	ExpiresAt        string         `db:"expires_at" json:"expires_at"`
	ResolvedAt       sql.NullString `db:"resolved_at" json:"resolved_at"`
	ResolvedBy       sql.NullString `db:"resolved_by" json:"resolved_by"`
	ResolutionReason sql.NullString `db:"resolution_reason" json:"resolution_reason"`
	CreatedAt        string         `db:"created_at" json:"created_at"`
}

// ============================================================================
// Query Types
// ============================================================================

// GetAgentByIDQuery retrieves an agent by ID.
type GetAgentByIDQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetAgentByIDQuery(id, tenantID string) *GetAgentByIDQuery {
	return &GetAgentByIDQuery{ID: id, TenantID: tenantID}
}

func (q GetAgentByIDQuery) QueryType() string { return "GetAgentByID" }
func (q GetAgentByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListAgentsQuery retrieves all agents for a tenant.
type ListAgentsQuery struct {
	query.BaseQuery
	TenantID      string  `json:"tenant_id"`
	Enabled       *bool   `json:"enabled,omitempty"`
	IncludeHidden bool    `json:"include_hidden"`
	Mode          *string `json:"mode,omitempty"`
	LifecycleMode *string `json:"lifecycle_mode,omitempty"`
	QLimit        int     `json:"limit,omitempty"`
	QOffset       int     `json:"offset,omitempty"`
}

func NewListAgentsQuery(tenantID string, enabled *bool, includeHidden bool, mode *string, lifecycleMode *string, limit, offset int) *ListAgentsQuery {
	return &ListAgentsQuery{
		TenantID:      tenantID,
		Enabled:       enabled,
		IncludeHidden: includeHidden,
		Mode:          mode,
		LifecycleMode: lifecycleMode,
		QLimit:        limit,
		QOffset:       offset,
	}
}

func (q ListAgentsQuery) QueryType() string { return "ListAgents" }
func (q ListAgentsQuery) Validate() error   { return nil }

// GetSessionByIDQuery retrieves a session by ID (with turns).
type GetSessionByIDQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetSessionByIDQuery(id, tenantID string) *GetSessionByIDQuery {
	return &GetSessionByIDQuery{ID: id, TenantID: tenantID}
}

func (q GetSessionByIDQuery) QueryType() string { return "GetAgentSessionByID" }
func (q GetSessionByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListSessionsQuery retrieves sessions with optional filters.
type ListSessionsQuery struct {
	query.BaseQuery
	TenantID  string  `json:"tenant_id"`
	AgentID   *string `json:"agent_id,omitempty"`
	TrooperID *string `json:"trooper_id,omitempty"`
	Status    *string `json:"status,omitempty"`
	QLimit    int     `json:"limit,omitempty"`
	QOffset   int     `json:"offset,omitempty"`
}

func NewListSessionsQuery(tenantID string, agentID, status *string, limit, offset int) *ListSessionsQuery {
	return &ListSessionsQuery{TenantID: tenantID, AgentID: agentID, Status: status, QLimit: limit, QOffset: offset}
}

func (q ListSessionsQuery) QueryType() string { return "ListAgentSessions" }
func (q ListSessionsQuery) Validate() error   { return nil }

// GetAgentByNameQuery retrieves an agent by name (case-insensitive).
type GetAgentByNameQuery struct {
	query.BaseQuery
	Name     string `json:"name"`
	TenantID string `json:"tenant_id"`
}

func NewGetAgentByNameQuery(name, tenantID string) *GetAgentByNameQuery {
	return &GetAgentByNameQuery{Name: name, TenantID: tenantID}
}

func (q GetAgentByNameQuery) QueryType() string { return "GetAgentByName" }
func (q GetAgentByNameQuery) Validate() error {
	if q.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return nil
}

// ============================================================================
// Query Handlers
// ============================================================================

// AgentByIDQueryHandler handles GetAgentByID queries.
type AgentByIDQueryHandler struct{ db *sqlx.DB }

func NewAgentByIDQueryHandler(db *sqlx.DB) *AgentByIDQueryHandler {
	return &AgentByIDQueryHandler{db: db}
}

func (h *AgentByIDQueryHandler) QueryType() string { return "GetAgentByID" }

func (h *AgentByIDQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetAgentByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetAgentByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get agent by id query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out AgentDefinitionReadModel
	var err error

	err = h.db.GetContext(ctx, &out, `
		SELECT `+agentColumns+` FROM agent_definitions
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}
	return &out, nil
}

// AgentByNameQueryHandler handles GetAgentByName queries.
type AgentByNameQueryHandler struct{ db *sqlx.DB }

func NewAgentByNameQueryHandler(db *sqlx.DB) *AgentByNameQueryHandler {
	return &AgentByNameQueryHandler{db: db}
}

func (h *AgentByNameQueryHandler) QueryType() string { return "GetAgentByName" }

func (h *AgentByNameQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetAgentByNameQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetAgentByNameQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"name", qry.Name,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get agent by name query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out AgentDefinitionReadModel
	err := h.db.GetContext(ctx, &out, `
		SELECT `+agentColumns+` FROM agent_definitions
		WHERE LOWER(name) = LOWER($1) AND tenant_id = $2 AND deleted_at IS NULL
	`, qry.Name, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get agent by name: %w", err)
	}
	return &out, nil
}

// ListAgentsQueryHandler handles ListAgents queries.
type ListAgentsQueryHandler struct{ db *sqlx.DB }

func NewListAgentsQueryHandler(db *sqlx.DB) *ListAgentsQueryHandler {
	return &ListAgentsQueryHandler{db: db}
}

func (h *ListAgentsQueryHandler) QueryType() string { return "ListAgents" }

func (h *ListAgentsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListAgentsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListAgentsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list agents query")

	// agent_definitions lives in the shared `everstack` schema; rows
	// are partitioned by tenant_id, not by per-tenant schema. Without
	// this filter the handler returned EVERY tenant's agents to
	// EVERY caller — the FE rendered them as if they belonged to the
	// caller's instance. Empty tenant short-circuits to an empty
	// result rather than running an unscoped query.
	if qry.TenantID == "" {
		return []AgentDefinitionReadModel{}, nil
	}

	var queryStr string
	var args []interface{}
	var argIndex int

	queryStr = `SELECT ` + agentColumns + ` FROM agent_definitions WHERE tenant_id = $1 AND deleted_at IS NULL`
	args = []interface{}{qry.TenantID}
	argIndex = 2

	if qry.Enabled != nil {
		queryStr += fmt.Sprintf(" AND enabled = $%d", argIndex)
		args = append(args, *qry.Enabled)
		argIndex++
	}
	if !qry.IncludeHidden {
		queryStr += " AND hidden = FALSE"
	}
	if qry.Mode != nil {
		queryStr += fmt.Sprintf(" AND mode = $%d", argIndex)
		args = append(args, *qry.Mode)
		argIndex++
	}
	if qry.LifecycleMode != nil {
		queryStr += fmt.Sprintf(" AND lifecycle_mode = $%d", argIndex)
		args = append(args, *qry.LifecycleMode)
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

	var out []AgentDefinitionReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}
	return out, nil
}

// SessionByIDQueryHandler handles GetAgentSessionByID queries.
type SessionByIDQueryHandler struct{ db *sqlx.DB }

func NewSessionByIDQueryHandler(db *sqlx.DB) *SessionByIDQueryHandler {
	return &SessionByIDQueryHandler{db: db}
}

func (h *SessionByIDQueryHandler) QueryType() string { return "GetAgentSessionByID" }

func (h *SessionByIDQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetSessionByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetSessionByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get session by id query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var session AgentSessionReadModel
	var err error

	err = h.db.GetContext(ctx, &session, `
		SELECT `+sessionColumns+` FROM agent_sessions
		WHERE id = $1 AND tenant_id = $2
	`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	var turns []AgentSessionTurnReadModel
	if err := h.db.SelectContext(ctx, &turns, `
		SELECT `+sessionTurnColumns+` FROM agent_session_turns
		WHERE session_id = $1 ORDER BY created_at ASC, turn_number ASC
	`, qry.ID); err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to fetch session turns")
	}

	return &SessionWithTurns{Session: session, Turns: turns}, nil
}

// SessionWithTurns wraps a session with its turns for query results.
type SessionWithTurns struct {
	Session AgentSessionReadModel       `json:"session"`
	Turns   []AgentSessionTurnReadModel `json:"turns"`
}

// ListSessionsQueryHandler handles ListAgentSessions queries.
type ListSessionsQueryHandler struct{ db *sqlx.DB }

func NewListSessionsQueryHandler(db *sqlx.DB) *ListSessionsQueryHandler {
	return &ListSessionsQueryHandler{db: db}
}

func (h *ListSessionsQueryHandler) QueryType() string { return "ListAgentSessions" }

func (h *ListSessionsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListSessionsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListSessionsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list sessions query")

	// agent_sessions partitions by tenant_id (see migration
	// agents_init_20260201000000). Pre-fix this handler ran against
	// `WHERE 1=1` and returned every tenant's sessions to every
	// caller — same Pattern B leak as ListAgents.
	if qry.TenantID == "" {
		return []AgentSessionReadModel{}, nil
	}

	var queryStr string
	var args []interface{}
	var argIndex int

	queryStr = `SELECT ` + sessionColumnsS + `,
		LEFT(
			(SELECT t.user_input FROM agent_session_turns t WHERE t.session_id = s.id ORDER BY t.turn_number ASC LIMIT 1),
			120
		) AS summary
		FROM agent_sessions s WHERE s.tenant_id = $1`
	args = []interface{}{qry.TenantID}
	argIndex = 2

	if qry.AgentID != nil {
		queryStr += fmt.Sprintf(" AND agent_id = $%d", argIndex)
		args = append(args, *qry.AgentID)
		argIndex++
	}

	if qry.TrooperID != nil {
		queryStr += fmt.Sprintf(" AND trooper_id = $%d", argIndex)
		args = append(args, *qry.TrooperID)
		argIndex++
	}

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

	var out []AgentSessionReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	return out, nil
}

// ============================================================================
// Approval Review Queries
// ============================================================================

// GetApprovalReviewByIDQuery retrieves an approval review by ID.
type GetApprovalReviewByIDQuery struct {
	query.BaseQuery
	ReviewID string `json:"review_id"`
	TenantID string `json:"tenant_id"`
}

func NewGetApprovalReviewByIDQuery(reviewID, tenantID string) *GetApprovalReviewByIDQuery {
	return &GetApprovalReviewByIDQuery{ReviewID: reviewID, TenantID: tenantID}
}

func (q GetApprovalReviewByIDQuery) QueryType() string { return "GetApprovalReviewByID" }
func (q GetApprovalReviewByIDQuery) Validate() error {
	if q.ReviewID == "" {
		return fmt.Errorf("review_id cannot be empty")
	}
	return nil
}

// ListApprovalReviewsQuery retrieves approval reviews with optional filters.
type ListApprovalReviewsQuery struct {
	query.BaseQuery
	TenantID  string  `json:"tenant_id"`
	SessionID *string `json:"session_id,omitempty"`
	Status    *string `json:"status,omitempty"`
	QLimit    int     `json:"limit,omitempty"`
	QOffset   int     `json:"offset,omitempty"`
}

func NewListApprovalReviewsQuery(tenantID string, sessionID, status *string, limit, offset int) *ListApprovalReviewsQuery {
	return &ListApprovalReviewsQuery{TenantID: tenantID, SessionID: sessionID, Status: status, QLimit: limit, QOffset: offset}
}

func (q ListApprovalReviewsQuery) QueryType() string { return "ListApprovalReviews" }
func (q ListApprovalReviewsQuery) Validate() error   { return nil }

// ApprovalReviewByIDQueryHandler handles GetApprovalReviewByID queries.
type ApprovalReviewByIDQueryHandler struct{ db *sqlx.DB }

func NewApprovalReviewByIDQueryHandler(db *sqlx.DB) *ApprovalReviewByIDQueryHandler {
	return &ApprovalReviewByIDQueryHandler{db: db}
}

func (h *ApprovalReviewByIDQueryHandler) QueryType() string { return "GetApprovalReviewByID" }

func (h *ApprovalReviewByIDQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetApprovalReviewByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetApprovalReviewByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"review_id", qry.ReviewID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get approval review by id query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out ApprovalReviewReadModel
	err := h.db.GetContext(ctx, &out, `
		SELECT `+approvalReviewColumns+` FROM agent_approval_reviews
		WHERE id = $1 AND tenant_id = $2
	`, qry.ReviewID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get approval review: %w", err)
	}
	return &out, nil
}

// ListApprovalReviewsQueryHandler handles ListApprovalReviews queries.
type ListApprovalReviewsQueryHandler struct{ db *sqlx.DB }

func NewListApprovalReviewsQueryHandler(db *sqlx.DB) *ListApprovalReviewsQueryHandler {
	return &ListApprovalReviewsQueryHandler{db: db}
}

func (h *ListApprovalReviewsQueryHandler) QueryType() string { return "ListApprovalReviews" }

func (h *ListApprovalReviewsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListApprovalReviewsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListApprovalReviewsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list approval reviews query")

	// agent_approval_reviews has a tenant_id column (see migration
	// agent_approval_reviews_20260202100000) that previous code
	// silently ignored — `WHERE 1=1` returned reviews from every
	// tenant. Same Pattern B leak as ListAgents / ListSessions.
	if qry.TenantID == "" {
		return &ApprovalReviewsResult{Reviews: []ApprovalReviewReadModel{}, Total: 0}, nil
	}

	queryStr := `SELECT ` + approvalReviewColumns + ` FROM agent_approval_reviews WHERE tenant_id = $1`
	args := []interface{}{qry.TenantID}
	argIndex := 2

	if qry.SessionID != nil {
		queryStr += fmt.Sprintf(" AND session_id = $%d", argIndex)
		args = append(args, *qry.SessionID)
		argIndex++
	}

	if qry.Status != nil {
		queryStr += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, *qry.Status)
		argIndex++
	}

	queryStr += " ORDER BY requested_at DESC"

	if qry.QLimit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.QLimit)
		argIndex++
	}

	if qry.QOffset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.QOffset)
	}

	var out []ApprovalReviewReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list approval reviews: %w", err)
	}

	// Total count (also tenant-scoped)
	countQuery := `SELECT COUNT(*) FROM agent_approval_reviews WHERE tenant_id = $1`
	countArgs := []interface{}{qry.TenantID}
	countArgIndex := 2
	if qry.SessionID != nil {
		countQuery += fmt.Sprintf(" AND session_id = $%d", countArgIndex)
		countArgs = append(countArgs, *qry.SessionID)
		countArgIndex++
	}
	if qry.Status != nil {
		countQuery += fmt.Sprintf(" AND status = $%d", countArgIndex)
		countArgs = append(countArgs, *qry.Status)
	}
	var total int
	_ = h.db.GetContext(ctx, &total, countQuery, countArgs...)

	return &ApprovalReviewsResult{Reviews: out, Total: total}, nil
}

// ApprovalReviewsResult wraps review results with total count.
type ApprovalReviewsResult struct {
	Reviews []ApprovalReviewReadModel `json:"reviews"`
	Total   int                       `json:"total"`
}

// ============================================================================
// Spawn Tree Queries
// ============================================================================

// SpawnTreeNodeReadModel maps to agent_spawn_trees table.
type SpawnTreeNodeReadModel struct {
	ID               string         `db:"id" json:"id"`
	TreeID           string         `db:"tree_id" json:"tree_id"`
	ParentNodeID     sql.NullString `db:"parent_node_id" json:"parent_node_id"`
	AgentID          sql.NullString `db:"agent_id" json:"agent_id"`
	Depth            int32          `db:"depth" json:"depth"`
	Status           string         `db:"status" json:"status"`
	Task             sql.NullString `db:"task" json:"task"`
	Result           sql.NullString `db:"result" json:"result"`
	PromptTokens     int32          `db:"prompt_tokens" json:"prompt_tokens"`
	CompletionTokens int32          `db:"completion_tokens" json:"completion_tokens"`
	TotalTokens      int32          `db:"total_tokens" json:"total_tokens"`
	StartedAt        string         `db:"started_at" json:"started_at"`
	CompletedAt      sql.NullString `db:"completed_at" json:"completed_at"`
	ExecutionID      sql.NullString `db:"execution_id" json:"execution_id"`
	TenantID         string         `db:"tenant_id" json:"tenant_id"`
}

// GetSpawnTreeQuery retrieves all spawn nodes for a tree by tree_id.
type GetSpawnTreeQuery struct {
	query.BaseQuery
	TreeID   string `json:"tree_id"`
	TenantID string `json:"tenant_id"`
}

func NewGetSpawnTreeQuery(treeID, tenantID string) *GetSpawnTreeQuery {
	return &GetSpawnTreeQuery{TreeID: treeID, TenantID: tenantID}
}

func (q GetSpawnTreeQuery) QueryType() string { return "GetSpawnTree" }
func (q GetSpawnTreeQuery) Validate() error {
	if q.TreeID == "" {
		return fmt.Errorf("tree_id cannot be empty")
	}
	return nil
}

// ListSpawnNodesQuery retrieves spawn nodes for a session (session_id == tree_id).
type ListSpawnNodesQuery struct {
	query.BaseQuery
	SessionID string `json:"session_id"`
	TenantID  string `json:"tenant_id"`
	QLimit    int    `json:"limit,omitempty"`
	QOffset   int    `json:"offset,omitempty"`
}

func NewListSpawnNodesQuery(sessionID, tenantID string, limit, offset int) *ListSpawnNodesQuery {
	return &ListSpawnNodesQuery{SessionID: sessionID, TenantID: tenantID, QLimit: limit, QOffset: offset}
}

func (q ListSpawnNodesQuery) QueryType() string { return "ListSpawnNodes" }
func (q ListSpawnNodesQuery) Validate() error {
	if q.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	return nil
}

// GetSpawnTreeQueryHandler handles GetSpawnTree queries.
type GetSpawnTreeQueryHandler struct{ db *sqlx.DB }

func NewGetSpawnTreeQueryHandler(db *sqlx.DB) *GetSpawnTreeQueryHandler {
	return &GetSpawnTreeQueryHandler{db: db}
}

func (h *GetSpawnTreeQueryHandler) QueryType() string { return "GetSpawnTree" }

func (h *GetSpawnTreeQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetSpawnTreeQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetSpawnTreeQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tree_id", qry.TreeID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get spawn tree query")

	if qry.TenantID == "" {
		return &SpawnTreeResult{Nodes: []SpawnTreeNodeReadModel{}, Total: 0}, nil
	}

	var out []SpawnTreeNodeReadModel
	err := h.db.SelectContext(ctx, &out, `
		SELECT `+spawnTreeColumns+` FROM agent_spawn_trees
		WHERE tree_id = $1::uuid AND tenant_id = $2
		ORDER BY depth ASC, started_at ASC
	`, qry.TreeID, qry.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get spawn tree: %w", err)
	}

	return &SpawnTreeResult{Nodes: out, Total: len(out)}, nil
}

// ListSpawnNodesQueryHandler handles ListSpawnNodes queries.
type ListSpawnNodesQueryHandler struct{ db *sqlx.DB }

func NewListSpawnNodesQueryHandler(db *sqlx.DB) *ListSpawnNodesQueryHandler {
	return &ListSpawnNodesQueryHandler{db: db}
}

func (h *ListSpawnNodesQueryHandler) QueryType() string { return "ListSpawnNodes" }

func (h *ListSpawnNodesQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListSpawnNodesQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListSpawnNodesQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"session_id", qry.SessionID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list spawn nodes query")

	if qry.TenantID == "" {
		return &SpawnTreeResult{Nodes: []SpawnTreeNodeReadModel{}, Total: 0}, nil
	}

	queryStr := `SELECT ` + spawnTreeColumns + ` FROM agent_spawn_trees WHERE tree_id = $1::uuid AND tenant_id = $2`
	args := []interface{}{qry.SessionID, qry.TenantID}
	argIndex := 3

	queryStr += " ORDER BY depth ASC, started_at ASC"

	if qry.QLimit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.QLimit)
		argIndex++
	}

	if qry.QOffset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.QOffset)
	}

	var out []SpawnTreeNodeReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list spawn nodes: %w", err)
	}

	var total int
	_ = h.db.GetContext(ctx, &total, `
		SELECT COUNT(*) FROM agent_spawn_trees
		WHERE tree_id = $1::uuid AND tenant_id = $2
	`, qry.SessionID, qry.TenantID)

	return &SpawnTreeResult{Nodes: out, Total: total}, nil
}

// SpawnTreeResult wraps spawn tree query results.
type SpawnTreeResult struct {
	Nodes []SpawnTreeNodeReadModel `json:"nodes"`
	Total int                      `json:"total"`
}

// ============================================================================
// Agent Link Queries
// ============================================================================

// AgentLinkReadModel maps to agent_links table.
type AgentLinkReadModel struct {
	ID            string `db:"id" json:"id"`
	TenantID      string `db:"tenant_id" json:"tenant_id"`
	SourceAgentID string `db:"source_agent_id" json:"source_agent_id"`
	TargetType    string `db:"target_type" json:"target_type"`
	TargetID      string `db:"target_id" json:"target_id"`
	TargetName    string `db:"target_name" json:"target_name"`
	LinkType      string `db:"link_type" json:"link_type"`
	Protocol      string `db:"protocol" json:"protocol"`
	Status        string `db:"status" json:"status"`
	Config        []byte `db:"config" json:"config"`
	CreatedAt     string `db:"created_at" json:"created_at"`
	UpdatedAt     string `db:"updated_at" json:"updated_at"`
}

// AgentChannelBindingReadModel maps to agent_channel_bindings table.
type AgentChannelBindingReadModel struct {
	ID              string `db:"id" json:"id"`
	TenantID        string `db:"tenant_id" json:"tenant_id"`
	AgentID         string `db:"agent_id" json:"agent_id"`
	ChannelConfigID string `db:"channel_config_id" json:"channel_config_id"`
	Enabled         bool   `db:"enabled" json:"enabled"`
	CreatedAt       string `db:"created_at" json:"created_at"`
}

// ListAgentLinksQuery retrieves links for an agent.
type ListAgentLinksQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	AgentID  string `json:"agent_id"`
}

func NewListAgentLinksQuery(tenantID, agentID string) *ListAgentLinksQuery {
	return &ListAgentLinksQuery{TenantID: tenantID, AgentID: agentID}
}

func (q ListAgentLinksQuery) QueryType() string { return "ListAgentLinks" }
func (q ListAgentLinksQuery) Validate() error   { return nil }

// ListAgentLinksQueryHandler handles ListAgentLinks queries.
type ListAgentLinksQueryHandler struct{ db *sqlx.DB }

func NewListAgentLinksQueryHandler(db *sqlx.DB) *ListAgentLinksQueryHandler {
	return &ListAgentLinksQueryHandler{db: db}
}

func (h *ListAgentLinksQueryHandler) QueryType() string { return "ListAgentLinks" }

func (h *ListAgentLinksQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListAgentLinksQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListAgentLinksQuery")
	}

	var out []AgentLinkReadModel
	err := h.db.SelectContext(ctx, &out, `
		SELECT `+agentLinkColumns+` FROM agent_links
		WHERE source_agent_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
	`, qry.AgentID, qry.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent links: %w", err)
	}
	return out, nil
}

// ListAgentChannelBindingsQuery retrieves channel bindings for an agent.
type ListAgentChannelBindingsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	AgentID  string `json:"agent_id"`
}

func NewListAgentChannelBindingsQuery(tenantID, agentID string) *ListAgentChannelBindingsQuery {
	return &ListAgentChannelBindingsQuery{TenantID: tenantID, AgentID: agentID}
}

func (q ListAgentChannelBindingsQuery) QueryType() string { return "ListAgentChannelBindings" }
func (q ListAgentChannelBindingsQuery) Validate() error   { return nil }

// ListAgentChannelBindingsQueryHandler handles ListAgentChannelBindings queries.
type ListAgentChannelBindingsQueryHandler struct{ db *sqlx.DB }

func NewListAgentChannelBindingsQueryHandler(db *sqlx.DB) *ListAgentChannelBindingsQueryHandler {
	return &ListAgentChannelBindingsQueryHandler{db: db}
}

func (h *ListAgentChannelBindingsQueryHandler) QueryType() string { return "ListAgentChannelBindings" }

func (h *ListAgentChannelBindingsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListAgentChannelBindingsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListAgentChannelBindingsQuery")
	}

	var out []AgentChannelBindingReadModel
	err := h.db.SelectContext(ctx, &out, `
		SELECT `+channelBindingColumns+` FROM agent_channel_bindings
		WHERE agent_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
	`, qry.AgentID, qry.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent channel bindings: %w", err)
	}
	return out, nil
}
