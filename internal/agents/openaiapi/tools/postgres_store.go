package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

type PostgresStore struct {
	db    *sqlx.DB
	lease time.Duration
}

func NewPostgresStore(db *sqlx.DB, lease time.Duration) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("tool database is required")
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	return &PostgresStore{db: db, lease: lease}, nil
}

func (s *PostgresStore) BindSession(ctx context.Context, o Owner, sid string, grants []Grant) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM openai_agent_tool_grants WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3`, o.OrganizationID, o.InstanceID, sid); err != nil {
		return err
	}
	for _, g := range grants {
		b, e := json.Marshal(g)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO openai_agent_tool_grants (organization_id,instance_id,session_id,upstream_name,grant_json) VALUES ($1,$2,$3,$4,$5)`, o.OrganizationID, o.InstanceID, sid, g.UpstreamName, b); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) GrantForCall(ctx context.Context, o Owner, sid, name string) (Grant, error) {
	var b []byte
	err := s.db.GetContext(ctx, &b, `SELECT grant_json FROM openai_agent_tool_grants g JOIN openai_agent_sessions s ON s.id=g.session_id AND s.organization_id=g.organization_id AND s.instance_id=g.instance_id WHERE g.organization_id=$1 AND g.instance_id=$2 AND g.session_id=$3 AND g.upstream_name=$4 AND s.cleanup_status<>'deleted' AND s.status NOT IN ('canceling','cancelled','canceled')`, o.OrganizationID, o.InstanceID, sid, name)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrDenied
	}
	if err != nil {
		return Grant{}, err
	}
	var g Grant
	if json.Unmarshal(b, &g) != nil {
		return Grant{}, ErrDenied
	}
	return g, nil
}

func callKey(o Owner, in Invocation) []any {
	return []any{o.OrganizationID, o.InstanceID, in.SessionID, in.TurnID, in.CallID}
}

func (s *PostgresStore) BeginCall(ctx context.Context, o Owner, in Invocation) (*Result, bool, error) {
	a := callKey(o, in)
	res, err := s.db.ExecContext(ctx, `INSERT INTO openai_agent_tool_calls (organization_id,instance_id,session_id,turn_id,call_id,tool_name,status,claimed_until) VALUES ($1,$2,$3,$4,$5,$6,'executing',NOW()+($7 * INTERVAL '1 millisecond')) ON CONFLICT (organization_id,instance_id,session_id,turn_id,call_id) DO NOTHING`, a[0], a[1], a[2], a[3], a[4], in.Name, s.lease.Milliseconds())
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return nil, true, nil
	}
	var status string
	var b []byte
	err = s.db.QueryRowxContext(ctx, `SELECT status,COALESCE(result_json,'{}'::jsonb) FROM openai_agent_tool_calls WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5`, a...).Scan(&status, &b)
	if err != nil {
		return nil, false, err
	}
	if status == "executing" {
		return nil, false, nil
	}
	var out Result
	if json.Unmarshal(b, &out) != nil {
		return nil, false, errors.New("invalid stored tool result")
	}
	return &out, false, nil
}

func (s *PostgresStore) MarkRecoveryRequired(ctx context.Context, o Owner, in Invocation, r Result) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	a := append(callKey(o, in), b)
	_, err = s.db.ExecContext(ctx, `UPDATE openai_agent_tool_calls SET status='recovery_required',result_json=$6,claimed_until=NULL,updated_at=NOW() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND status='executing'`, a...)
	return err
}

func (s *PostgresStore) ReleaseCall(ctx context.Context, o Owner, in Invocation) error {
	a := callKey(o, in)
	_, err := s.db.ExecContext(ctx, `DELETE FROM openai_agent_tool_calls WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND status='executing'`, a...)
	return err
}

func (s *PostgresStore) CompleteCall(ctx context.Context, o Owner, in Invocation, r Result) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	a := append(callKey(o, in), b, r.State)
	res, err := s.db.ExecContext(ctx, `UPDATE openai_agent_tool_calls SET status=$7,result_json=$6,claimed_until=NULL,updated_at=NOW() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND status='executing'`, a...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("tool call claim lost")
	}
	return nil
}

func (s *PostgresStore) ClaimDelivery(ctx context.Context, o Owner, in Invocation, token string, lease time.Duration) (bool, string, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()
	var previous string
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT delivery_status,(delivery_status IN ('pending','uncertain') OR (delivery_status='submitting' AND delivery_claimed_until<clock_timestamp())) FROM openai_agent_tool_calls WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND status='completed' FOR UPDATE`, callKey(o, in)...).Scan(&previous, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if !eligible {
		return false, previous, tx.Commit()
	}
	args := append(callKey(o, in), token, lease.Milliseconds())
	if _, err = tx.ExecContext(ctx, `UPDATE openai_agent_tool_calls SET delivery_status='submitting',delivery_token=$6,delivery_claimed_until=clock_timestamp()+($7 * INTERVAL '1 millisecond'),updated_at=clock_timestamp() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5`, args...); err != nil {
		return false, "", err
	}
	if err = tx.Commit(); err != nil {
		return false, "", err
	}
	if previous == "submitting" {
		previous = "expired"
	}
	return true, previous, nil
}

func (s *PostgresStore) ReleaseDelivery(ctx context.Context, o Owner, in Invocation, token string) error {
	a := append(callKey(o, in), token)
	res, err := s.db.ExecContext(ctx, `UPDATE openai_agent_tool_calls SET delivery_status='pending',delivery_token=NULL,delivery_claimed_until=NULL,updated_at=NOW() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND delivery_status='submitting' AND delivery_token=$6`, a...)
	return deliveryMutationResult(res, err)
}

func (s *PostgresStore) CompleteDelivery(ctx context.Context, o Owner, in Invocation, token string) error {
	a := append(callKey(o, in), token)
	res, err := s.db.ExecContext(ctx, `UPDATE openai_agent_tool_calls SET delivery_status='submitted',delivery_token=NULL,delivery_claimed_until=NULL,updated_at=NOW() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND delivery_status='submitting' AND delivery_token=$6`, a...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("tool result delivery claim was lost")
	}
	return nil
}

func (s *PostgresStore) MarkDeliveryUncertain(ctx context.Context, o Owner, in Invocation, token string) error {
	a := append(callKey(o, in), token)
	res, err := s.db.ExecContext(ctx, `UPDATE openai_agent_tool_calls SET delivery_status='uncertain',delivery_token=NULL,delivery_claimed_until=NULL,updated_at=NOW() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND turn_id=$4 AND call_id=$5 AND delivery_status='submitting' AND delivery_token=$6`, a...)
	return deliveryMutationResult(res, err)
}

func deliveryMutationResult(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("tool result delivery claim was lost")
	}
	return nil
}
