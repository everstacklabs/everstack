package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

var ErrNotFound = errors.New("OpenAI Agents session not found")

type PostgresRepository struct{ db *sqlx.DB }

func NewPostgresRepository(db *sqlx.DB) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("OpenAI usage database is required")
	}
	return &PostgresRepository{db: db}, nil
}

func (r *PostgresRepository) RecordTurnUsage(ctx context.Context, usage TurnUsage) (bool, error) {
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	var owned, accepted, changed bool
	err := r.db.QueryRowxContext(ctx, `WITH owned AS (
 SELECT 1 FROM openai_agent_sessions
 WHERE id=$1 AND organization_id=$2 AND instance_id=$3 AND upstream_session_id=$4
), accepted AS (
 INSERT INTO openai_agent_usage_events
  (organization_id,instance_id,session_id,event_id,upstream_turn_id,event_cursor,observed_at)
 SELECT $2,$3,$1,$5,$6,$7,$11 FROM owned
 ON CONFLICT (session_id,event_id) DO NOTHING RETURNING 1
), changed AS (
 INSERT INTO openai_agent_turn_usage
  (organization_id,instance_id,session_id,upstream_session_id,upstream_turn_id,input_tokens,output_tokens,total_tokens,event_cursor,updated_at)
 SELECT $2,$3,$1,$4,$6,$8,$9,$10,$7,$11 FROM accepted
 ON CONFLICT (session_id,upstream_turn_id) DO UPDATE SET
  input_tokens=COALESCE(EXCLUDED.input_tokens,openai_agent_turn_usage.input_tokens),
  output_tokens=COALESCE(EXCLUDED.output_tokens,openai_agent_turn_usage.output_tokens),
  total_tokens=COALESCE(EXCLUDED.total_tokens,openai_agent_turn_usage.total_tokens),
  event_cursor=EXCLUDED.event_cursor,updated_at=EXCLUDED.updated_at
 WHERE EXCLUDED.event_cursor > openai_agent_turn_usage.event_cursor
 RETURNING 1
)
SELECT EXISTS(SELECT 1 FROM owned),EXISTS(SELECT 1 FROM accepted),EXISTS(SELECT 1 FROM changed)`,
		usage.LocalSessionID, usage.OrganizationID, usage.InstanceID, usage.UpstreamSessionID,
		usage.EventID, usage.UpstreamTurnID, usage.Cursor, usage.InputTokens, usage.OutputTokens,
		usage.TotalTokens, usage.ObservedAt.UTC()).Scan(&owned, &accepted, &changed)
	if err != nil {
		return false, fmt.Errorf("record OpenAI turn usage: %w", err)
	}
	if !owned {
		return false, ErrNotFound
	}
	// accepted can be true for a stale out-of-order event; changed truthfully
	// reports whether the authoritative per-turn snapshot advanced.
	return accepted && changed, nil
}

func (r *PostgresRepository) SessionUsage(ctx context.Context, scope Scope) (UsageSnapshot, error) {
	var count int64
	var input, output, total sql.NullInt64
	var updated sql.NullTime
	err := r.db.QueryRowxContext(ctx, `SELECT COUNT(u.upstream_turn_id),SUM(u.input_tokens),SUM(u.output_tokens),SUM(u.total_tokens),MAX(u.updated_at)
 FROM openai_agent_sessions s LEFT JOIN openai_agent_turn_usage u ON u.session_id=s.id
 WHERE s.id=$1 AND s.organization_id=$2 AND s.instance_id=$3
 GROUP BY s.id`, scope.LocalSessionID, scope.OrganizationID, scope.InstanceID).
		Scan(&count, &input, &output, &total, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return UsageSnapshot{}, ErrNotFound
	}
	if err != nil {
		return UsageSnapshot{}, fmt.Errorf("read OpenAI session usage: %w", err)
	}
	if count == 0 {
		return Unavailable(), nil
	}
	snapshot := UsageSnapshot{Status: UsageReported, Source: SourceOpenAI}
	if input.Valid {
		snapshot.InputTokens = &input.Int64
	}
	if output.Valid {
		snapshot.OutputTokens = &output.Int64
	}
	if total.Valid {
		snapshot.TotalTokens = &total.Int64
	}
	if updated.Valid {
		t := updated.Time.UTC()
		snapshot.UpdatedAt = &t
	}
	return snapshot, nil
}
