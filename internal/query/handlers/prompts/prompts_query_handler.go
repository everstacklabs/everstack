package prompts

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/jmoiron/sqlx"
)

// --- Read Models ---

// PromptReadModel maps to the prompts table plus version/label aggregates.
type PromptReadModel struct {
	ID            string         `db:"id" json:"id"`
	TenantID      string         `db:"tenant_id" json:"tenant_id"`
	Name          string         `db:"name" json:"name"`
	Description   string         `db:"description" json:"description"`
	Tags          []byte         `db:"tags" json:"tags"`
	LatestVersion int32          `db:"latest_version" json:"latest_version"`
	VersionCount  int32          `db:"version_count" json:"version_count"`
	Labels        []byte         `db:"labels" json:"labels"`
	CreatedAt     string         `db:"created_at" json:"created_at"`
	UpdatedAt     string         `db:"updated_at" json:"updated_at"`
	ArchivedAt    sql.NullString `db:"archived_at" json:"archived_at"`
}

// PromptVersionReadModel maps to the prompt_versions table.
type PromptVersionReadModel struct {
	ID            string `db:"id" json:"id"`
	PromptID      string `db:"prompt_id" json:"prompt_id"`
	TenantID      string `db:"tenant_id" json:"tenant_id"`
	Version       int32  `db:"version" json:"version"`
	Messages      []byte `db:"messages" json:"messages"`
	Config        []byte `db:"config" json:"config"`
	Labels        []byte `db:"labels" json:"labels"`
	CommitMessage string `db:"commit_message" json:"commit_message"`
	CreatedBy     string `db:"created_by" json:"created_by"`
	CreatedAt     string `db:"created_at" json:"created_at"`
}

// promptSelect joins version count / latest version / label map onto prompts.
const promptSelect = `
	SELECT p.id, p.tenant_id, p.name, p.description, p.tags,
		p.created_at, p.updated_at, p.archived_at,
		COALESCE(v.latest_version, 0) AS latest_version,
		COALESCE(v.version_count, 0) AS version_count,
		COALESCE(l.labels, '{}'::jsonb) AS labels
	FROM prompts p
	LEFT JOIN (
		SELECT prompt_id, MAX(version) AS latest_version, COUNT(*) AS version_count
		FROM prompt_versions GROUP BY prompt_id
	) v ON v.prompt_id = p.id
	LEFT JOIN (
		SELECT pv.prompt_id, jsonb_object_agg(elem, pv.version) AS labels
		FROM prompt_versions pv, jsonb_array_elements_text(pv.labels) AS elem
		GROUP BY pv.prompt_id
	) l ON l.prompt_id = p.id
`

// --- Queries ---

// GetPromptQuery retrieves a prompt by id or name.
type GetPromptQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	Name     string `json:"name"`
	TenantID string `json:"tenant_id"`
}

func NewGetPromptQuery(id, name, tenantID string) *GetPromptQuery {
	return &GetPromptQuery{BaseQuery: query.BaseQuery{}, ID: id, Name: name, TenantID: tenantID}
}

func (q GetPromptQuery) QueryType() string { return "GetPrompt" }
func (q GetPromptQuery) Validate() error {
	if q.ID == "" && q.Name == "" {
		return fmt.Errorf("id or name is required")
	}
	return nil
}

// ListPromptsQuery retrieves all prompts for a tenant.
type ListPromptsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

func NewListPromptsQuery(tenantID string, limit, offset int) *ListPromptsQuery {
	return &ListPromptsQuery{BaseQuery: query.BaseQuery{}, TenantID: tenantID, Limit: limit, Offset: offset}
}

func (q ListPromptsQuery) QueryType() string { return "ListPrompts" }
func (q ListPromptsQuery) Validate() error   { return nil }

// ListPromptVersionsQuery retrieves versions of a prompt, newest first.
type ListPromptVersionsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	PromptID string `json:"prompt_id"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

func NewListPromptVersionsQuery(tenantID, promptID string, limit, offset int) *ListPromptVersionsQuery {
	return &ListPromptVersionsQuery{BaseQuery: query.BaseQuery{}, TenantID: tenantID, PromptID: promptID, Limit: limit, Offset: offset}
}

func (q ListPromptVersionsQuery) QueryType() string { return "ListPromptVersions" }
func (q ListPromptVersionsQuery) Validate() error {
	if q.PromptID == "" {
		return fmt.Errorf("prompt_id cannot be empty")
	}
	return nil
}

// GetPromptVersionQuery resolves a single version by number, by label, or
// latest when neither is set.
type GetPromptVersionQuery struct {
	query.BaseQuery
	TenantID string  `json:"tenant_id"`
	PromptID string  `json:"prompt_id"`
	Version  *int    `json:"version,omitempty"`
	Label    *string `json:"label,omitempty"`
}

func NewGetPromptVersionQuery(tenantID, promptID string, version *int, label *string) *GetPromptVersionQuery {
	return &GetPromptVersionQuery{BaseQuery: query.BaseQuery{}, TenantID: tenantID, PromptID: promptID, Version: version, Label: label}
}

func (q GetPromptVersionQuery) QueryType() string { return "GetPromptVersion" }
func (q GetPromptVersionQuery) Validate() error {
	if q.PromptID == "" {
		return fmt.Errorf("prompt_id cannot be empty")
	}
	return nil
}

// --- Query Handlers ---

// GetPromptQueryHandler handles GetPrompt queries.
type GetPromptQueryHandler struct {
	db *sqlx.DB
}

func NewGetPromptQueryHandler(db *sqlx.DB) *GetPromptQueryHandler {
	return &GetPromptQueryHandler{db: db}
}

func (h *GetPromptQueryHandler) QueryType() string { return "GetPrompt" }

func (h *GetPromptQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetPromptQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetPromptQuery")
	}

	if qry.TenantID == "" {
		return nil, nil
	}

	queryStr := promptSelect + ` WHERE p.tenant_id = $1 AND `
	args := []interface{}{qry.TenantID}
	if qry.ID != "" {
		queryStr += `p.id = $2`
		args = append(args, qry.ID)
	} else {
		queryStr += `p.name = $2`
		args = append(args, qry.Name)
	}

	var out PromptReadModel
	err := h.db.GetContext(ctx, &out, queryStr, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlation.GetCorrelationID(ctx),
		).Error("failed to execute get prompt query")
		return nil, fmt.Errorf("failed to get prompt: %w", err)
	}
	return &out, nil
}

// ListPromptsQueryHandler handles ListPrompts queries.
type ListPromptsQueryHandler struct {
	db *sqlx.DB
}

func NewListPromptsQueryHandler(db *sqlx.DB) *ListPromptsQueryHandler {
	return &ListPromptsQueryHandler{db: db}
}

func (h *ListPromptsQueryHandler) QueryType() string { return "ListPrompts" }

func (h *ListPromptsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListPromptsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListPromptsQuery")
	}

	if qry.TenantID == "" {
		return []PromptReadModel{}, nil
	}

	queryStr := promptSelect + ` WHERE p.tenant_id = $1 AND p.archived_at IS NULL ORDER BY p.updated_at DESC`
	args := []interface{}{qry.TenantID}
	argIndex := 2

	if qry.Limit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.Limit)
		argIndex++
	}
	if qry.Offset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.Offset)
	}

	var out []PromptReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlation.GetCorrelationID(ctx),
		).Error("failed to execute list prompts query")
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}
	return out, nil
}

// ListPromptVersionsQueryHandler handles ListPromptVersions queries.
type ListPromptVersionsQueryHandler struct {
	db *sqlx.DB
}

func NewListPromptVersionsQueryHandler(db *sqlx.DB) *ListPromptVersionsQueryHandler {
	return &ListPromptVersionsQueryHandler{db: db}
}

func (h *ListPromptVersionsQueryHandler) QueryType() string { return "ListPromptVersions" }

func (h *ListPromptVersionsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListPromptVersionsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListPromptVersionsQuery")
	}

	if qry.TenantID == "" {
		return []PromptVersionReadModel{}, nil
	}

	queryStr := `SELECT * FROM prompt_versions WHERE prompt_id = $1 AND tenant_id = $2 ORDER BY version DESC`
	args := []interface{}{qry.PromptID, qry.TenantID}
	argIndex := 3

	if qry.Limit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.Limit)
		argIndex++
	}
	if qry.Offset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.Offset)
	}

	var out []PromptVersionReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlation.GetCorrelationID(ctx),
		).Error("failed to execute list prompt versions query")
		return nil, fmt.Errorf("failed to list prompt versions: %w", err)
	}
	return out, nil
}

// GetPromptVersionQueryHandler handles GetPromptVersion queries.
type GetPromptVersionQueryHandler struct {
	db *sqlx.DB
}

func NewGetPromptVersionQueryHandler(db *sqlx.DB) *GetPromptVersionQueryHandler {
	return &GetPromptVersionQueryHandler{db: db}
}

func (h *GetPromptVersionQueryHandler) QueryType() string { return "GetPromptVersion" }

func (h *GetPromptVersionQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetPromptVersionQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetPromptVersionQuery")
	}

	if qry.TenantID == "" {
		return nil, nil
	}

	queryStr := `SELECT * FROM prompt_versions WHERE prompt_id = $1 AND tenant_id = $2`
	args := []interface{}{qry.PromptID, qry.TenantID}

	switch {
	case qry.Version != nil:
		queryStr += ` AND version = $3`
		args = append(args, *qry.Version)
	case qry.Label != nil:
		queryStr += ` AND labels ? $3`
		args = append(args, *qry.Label)
	default:
		queryStr += ` ORDER BY version DESC LIMIT 1`
	}

	var out PromptVersionReadModel
	err := h.db.GetContext(ctx, &out, queryStr, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlation.GetCorrelationID(ctx),
		).Error("failed to execute get prompt version query")
		return nil, fmt.Errorf("failed to get prompt version: %w", err)
	}
	return &out, nil
}
