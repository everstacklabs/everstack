package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// WorkflowReadModel maps to workflows table
type WorkflowReadModel struct {
	ID          string         `db:"id" json:"id"`
	TenantID    string         `db:"tenant_id" json:"tenant_id"`
	Name        string         `db:"name" json:"name"`
	Description sql.NullString `db:"description" json:"description"`
	Nodes       []byte         `db:"nodes" json:"nodes"`       // JSONB
	Edges       []byte         `db:"edges" json:"edges"`       // JSONB
	Viewport    []byte         `db:"viewport" json:"viewport"` // JSONB
	Enabled     bool           `db:"enabled" json:"enabled"`
	Version     int32          `db:"version" json:"version"`
	CreatedAt   string         `db:"created_at" json:"created_at"`
	UpdatedAt   string         `db:"updated_at" json:"updated_at"`
}

// GetWorkflowByIDQuery retrieves a workflow by ID
type GetWorkflowByIDQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetWorkflowByIDQuery(id, tenantID string) *GetWorkflowByIDQuery {
	return &GetWorkflowByIDQuery{
		BaseQuery: query.BaseQuery{},
		ID:        id,
		TenantID:  tenantID,
	}
}

func (q GetWorkflowByIDQuery) QueryType() string { return "GetWorkflowByID" }

func (q GetWorkflowByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListWorkflowsQuery retrieves all workflows for a tenant
type ListWorkflowsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

func NewListWorkflowsQuery(tenantID string, enabled *bool, limit, offset int) *ListWorkflowsQuery {
	return &ListWorkflowsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		Enabled:   enabled,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListWorkflowsQuery) QueryType() string { return "ListWorkflows" }

func (q ListWorkflowsQuery) Validate() error {
	return nil
}

// WorkflowsQueryHandler handles GetWorkflowByID queries
type WorkflowsQueryHandler struct {
	db *sqlx.DB
}

func NewWorkflowsQueryHandler(db *sqlx.DB) *WorkflowsQueryHandler {
	return &WorkflowsQueryHandler{db: db}
}

func (h *WorkflowsQueryHandler) QueryType() string { return "GetWorkflowByID" }

func (h *WorkflowsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetWorkflowByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetWorkflowByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get workflow by id query")

	var out WorkflowReadModel
	var err error

	if qry.TenantID == "" {
		// Pre-fix this fetched by id alone; combined with shared
		// `everstack.workflows`, that meant any tenant could read
		// any other tenant's workflow definition (nodes, edges,
		// system prompts) by id.
		return nil, nil
	}
	err = h.db.GetContext(ctx, &out, `
		SELECT * FROM workflows
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
		).Error("failed to execute get workflow by id query")
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}
	return &out, nil
}

// ListWorkflowsQueryHandler handles ListWorkflows queries
type ListWorkflowsQueryHandler struct {
	db *sqlx.DB
}

func NewListWorkflowsQueryHandler(db *sqlx.DB) *ListWorkflowsQueryHandler {
	return &ListWorkflowsQueryHandler{db: db}
}

func (h *ListWorkflowsQueryHandler) QueryType() string { return "ListWorkflows" }

func (h *ListWorkflowsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListWorkflowsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListWorkflowsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list workflows query")

	// Pre-fix this handler ran `WHERE 1=1` and ignored qry.TenantID
	// entirely — every tenant's workflows leaked into every studio
	// page. Same Pattern B shape as the agents/sessions/approval
	// review query handlers fixed in #44.
	if qry.TenantID == "" {
		return []WorkflowReadModel{}, nil
	}

	var queryStr string
	var args []interface{}
	var argIndex int

	queryStr = `SELECT * FROM workflows WHERE tenant_id = $1`
	args = []interface{}{qry.TenantID}
	argIndex = 2

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

	var out []WorkflowReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list workflows query")
		return nil, fmt.Errorf("failed to list workflows: %w", err)
	}

	logger.WithFields(
		"count", len(out),
	).Debug("workflows: list query completed")

	return out, nil
}

// WorkflowVersionEntryReadModel maps to rows from events for version history
type WorkflowVersionEntryReadModel struct {
	EventType string `db:"type"`
	Payload   []byte `db:"payload"`
	CreatedAt int64  `db:"created_at"`
}

// GetWorkflowVersionHistoryQuery retrieves version history events for a workflow
type GetWorkflowVersionHistoryQuery struct {
	query.BaseQuery
	WorkflowID string `json:"workflow_id"`
	TenantID   string `json:"tenant_id"`
}

func NewGetWorkflowVersionHistoryQuery(workflowID, tenantID string) *GetWorkflowVersionHistoryQuery {
	return &GetWorkflowVersionHistoryQuery{
		BaseQuery:  query.BaseQuery{},
		WorkflowID: workflowID,
		TenantID:   tenantID,
	}
}

func (q GetWorkflowVersionHistoryQuery) QueryType() string { return "GetWorkflowVersionHistory" }

func (q GetWorkflowVersionHistoryQuery) Validate() error {
	if q.WorkflowID == "" {
		return fmt.Errorf("workflow_id cannot be empty")
	}
	return nil
}

// WorkflowVersionHistoryQueryHandler handles GetWorkflowVersionHistory queries
type WorkflowVersionHistoryQueryHandler struct {
	db      *sqlx.DB
	dialect string // "postgres" or "clickhouse"
}

func NewWorkflowVersionHistoryQueryHandler(db *sqlx.DB, dialect string) *WorkflowVersionHistoryQueryHandler {
	return &WorkflowVersionHistoryQueryHandler{db: db, dialect: dialect}
}

func (h *WorkflowVersionHistoryQueryHandler) QueryType() string {
	return "GetWorkflowVersionHistory"
}

// GetWorkflowAtVersionQuery retrieves events to reconstruct a workflow at a given version
type GetWorkflowAtVersionQuery struct {
	query.BaseQuery
	WorkflowID string `json:"workflow_id"`
	TenantID   string `json:"tenant_id"`
	Version    int32  `json:"version"`
}

func NewGetWorkflowAtVersionQuery(workflowID, tenantID string, version int32) *GetWorkflowAtVersionQuery {
	return &GetWorkflowAtVersionQuery{
		BaseQuery:  query.BaseQuery{},
		WorkflowID: workflowID,
		TenantID:   tenantID,
		Version:    version,
	}
}

func (q GetWorkflowAtVersionQuery) QueryType() string { return "GetWorkflowAtVersion" }

func (q GetWorkflowAtVersionQuery) Validate() error {
	if q.WorkflowID == "" {
		return fmt.Errorf("workflow_id cannot be empty")
	}
	if q.Version < 1 {
		return fmt.Errorf("version must be >= 1")
	}
	return nil
}

// GetWorkflowAtVersionResult contains the events needed to reconstruct a workflow at a version.
type GetWorkflowAtVersionResult struct {
	Events  []WorkflowVersionEntryReadModel
	Version int32
}

// WorkflowAtVersionQueryHandler handles GetWorkflowAtVersion queries
type WorkflowAtVersionQueryHandler struct {
	db      *sqlx.DB
	dialect string
}

func NewWorkflowAtVersionQueryHandler(db *sqlx.DB, dialect string) *WorkflowAtVersionQueryHandler {
	return &WorkflowAtVersionQueryHandler{db: db, dialect: dialect}
}

func (h *WorkflowAtVersionQueryHandler) QueryType() string { return "GetWorkflowAtVersion" }

func (h *WorkflowAtVersionQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetWorkflowAtVersionQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetWorkflowAtVersionQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"workflow_id", qry.WorkflowID,
		"tenant_id", qry.TenantID,
		"version", qry.Version,
		"correlation_id", correlationID,
	).Debug("executing get workflow at version query")

	var out []WorkflowVersionEntryReadModel
	var err error

	if h.dialect == "clickhouse" {
		var all []WorkflowVersionEntryReadModel
		err = h.db.SelectContext(ctx, &all, `
			SELECT type, payload, created_at FROM events
			WHERE stream = 'workflows'
			  AND type IN ('workflow.created', 'workflow.updated')
			ORDER BY created_at ASC
		`)
		if err == nil {
			for _, evt := range all {
				var m map[string]interface{}
				if json.Unmarshal(evt.Payload, &m) != nil {
					continue
				}
				evtID, _ := m["id"].(string)
				if evtID == qry.WorkflowID {
					out = append(out, evt)
					if int32(len(out)) >= qry.Version {
						break
					}
				}
			}
		}
	} else {
		err = h.db.SelectContext(ctx, &out, `
			SELECT type, payload, created_at FROM events
			WHERE stream = 'workflows'
			  AND type IN ('workflow.created', 'workflow.updated')
			  AND payload->>'id' = $1
			ORDER BY created_at ASC
			LIMIT $2
		`, qry.WorkflowID, qry.Version)
	}

	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get workflow at version query")
		return nil, fmt.Errorf("failed to get workflow at version: %w", err)
	}

	return &GetWorkflowAtVersionResult{
		Events:  out,
		Version: qry.Version,
	}, nil
}

func (h *WorkflowVersionHistoryQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetWorkflowVersionHistoryQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetWorkflowVersionHistoryQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"workflow_id", qry.WorkflowID,
		"tenant_id", qry.TenantID,
		"dialect", h.dialect,
		"correlation_id", correlationID,
	).Debug("executing get workflow version history query")

	var out []WorkflowVersionEntryReadModel
	var err error

	if h.dialect == "clickhouse" {
		// ClickHouse: existing payloads were stored as binary []byte so
		// JSONExtractString cannot parse them. Fetch all workflow events
		// for the stream and filter by ID/tenant in Go.
		var all []WorkflowVersionEntryReadModel
		err = h.db.SelectContext(ctx, &all, `
			SELECT type, payload, created_at FROM events
			WHERE stream = 'workflows'
			  AND type IN ('workflow.created', 'workflow.updated')
			ORDER BY created_at ASC
		`)
		if err == nil {
			for _, evt := range all {
				var m map[string]interface{}
				if json.Unmarshal(evt.Payload, &m) != nil {
					continue
				}
				evtID, _ := m["id"].(string)
				if evtID == qry.WorkflowID {
					out = append(out, evt)
				}
			}
		}
	} else {
		err = h.db.SelectContext(ctx, &out, `
			SELECT type, payload, created_at FROM events
			WHERE stream = 'workflows'
			  AND type IN ('workflow.created', 'workflow.updated')
			  AND payload->>'id' = $1
			ORDER BY created_at ASC
		`, qry.WorkflowID)
	}

	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get workflow version history query")
		return nil, fmt.Errorf("failed to get workflow version history: %w", err)
	}

	return out, nil
}
