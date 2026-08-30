package functions

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// FunctionReadModel maps to functions table
type FunctionReadModel struct {
	ID                   string         `db:"id" json:"id"`
	TenantID             string         `db:"tenant_id" json:"tenant_id"`
	Name                 string         `db:"name" json:"name"`
	Description          sql.NullString `db:"description" json:"description"`
	Mode                 string         `db:"mode" json:"mode"`
	Parameters           []byte         `db:"parameters" json:"parameters"`
	WebhookURL           sql.NullString `db:"webhook_url" json:"webhook_url"`
	WebhookMethod        sql.NullString `db:"webhook_method" json:"webhook_method"`
	WebhookHeaders       []byte         `db:"webhook_headers" json:"webhook_headers"`
	WebhookTimeoutMs     sql.NullInt32  `db:"webhook_timeout_ms" json:"webhook_timeout_ms"`
	ProxyBaseURL         sql.NullString `db:"proxy_base_url" json:"proxy_base_url"`
	ProxyPath            sql.NullString `db:"proxy_path" json:"proxy_path"`
	ProxyMethod          sql.NullString `db:"proxy_method" json:"proxy_method"`
	ProxyQueryMapping    []byte         `db:"proxy_query_mapping" json:"proxy_query_mapping"`
	ProxyHeaderMapping   []byte         `db:"proxy_header_mapping" json:"proxy_header_mapping"`
	ProxyBodyMapping     []byte         `db:"proxy_body_mapping" json:"proxy_body_mapping"`
	ProxyResponseMapping []byte         `db:"proxy_response_mapping" json:"proxy_response_mapping"`
	Runtime              sql.NullString `db:"runtime" json:"runtime"`
	Code                 sql.NullString `db:"code" json:"code"`
	Packages             pq.StringArray `db:"packages" json:"packages"`
	NetworkMode          sql.NullString `db:"network_mode" json:"network_mode"`
	AllowedHosts         pq.StringArray `db:"allowed_hosts" json:"allowed_hosts"`
	VCPUs                int32          `db:"vcpus" json:"vcpus"`
	DockerHost       sql.NullString `db:"docker_host" json:"docker_host"`
	TimeoutMs            int32          `db:"timeout_ms" json:"timeout_ms"`
	MemoryMB             int32          `db:"memory_mb" json:"memory_mb"`
	MaxRetries           int32          `db:"max_retries" json:"max_retries"`
	Enabled              bool           `db:"enabled" json:"enabled"`
	CreatedAt            string         `db:"created_at" json:"created_at"`
	UpdatedAt            string         `db:"updated_at" json:"updated_at"`
}

// GetFunctionByIDQuery retrieves a function by ID
type GetFunctionByIDQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetFunctionByIDQuery(id, tenantID string) *GetFunctionByIDQuery {
	return &GetFunctionByIDQuery{
		BaseQuery: query.BaseQuery{},
		ID:        id,
		TenantID:  tenantID,
	}
}

func (q GetFunctionByIDQuery) QueryType() string { return "GetFunctionByID" }

func (q GetFunctionByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	// tenant_id is optional for self-hosted mode - will use "default" if empty
	return nil
}

// GetFunctionByNameQuery retrieves a function by name
type GetFunctionByNameQuery struct {
	query.BaseQuery
	Name     string `json:"name"`
	TenantID string `json:"tenant_id"`
}

func NewGetFunctionByNameQuery(name, tenantID string) *GetFunctionByNameQuery {
	return &GetFunctionByNameQuery{
		BaseQuery: query.BaseQuery{},
		Name:      name,
		TenantID:  tenantID,
	}
}

func (q GetFunctionByNameQuery) QueryType() string { return "GetFunctionByName" }

func (q GetFunctionByNameQuery) Validate() error {
	if q.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	// tenant_id is optional for self-hosted mode - will use "default" if empty
	return nil
}

// ListFunctionsQuery retrieves all functions for a tenant
type ListFunctionsQuery struct {
	query.BaseQuery
	TenantID string  `json:"tenant_id"`
	Mode     *string `json:"mode,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
	Limit    int     `json:"limit,omitempty"`
	Offset   int     `json:"offset,omitempty"`
}

func NewListFunctionsQuery(tenantID string, mode *string, enabled *bool, limit, offset int) *ListFunctionsQuery {
	return &ListFunctionsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		Mode:      mode,
		Enabled:   enabled,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListFunctionsQuery) QueryType() string { return "ListFunctions" }

func (q ListFunctionsQuery) Validate() error {
	// tenant_id is optional for self-hosted mode - will use "default" if empty
	return nil
}

// FunctionsQueryHandler handles GetFunctionByID queries
type FunctionsQueryHandler struct {
	db *sqlx.DB
}

func NewFunctionsQueryHandler(db *sqlx.DB) *FunctionsQueryHandler {
	return &FunctionsQueryHandler{db: db}
}

func (h *FunctionsQueryHandler) QueryType() string { return "GetFunctionByID" }

func (h *FunctionsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetFunctionByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetFunctionByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get function by id query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out FunctionReadModel
	err := h.db.GetContext(ctx, &out, `
		SELECT * FROM functions
		WHERE id = $1 AND tenant_id = $2
	`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get function by id query")
		return nil, fmt.Errorf("failed to get function: %w", err)
	}
	return &out, nil
}

// FunctionByNameQueryHandler handles GetFunctionByName queries
type FunctionByNameQueryHandler struct {
	db *sqlx.DB
}

func NewFunctionByNameQueryHandler(db *sqlx.DB) *FunctionByNameQueryHandler {
	return &FunctionByNameQueryHandler{db: db}
}

func (h *FunctionByNameQueryHandler) QueryType() string { return "GetFunctionByName" }

func (h *FunctionByNameQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetFunctionByNameQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetFunctionByNameQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"name", qry.Name,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get function by name query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out FunctionReadModel
	err := h.db.GetContext(ctx, &out, `
		SELECT * FROM functions
		WHERE name = $1 AND tenant_id = $2 AND enabled = true
	`, qry.Name, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get function by name query")
		return nil, fmt.Errorf("failed to get function: %w", err)
	}
	return &out, nil
}

// ListFunctionsQueryHandler handles ListFunctions queries
type ListFunctionsQueryHandler struct {
	db *sqlx.DB
}

func NewListFunctionsQueryHandler(db *sqlx.DB) *ListFunctionsQueryHandler {
	return &ListFunctionsQueryHandler{db: db}
}

func (h *ListFunctionsQueryHandler) QueryType() string { return "ListFunctions" }

func (h *ListFunctionsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListFunctionsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListFunctionsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list functions query")

	if qry.TenantID == "" {
		return []FunctionReadModel{}, nil
	}

	queryStr := `SELECT * FROM functions WHERE tenant_id = $1`
	args := []interface{}{qry.TenantID}
	argIndex := 2

	if qry.Mode != nil {
		queryStr += fmt.Sprintf(" AND mode = $%d", argIndex)
		args = append(args, *qry.Mode)
		argIndex++
	}

	if qry.Enabled != nil {
		queryStr += fmt.Sprintf(" AND enabled = $%d", argIndex)
		args = append(args, *qry.Enabled)
		argIndex++
	}

	queryStr += " ORDER BY created_at DESC"

	if qry.Limit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.Limit)
		argIndex++
	}

	if qry.Offset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.Offset)
	}

	var out []FunctionReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list functions query")
		return nil, fmt.Errorf("failed to list functions: %w", err)
	}

	logger.WithFields(
		"count", len(out),
		"query", queryStr,
	).Info("functions: list query completed")

	return out, nil
}
