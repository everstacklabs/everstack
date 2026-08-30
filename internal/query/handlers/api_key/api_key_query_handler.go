package apikey

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// GetApiKeyByIDQuery retrieves an API key by its ID. Scoping to the caller's
// org id is required: without it any tenant could read another tenant's key
// metadata by guessing the id (small UUID space, predictable when keys are
// created in batches). The handler enforces the predicate even if a caller
// forgets, so this is defense-in-depth not just an API tweak.
type GetApiKeyByIDQuery struct {
	query.BaseQuery
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
}

func NewGetApiKeyByIDQuery(id, orgID, traceID string) *GetApiKeyByIDQuery {
	return &GetApiKeyByIDQuery{
		BaseQuery: query.BaseQuery{},
		ID:        id,
		OrgID:     orgID,
	}
}

func (q GetApiKeyByIDQuery) QueryType() string { return "GetApiKeyByID" }

func (q GetApiKeyByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	if q.OrgID == "" {
		return fmt.Errorf("org id is required")
	}
	return nil
}

// APIKeyReadModel maps to api_keys
type APIKeyReadModel struct {
	ID          string  `db:"id" json:"id"`
	Name        string  `db:"name" json:"name"`
	Hash        string  `db:"hash" json:"hash"`
	Type        string  `db:"type" json:"type"`
	SensitiveID string  `db:"sensitive_id" json:"sensitive_id"`
	OrgID       *string `db:"org_id" json:"org_id,omitempty"`
	InstanceID  *string `db:"instance_id" json:"instance_id,omitempty"`
	CreatedAt   string  `db:"created_at" json:"created_at"`
	UpdatedAt   string  `db:"updated_at" json:"updated_at"`
}

// ApiKeyQueryHandler handles GetApiKeyByID queries.
type ApiKeyQueryHandler struct {
	db *sqlx.DB
}

func NewApiKeyQueryHandler(db *sqlx.DB) *ApiKeyQueryHandler { return &ApiKeyQueryHandler{db: db} }

func (h *ApiKeyQueryHandler) QueryType() string { return "GetApiKeyByID" }

func (h *ApiKeyQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetApiKeyByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetApiKeyByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"correlation_id", correlationID,
	).Debug("executing get api key by id query")

	var out APIKeyReadModel
	err := h.db.GetContext(ctx, &out, `SELECT id, name, hash, type, sensitive_id, org_id, instance_id, created_at, updated_at FROM api_keys WHERE id = $1 AND org_id = $2`, qry.ID, qry.OrgID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get api key by id query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	return out, nil
}

// GetApiKeyByHashQuery retrieves an API key by its hash (not revoked).
type GetApiKeyByHashQuery struct {
	query.BaseQuery
	Hash string `json:"hash"`
}

func NewGetApiKeyByHashQuery(hash, userID, traceID string) *GetApiKeyByHashQuery {
	return &GetApiKeyByHashQuery{BaseQuery: query.BaseQuery{}, Hash: hash}
}

func (q GetApiKeyByHashQuery) QueryType() string { return "GetApiKeyByHash" }

func (q GetApiKeyByHashQuery) Validate() error {
	if q.Hash == "" {
		return fmt.Errorf("hash cannot be empty")
	}
	return nil
}

// ApiKeyByHashQueryHandler handles GetApiKeyByHash queries (ensures not revoked).
type ApiKeyByHashQueryHandler struct{ db *sqlx.DB }

func NewApiKeyByHashQueryHandler(db *sqlx.DB) *ApiKeyByHashQueryHandler {
	return &ApiKeyByHashQueryHandler{db: db}
}

func (h *ApiKeyByHashQueryHandler) QueryType() string { return "GetApiKeyByHash" }

func (h *ApiKeyByHashQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetApiKeyByHashQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetApiKeyByHashQuery")
	}
	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"correlation_id", correlationID,
	).Debug("executing get api key by hash query")

	var out APIKeyReadModel
	err := h.db.GetContext(ctx, &out, `SELECT id, name, hash, type, sensitive_id, org_id, instance_id, created_at, updated_at FROM api_keys WHERE hash = $1 AND COALESCE(revoked, FALSE) = FALSE`, qry.Hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get api key by hash query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	return out, nil
}

// ListApiKeysQuery retrieves all API keys (not revoked).
type ListApiKeysQuery struct {
	query.BaseQuery
	UserID     string `json:"user_id"`
	OrgID      string `json:"org_id"`
	InstanceID string `json:"instance_id"`
	Type       string `json:"type"`
}

func NewListApiKeysQuery(userID, orgID, instanceID, type_, traceID string) *ListApiKeysQuery {
	return &ListApiKeysQuery{
		BaseQuery:  query.BaseQuery{},
		UserID:     userID,
		OrgID:      orgID,
		InstanceID: instanceID,
		Type:       type_,
	}
}

func (q ListApiKeysQuery) QueryType() string { return "ListApiKeys" }

func (q ListApiKeysQuery) Validate() error {
	return nil // All fields are optional
}

// ListApiKeysQueryHandler handles ListApiKeys queries.
type ListApiKeysQueryHandler struct {
	db *sqlx.DB
}

func NewListApiKeysQueryHandler(db *sqlx.DB) *ListApiKeysQueryHandler {
	return &ListApiKeysQueryHandler{db: db}
}

func (h *ListApiKeysQueryHandler) QueryType() string { return "ListApiKeys" }

func (h *ListApiKeysQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListApiKeysQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListApiKeysQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"user_id", qry.UserID,
		"org_id", qry.OrgID,
		"instance_id", qry.InstanceID,
		"type", qry.Type,
		"correlation_id", correlationID,
	).Debug("executing list api keys query")

	// Build the query with optional filters
	query := `SELECT id, name, hash, type, sensitive_id, org_id, instance_id, created_at, updated_at FROM api_keys WHERE COALESCE(revoked, FALSE) = FALSE`
	args := []interface{}{}
	argIndex := 1

	if qry.InstanceID != "" {
		query += fmt.Sprintf(" AND instance_id = $%d", argIndex)
		args = append(args, qry.InstanceID)
		argIndex++
	}

	if qry.UserID != "" {
		query += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, qry.UserID)
		argIndex++
	}

	if qry.OrgID != "" {
		query += fmt.Sprintf(" AND org_id = $%d", argIndex)
		args = append(args, qry.OrgID)
		argIndex++
	}

	if qry.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", argIndex)
		args = append(args, qry.Type)
		argIndex++
	}

	query += " ORDER BY created_at DESC"

	var out []APIKeyReadModel
	err := h.db.SelectContext(ctx, &out, query, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list api keys query")
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	return out, nil
}
