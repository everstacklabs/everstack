package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// PostgresApprovalGate creates durable reviews for the OpenAI Agents review
// endpoints to expose. A supervisor may poll again after a review is resolved.
type PostgresApprovalGate struct {
	db  *sqlx.DB
	ttl time.Duration
}

type ApprovalReview struct {
	ID, SessionID, TurnID, CallID, ToolName, Status, RequestedBy string
	ResourceKind, ResourceID                                     string
	Arguments                                                    json.RawMessage
	RequestedAt, ExpiresAt                                       time.Time
}

func NewPostgresApprovalGate(db *sqlx.DB, ttl time.Duration) (*PostgresApprovalGate, error) {
	if db == nil {
		return nil, errors.New("approval database is required")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &PostgresApprovalGate{db: db, ttl: ttl}, nil
}

func (g *PostgresApprovalGate) ListPending(ctx context.Context, o Owner) ([]ApprovalReview, error) {
	rows, err := g.db.QueryxContext(ctx, `SELECT id,session_id,turn_id,call_id,tool_name,resource_kind,resource_id,arguments,status,requested_by,requested_at,expires_at FROM openai_agent_tool_approvals WHERE organization_id=$1 AND instance_id=$2 AND status='pending' AND expires_at>NOW() ORDER BY requested_at`, o.OrganizationID, o.InstanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ApprovalReview{}
	for rows.Next() {
		var r ApprovalReview
		if err = rows.Scan(&r.ID, &r.SessionID, &r.TurnID, &r.CallID, &r.ToolName, &r.ResourceKind, &r.ResourceID, &r.Arguments, &r.Status, &r.RequestedBy, &r.RequestedAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (g *PostgresApprovalGate) Review(ctx context.Context, o Owner, id string) (ApprovalReview, error) {
	var r ApprovalReview
	err := g.db.QueryRowxContext(ctx, `SELECT id,session_id,turn_id,call_id,tool_name,resource_kind,resource_id,arguments,status,requested_by,requested_at,expires_at FROM openai_agent_tool_approvals WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID).Scan(&r.ID, &r.SessionID, &r.TurnID, &r.CallID, &r.ToolName, &r.ResourceKind, &r.ResourceID, &r.Arguments, &r.Status, &r.RequestedBy, &r.RequestedAt, &r.ExpiresAt)
	if err != nil {
		return r, ErrDenied
	}
	return r, nil
}

func (g *PostgresApprovalGate) Resolve(ctx context.Context, o Owner, id string, decision Decision) error {
	if decision != Approved && decision != Denied {
		return errors.New("approval decision must be approved or denied")
	}
	var current string
	if err := g.db.GetContext(ctx, &current, `SELECT status FROM openai_agent_tool_approvals WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID); err != nil {
		return ErrDenied
	}
	if current == string(decision) {
		return nil
	}
	if current != "pending" {
		return ErrDenied
	}
	res, err := g.db.ExecContext(ctx, `UPDATE openai_agent_tool_approvals SET status=$5,resolved_by=$4,resolved_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3 AND status='pending' AND expires_at>NOW()`, id, o.OrganizationID, o.InstanceID, o.PrincipalID, string(decision))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrDenied
	}
	return nil
}

func (g *PostgresApprovalGate) Check(ctx context.Context, o Owner, grant Grant, in Invocation) (Decision, error) {
	args := in.Arguments
	if !json.Valid(args) {
		args = json.RawMessage(`{}`)
	}
	_, err := g.db.ExecContext(ctx, `INSERT INTO openai_agent_tool_approvals (id,organization_id,instance_id,session_id,turn_id,call_id,tool_name,resource_kind,resource_id,arguments,requested_by,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW()+($12 * INTERVAL '1 millisecond')) ON CONFLICT (organization_id,instance_id,session_id,turn_id,call_id) DO NOTHING`, uuid.NewString(), o.OrganizationID, o.InstanceID, in.SessionID, in.TurnID, in.CallID, in.Name, grant.Kind, grant.ResourceID, args, o.PrincipalID, g.ttl.Milliseconds())
	if err != nil {
		return Pending, err
	}
	_, err = g.db.ExecContext(ctx, `UPDATE openai_agent_tool_approvals SET status='expired',resolved_at=NOW() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND status='pending' AND expires_at<=NOW()`, o.OrganizationID, o.InstanceID, in.SessionID, in.TurnID, in.CallID)
	if err != nil {
		return Pending, err
	}
	var status string
	err = g.db.GetContext(ctx, &status, `SELECT status FROM openai_agent_tool_approvals WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5`, o.OrganizationID, o.InstanceID, in.SessionID, in.TurnID, in.CallID)
	if err != nil {
		return Pending, err
	}
	switch status {
	case "approved":
		return Approved, nil
	case "pending":
		return Pending, nil
	default:
		return Denied, nil
	}
}
