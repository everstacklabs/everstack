package license

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/query"
)

const GetActiveInstanceIDType = "license.get_active_instance_id"

type GetActiveInstanceID struct {
	query.BaseQuery
}

func (q GetActiveInstanceID) QueryType() string { return GetActiveInstanceIDType }
func (q GetActiveInstanceID) Validate() error   { return nil }

type GetActiveInstanceIDHandler struct{ db *sqlx.DB }

func NewGetActiveInstanceIDHandler(db *sqlx.DB) *GetActiveInstanceIDHandler {
	return &GetActiveInstanceIDHandler{db: db}
}

func (h *GetActiveInstanceIDHandler) QueryType() string { return GetActiveInstanceIDType }

func (h *GetActiveInstanceIDHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	if h.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var instanceID sql.NullString
	// Read instance_id from instance_kid column (assigned by license service during activation)
	err := h.db.QueryRowContext(ctx, `SELECT instance_kid FROM system.instances WHERE instance_status='active' ORDER BY updated_at DESC LIMIT 1`).Scan(&instanceID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !instanceID.Valid {
		return nil, nil
	}
	return instanceID.String, nil
}
