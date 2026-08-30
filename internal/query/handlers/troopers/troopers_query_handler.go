package troopers

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// ============================================================================
// Read Models
// ============================================================================

// TrooperReadModel maps to troopers table.
type TrooperReadModel struct {
	ID                    string         `db:"id"`
	TenantID              string         `db:"tenant_id"`
	Name                  string         `db:"name"`
	Description           sql.NullString `db:"description"`
	Status                string         `db:"status"`
	Model                 string         `db:"model"`
	SystemPrompt          sql.NullString `db:"system_prompt"`
	Tools                 pq.StringArray `db:"tools"`
	AgentConfig           []byte         `db:"agent_config"`
	MaxTurns              int32          `db:"max_turns"`
	MaxToolCallsPerTurn   int32          `db:"max_tool_calls_per_turn"`
	MaxSteps              sql.NullInt32  `db:"max_steps"`
	SoulMD                string         `db:"soul_md"`
	IdentityMD            string         `db:"identity_md"`
	UserMD                string         `db:"user_md"`
	RoleMD                string         `db:"role_md"`
	SandboxImage          string         `db:"sandbox_image"`
	SandboxCPULimit       float64        `db:"sandbox_cpu_limit"`
	SandboxMemoryMB       int32          `db:"sandbox_memory_mb"`
	SandboxDiskMB         int32          `db:"sandbox_disk_mb"`
	SandboxTimeoutSeconds int32          `db:"sandbox_timeout_seconds"`
	SandboxNetworkMode    string         `db:"sandbox_network_mode"`
	SandboxAllowedHosts   pq.StringArray `db:"sandbox_allowed_hosts"`
	SandboxEnvVars        []byte         `db:"sandbox_env_vars"`
	SandboxSSHEnabled     bool           `db:"sandbox_ssh_enabled"`
	SandboxGitRepoURL     sql.NullString `db:"sandbox_git_repo_url"`
	SandboxGitBranch      sql.NullString `db:"sandbox_git_branch"`
	DBSqlitePath          string         `db:"db_sqlite_path"`
	DBLanceDBPath         string         `db:"db_lancedb_path"`
	DBRedbPath            string         `db:"db_redb_path"`
	MaxConcurrentWorkers  int32          `db:"max_concurrent_workers"`
	WorkerPoolConfig      []byte         `db:"worker_pool_config"`
	Color                 sql.NullString `db:"color"`
	Icon                  sql.NullString `db:"icon"`
	SandboxID             sql.NullString `db:"sandbox_id"`
	CreatedAt             string         `db:"created_at"`
	UpdatedAt             string         `db:"updated_at"`
	DeletedAt             sql.NullString `db:"deleted_at"`
}

// TrooperLinkReadModel maps to trooper_links table.
type TrooperLinkReadModel struct {
	ID                string         `db:"id"`
	TenantID          string         `db:"tenant_id"`
	SourceTrooperID string         `db:"source_trooper_id"`
	TargetType        string         `db:"target_type"`
	TargetID          string         `db:"target_id"`
	TargetName        sql.NullString `db:"target_name"`
	LinkType          string         `db:"link_type"`
	Protocol          string         `db:"protocol"`
	Status            string         `db:"status"`
	Config            []byte         `db:"config"`
	CreatedAt         string         `db:"created_at"`
	UpdatedAt         string         `db:"updated_at"`
}

// TrooperChannelBindingReadModel maps to trooper_channel_bindings table.
type TrooperChannelBindingReadModel struct {
	ID              string `db:"id"`
	TenantID        string `db:"tenant_id"`
	TrooperID     string `db:"trooper_id"`
	ChannelConfigID string `db:"channel_config_id"`
	Enabled         bool   `db:"enabled"`
	CreatedAt       string `db:"created_at"`
}

// ============================================================================
// Query Types
// ============================================================================

// GetTrooperByIDQuery retrieves a trooper by ID.
type GetTrooperByIDQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetTrooperByIDQuery(id, tenantID string) *GetTrooperByIDQuery {
	return &GetTrooperByIDQuery{ID: id, TenantID: tenantID}
}

func (q GetTrooperByIDQuery) QueryType() string { return "GetTrooperByID" }
func (q GetTrooperByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListTroopersQuery retrieves all troopers for a tenant.
type ListTroopersQuery struct {
	query.BaseQuery
	TenantID string  `json:"tenant_id"`
	Status   *string `json:"status,omitempty"`
	QLimit   int     `json:"limit,omitempty"`
	QOffset  int     `json:"offset,omitempty"`
}

func NewListTroopersQuery(tenantID string, status *string, limit, offset int) *ListTroopersQuery {
	return &ListTroopersQuery{
		TenantID: tenantID,
		Status:   status,
		QLimit:   limit,
		QOffset:  offset,
	}
}

func (q ListTroopersQuery) QueryType() string { return "ListTroopers" }
func (q ListTroopersQuery) Validate() error   { return nil }

// ListTrooperLinksQuery retrieves links for a trooper.
type ListTrooperLinksQuery struct {
	query.BaseQuery
	TenantID    string `json:"tenant_id"`
	TrooperID string `json:"trooper_id"`
}

func NewListTrooperLinksQuery(tenantID, trooperID string) *ListTrooperLinksQuery {
	return &ListTrooperLinksQuery{TenantID: tenantID, TrooperID: trooperID}
}

func (q ListTrooperLinksQuery) QueryType() string { return "ListTrooperLinks" }
func (q ListTrooperLinksQuery) Validate() error {
	if q.TrooperID == "" {
		return fmt.Errorf("trooper_id cannot be empty")
	}
	return nil
}

// ListChannelBindingsQuery retrieves channel bindings for a trooper.
type ListChannelBindingsQuery struct {
	query.BaseQuery
	TenantID    string `json:"tenant_id"`
	TrooperID string `json:"trooper_id"`
}

func NewListChannelBindingsQuery(tenantID, trooperID string) *ListChannelBindingsQuery {
	return &ListChannelBindingsQuery{TenantID: tenantID, TrooperID: trooperID}
}

func (q ListChannelBindingsQuery) QueryType() string { return "ListChannelBindings" }
func (q ListChannelBindingsQuery) Validate() error {
	if q.TrooperID == "" {
		return fmt.Errorf("trooper_id cannot be empty")
	}
	return nil
}

// ============================================================================
// Result Wrappers
// ============================================================================

// TroopersResult wraps trooper results with total count.
type TroopersResult struct {
	Troopers []TrooperReadModel `json:"troopers"`
	Total      int                  `json:"total"`
}

// ============================================================================
// Query Handlers
// ============================================================================

// TrooperByIDQueryHandler handles GetTrooperByID queries.
type TrooperByIDQueryHandler struct{ db *sqlx.DB }

func NewTrooperByIDQueryHandler(db *sqlx.DB) *TrooperByIDQueryHandler {
	return &TrooperByIDQueryHandler{db: db}
}

func (h *TrooperByIDQueryHandler) QueryType() string { return "GetTrooperByID" }

func (h *TrooperByIDQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetTrooperByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetTrooperByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get trooper by id query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out TrooperReadModel
	err := h.db.GetContext(ctx, &out, `
		SELECT * FROM troopers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get trooper: %w", err)
	}
	return &out, nil
}

// ListTroopersQueryHandler handles ListTroopers queries.
type ListTroopersQueryHandler struct{ db *sqlx.DB }

func NewListTroopersQueryHandler(db *sqlx.DB) *ListTroopersQueryHandler {
	return &ListTroopersQueryHandler{db: db}
}

func (h *ListTroopersQueryHandler) QueryType() string { return "ListTroopers" }

func (h *ListTroopersQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListTroopersQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListTroopersQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list troopers query")

	if qry.TenantID == "" {
		return &TroopersResult{Troopers: []TrooperReadModel{}, Total: 0}, nil
	}

	queryStr := `SELECT * FROM troopers WHERE tenant_id = $1 AND deleted_at IS NULL`
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

	var out []TrooperReadModel
	if err := h.db.SelectContext(ctx, &out, queryStr, args...); err != nil {
		return nil, fmt.Errorf("failed to list troopers: %w", err)
	}

	countQuery := `SELECT COUNT(*) FROM troopers WHERE tenant_id = $1 AND deleted_at IS NULL`
	countArgs := []interface{}{qry.TenantID}
	countArgIndex := 2
	if qry.Status != nil {
		countQuery += fmt.Sprintf(" AND status = $%d", countArgIndex)
		countArgs = append(countArgs, *qry.Status)
	}
	var total int
	_ = h.db.GetContext(ctx, &total, countQuery, countArgs...)

	return &TroopersResult{Troopers: out, Total: total}, nil
}

// ListTrooperLinksQueryHandler handles ListTrooperLinks queries.
type ListTrooperLinksQueryHandler struct{ db *sqlx.DB }

func NewListTrooperLinksQueryHandler(db *sqlx.DB) *ListTrooperLinksQueryHandler {
	return &ListTrooperLinksQueryHandler{db: db}
}

func (h *ListTrooperLinksQueryHandler) QueryType() string { return "ListTrooperLinks" }

func (h *ListTrooperLinksQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListTrooperLinksQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListTrooperLinksQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"trooper_id", qry.TrooperID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list trooper links query")

	if qry.TenantID == "" {
		return []TrooperLinkReadModel{}, nil
	}

	var out []TrooperLinkReadModel
	err := h.db.SelectContext(ctx, &out, `
		SELECT * FROM trooper_links WHERE source_trooper_id = $1 AND tenant_id = $2 ORDER BY created_at DESC
	`, qry.TrooperID, qry.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list trooper links: %w", err)
	}
	return out, nil
}

// ListChannelBindingsQueryHandler handles ListChannelBindings queries.
type ListChannelBindingsQueryHandler struct{ db *sqlx.DB }

func NewListChannelBindingsQueryHandler(db *sqlx.DB) *ListChannelBindingsQueryHandler {
	return &ListChannelBindingsQueryHandler{db: db}
}

func (h *ListChannelBindingsQueryHandler) QueryType() string { return "ListChannelBindings" }

func (h *ListChannelBindingsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListChannelBindingsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListChannelBindingsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"trooper_id", qry.TrooperID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list channel bindings query")

	if qry.TenantID == "" {
		return []TrooperChannelBindingReadModel{}, nil
	}

	var out []TrooperChannelBindingReadModel
	err := h.db.SelectContext(ctx, &out, `
		SELECT * FROM trooper_channel_bindings WHERE trooper_id = $1 AND tenant_id = $2 ORDER BY created_at DESC
	`, qry.TrooperID, qry.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list channel bindings: %w", err)
	}
	return out, nil
}
