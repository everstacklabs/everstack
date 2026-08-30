package deployment

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// PostgresStore implements Store using PostgreSQL.
type PostgresStore struct {
	db *sqlx.DB
}

// NewPostgresStore creates a new Postgres-backed deployment store.
func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// ─── Deployments ──────────────────────────────────────────────────────

func (s *PostgresStore) CreateDeployment(ctx context.Context, d *Deployment) error {
	q := `
		INSERT INTO agent_deployments (
			tenant_id, agent_id, name, version, status,
			agent_config_snapshot,
			rate_limit_rpm, rate_limit_burst, spend_limit_daily_cents,
			max_concurrent_sessions, max_turns_per_session, session_timeout_seconds,
			track_sessions,
			allowed_origins, description, changelog, deployed_by
		) VALUES (
			$1, $2, $3, $4, $5,
			$6,
			$7, $8, $9,
			$10, $11, $12,
			$13,
			$14, $15, $16, $17
		) RETURNING id, created_at, updated_at`

	if d.AgentConfigSnapshot == nil {
		d.AgentConfigSnapshot = []byte("{}")
	}

	return s.db.QueryRowxContext(ctx, q,
		d.TenantID, d.AgentID, d.Name, d.Version, d.Status,
		d.AgentConfigSnapshot,
		d.RateLimitRPM, d.RateLimitBurst, d.SpendLimitDailyCents,
		d.MaxConcurrentSessions, d.MaxTurnsPerSession, d.SessionTimeoutSeconds,
		d.TrackSessions,
		d.AllowedOrigins, d.Description, d.Changelog, d.DeployedBy,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
}

func (s *PostgresStore) GetDeployment(ctx context.Context, id, tenantID string) (*Deployment, error) {
	var d Deployment
	q := `SELECT * FROM agent_deployments WHERE id = $1 AND tenant_id = $2`
	if err := s.db.GetContext(ctx, &d, q, id, tenantID); err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	return &d, nil
}

func (s *PostgresStore) GetActiveDeployment(ctx context.Context, agentID, tenantID string, version *int) (*Deployment, error) {
	var d Deployment
	if version != nil {
		q := `SELECT * FROM agent_deployments WHERE agent_id = $1 AND tenant_id = $2 AND version = $3`
		if err := s.db.GetContext(ctx, &d, q, agentID, tenantID, *version); err != nil {
			return nil, fmt.Errorf("get deployment by version: %w", err)
		}
	} else {
		q := `SELECT * FROM agent_deployments WHERE agent_id = $1 AND tenant_id = $2 AND status = 'active' ORDER BY version DESC LIMIT 1`
		if err := s.db.GetContext(ctx, &d, q, agentID, tenantID); err != nil {
			return nil, fmt.Errorf("get active deployment: %w", err)
		}
	}
	return &d, nil
}

func (s *PostgresStore) ListDeployments(ctx context.Context, agentID, tenantID string, limit, offset int) ([]*Deployment, int, error) {
	if limit <= 0 {
		limit = 50
	}

	if tenantID == "" {
		return nil, 0, fmt.Errorf("tenantID is required")
	}

	args := []interface{}{tenantID}
	where := "WHERE tenant_id = $1"
	argIdx := 2

	if agentID != "" {
		where += fmt.Sprintf(" AND agent_id = $%d", argIdx)
		args = append(args, agentID)
		argIdx++
	}
	// Count
	var total int
	countQ := "SELECT COUNT(*) FROM agent_deployments " + where
	if err := s.db.GetContext(ctx, &total, countQ, args...); err != nil {
		return nil, 0, fmt.Errorf("count deployments: %w", err)
	}

	// List
	q := fmt.Sprintf("SELECT * FROM agent_deployments %s ORDER BY version DESC LIMIT $%d OFFSET $%d", where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	var deployments []*Deployment
	if err := s.db.SelectContext(ctx, &deployments, q, args...); err != nil {
		return nil, 0, fmt.Errorf("list deployments: %w", err)
	}
	return deployments, total, nil
}

func (s *PostgresStore) UpdateDeployment(ctx context.Context, d *Deployment) error {
	q := `
		UPDATE agent_deployments SET
			status = $2,
			rate_limit_rpm = $3,
			max_concurrent_sessions = $4,
			max_turns_per_session = $5,
			session_timeout_seconds = $6,
			track_sessions = $7,
			updated_at = NOW()
		WHERE id = $1 AND tenant_id = $8
		RETURNING updated_at`
	return s.db.QueryRowxContext(ctx, q,
		d.ID, d.Status, d.RateLimitRPM,
		d.MaxConcurrentSessions, d.MaxTurnsPerSession, d.SessionTimeoutSeconds,
		d.TrackSessions, d.TenantID,
	).Scan(&d.UpdatedAt)
}

// ─── Keys ─────────────────────────────────────────────────────────────

func (s *PostgresStore) CreateKey(ctx context.Context, key *DeploymentKey) error {
	q := `
		INSERT INTO agent_deployment_keys (
			tenant_id, deployment_id, key_hash, key_prefix, name, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`
	return s.db.QueryRowxContext(ctx, q,
		key.TenantID, key.DeploymentID, key.KeyHash, key.KeyPrefix, key.Name, key.ExpiresAt,
	).Scan(&key.ID, &key.CreatedAt)
}

func (s *PostgresStore) GetKeyByHash(ctx context.Context, hash string) (*DeploymentKey, error) {
	var key DeploymentKey
	q := `SELECT * FROM agent_deployment_keys WHERE key_hash = $1 AND is_active = TRUE`
	if err := s.db.GetContext(ctx, &key, q, hash); err != nil {
		return nil, fmt.Errorf("get key by hash: %w", err)
	}
	return &key, nil
}

func (s *PostgresStore) ListKeys(ctx context.Context, deploymentID string) ([]*DeploymentKey, error) {
	var keys []*DeploymentKey
	q := `SELECT * FROM agent_deployment_keys WHERE deployment_id = $1 ORDER BY created_at DESC`
	if err := s.db.SelectContext(ctx, &keys, q, deploymentID); err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	return keys, nil
}

func (s *PostgresStore) RevokeKey(ctx context.Context, keyID string) error {
	q := `UPDATE agent_deployment_keys SET is_active = FALSE WHERE id = $1`
	_, err := s.db.ExecContext(ctx, q, keyID)
	return err
}

func (s *PostgresStore) TouchKeyLastUsed(ctx context.Context, keyID string) error {
	q := `UPDATE agent_deployment_keys SET last_used_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, q, keyID)
	return err
}

// ─── Invocations ──────────────────────────────────────────────────────

func (s *PostgresStore) RecordInvocation(ctx context.Context, inv *Invocation) error {
	q := `
		INSERT INTO agent_deployment_invocations (
			tenant_id, deployment_id, session_id, key_id, status,
			input_preview, client_ip, user_agent
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`
	return s.db.QueryRowxContext(ctx, q,
		inv.TenantID, inv.DeploymentID, inv.SessionID, inv.KeyID, inv.Status,
		inv.InputPreview, inv.ClientIP, inv.UserAgent,
	).Scan(&inv.ID, &inv.CreatedAt)
}

func (s *PostgresStore) CompleteInvocation(ctx context.Context, id string, sessionID string, status string, output string, turns, promptTokens, completionTokens, durationMs int) error {
	now := time.Now()
	q := `
		UPDATE agent_deployment_invocations SET
			session_id = $2,
			status = $3,
			output_preview = $4,
			turns = $5,
			prompt_tokens = $6,
			completion_tokens = $7,
			duration_ms = $8,
			completed_at = $9
		WHERE id = $1`
	var sessID interface{}
	if sessionID != "" {
		sessID = sessionID
	}
	_, err := s.db.ExecContext(ctx, q, id, sessID, status, output, turns, promptTokens, completionTokens, durationMs, now)
	return err
}

func (s *PostgresStore) ListInvocations(ctx context.Context, deploymentID string, limit, offset int) ([]*Invocation, int, error) {
	if limit <= 0 {
		limit = 50
	}

	var total int
	countQ := `SELECT COUNT(*) FROM agent_deployment_invocations WHERE deployment_id = $1`
	if err := s.db.GetContext(ctx, &total, countQ, deploymentID); err != nil {
		return nil, 0, fmt.Errorf("count invocations: %w", err)
	}

	// Explicit column list with COALESCE for nullable text/int columns —
	// SELECT * blew up scanning NULL into the struct's non-pointer string/int
	// fields (output_preview, error_message, duration_ms are nullable in the
	// migration but the struct uses plain string/int). The error then bubbles
	// out of the gRPC handler and the UI renders "No invocations yet" — same
	// branch as a genuinely empty result.
	var invocations []*Invocation
	q := `
		SELECT
			id, tenant_id, deployment_id, session_id, key_id, status,
			COALESCE(input_preview, '')   AS input_preview,
			COALESCE(output_preview, '')  AS output_preview,
			COALESCE(turns, 0)            AS turns,
			COALESCE(prompt_tokens, 0)    AS prompt_tokens,
			COALESCE(completion_tokens, 0) AS completion_tokens,
			COALESCE(duration_ms, 0)      AS duration_ms,
			COALESCE(error_message, '')   AS error_message,
			COALESCE(client_ip, '')       AS client_ip,
			COALESCE(user_agent, '')      AS user_agent,
			created_at, completed_at
		FROM agent_deployment_invocations
		WHERE deployment_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	if err := s.db.SelectContext(ctx, &invocations, q, deploymentID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("list invocations: %w", err)
	}
	return invocations, total, nil
}
