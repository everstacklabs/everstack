package openaiapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type PostgresStore struct{ db *sqlx.DB }

func NewPostgresStore(db *sqlx.DB) (*PostgresStore, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	return &PostgresStore{db: db}, nil
}

const connectionColumns = `id, name, provider, openai_project_id, openai_organization_id,
 openai_principal_id, allowed_models, status, COALESCE(last_error,''), credential_ref,
 COALESCE(executor_credential_ref,''), created_at, updated_at`

func scanConnection(row interface{ Scan(...any) error }) (*Connection, error) {
	var c Connection
	var models []byte
	err := row.Scan(&c.ID, &c.Name, &c.Provider, &c.ProjectID, &c.OpenAIOrganization, &c.OpenAIPrincipal,
		&models, &c.Status, &c.LastError, &c.credentialRef, &c.executorRef, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan OpenAI Agents connection: %w", err)
	}
	_ = json.Unmarshal(models, &c.AllowedModels)
	return &c, nil
}

func (s *PostgresStore) CreateConnection(ctx context.Context, o Ownership, c Connection, credentialRef, executorRef string) (*Connection, error) {
	models, _ := json.Marshal(c.AllowedModels)
	row := s.db.QueryRowxContext(ctx, `INSERT INTO openai_agent_connections
 (id,organization_id,instance_id,name,provider,openai_project_id,openai_organization_id,openai_principal_id,allowed_models,status,credential_ref,executor_credential_ref)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending',$10,NULLIF($11,'')) RETURNING `+connectionColumns,
		c.ID, o.OrganizationID, o.InstanceID, c.Name, Provider, c.ProjectID, c.OpenAIOrganization, c.OpenAIPrincipal, models, credentialRef, executorRef)
	return scanConnection(row)
}
func (s *PostgresStore) ListConnections(ctx context.Context, o Ownership) ([]Connection, error) {
	rows, err := s.db.QueryxContext(ctx, `SELECT `+connectionColumns+` FROM openai_agent_connections WHERE organization_id=$1 AND instance_id=$2 AND deleted_at IS NULL ORDER BY created_at DESC`, o.OrganizationID, o.InstanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}
func (s *PostgresStore) GetConnection(ctx context.Context, o Ownership, id string) (*Connection, error) {
	return scanConnection(s.db.QueryRowxContext(ctx, `SELECT `+connectionColumns+` FROM openai_agent_connections WHERE id=$1 AND organization_id=$2 AND instance_id=$3 AND deleted_at IS NULL`, id, o.OrganizationID, o.InstanceID))
}
func (s *PostgresStore) UpdateConnection(ctx context.Context, o Ownership, c Connection, credentialRef, executorRef string) (*Connection, error) {
	models, _ := json.Marshal(c.AllowedModels)
	return scanConnection(s.db.QueryRowxContext(ctx, `UPDATE openai_agent_connections SET name=$4,openai_project_id=$5,openai_organization_id=$6,openai_principal_id=$7,allowed_models=$8,credential_ref=$9,executor_credential_ref=NULLIF($10,''),status='pending',last_error=NULL,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3 AND deleted_at IS NULL RETURNING `+connectionColumns, c.ID, o.OrganizationID, o.InstanceID, c.Name, c.ProjectID, c.OpenAIOrganization, c.OpenAIPrincipal, models, credentialRef, executorRef))
}
func (s *PostgresStore) UpdateConnectionStatus(ctx context.Context, o Ownership, id, status, lastErr string) error {
	lastErr = sanitizeError(lastErr)
	return ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_connections SET status=$4,last_error=NULLIF($5,''),updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3 AND deleted_at IS NULL`, id, o.OrganizationID, o.InstanceID, status, lastErr))
}
func (s *PostgresStore) DeleteConnection(ctx context.Context, o Ownership, id string) error {
	// The NOT EXISTS clause makes the guard atomic with the delete, so a
	// session created concurrently cannot slip in behind a separate check.
	res, err := s.db.ExecContext(ctx, `UPDATE openai_agent_connections SET deleted_at=NOW(),status='revoked',credential_ref='',executor_credential_ref=NULL,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3 AND deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM openai_agent_sessions WHERE connection_id=$1 AND organization_id=$2 AND instance_id=$3 AND cleanup_status<>'deleted')`, id, o.OrganizationID, o.InstanceID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// Nothing was deleted. Separate "still in use" from "not there", so the
	// caller is told which one it is. This read races only the error message.
	var live int
	if err = s.db.GetContext(ctx, &live, `SELECT COUNT(*) FROM openai_agent_sessions WHERE connection_id=$1 AND organization_id=$2 AND instance_id=$3 AND cleanup_status<>'deleted'`, id, o.OrganizationID, o.InstanceID); err != nil {
		return err
	}
	if live > 0 {
		return ErrConnectionInUse
	}
	return ErrNotFound
}

const sessionColumns = `id,provider,connection_id,COALESCE(conversation_id,''),COALESCE(upstream_session_id,''),COALESCE(upstream_environment_id,''),COALESCE(sandbox_id,''),model,environment_type,workspace_directory,status,readiness,cleanup_status,environment_cleanup_status,COALESCE(last_error,''),idempotency_key,request_fingerprint,created_at,updated_at`
const recoverySessionColumns = `s.id,s.provider,s.connection_id,COALESCE(s.conversation_id,''),COALESCE(s.upstream_session_id,''),COALESCE(s.upstream_environment_id,''),COALESCE(s.sandbox_id,''),s.model,s.environment_type,s.workspace_directory,s.status,s.readiness,s.cleanup_status,s.environment_cleanup_status,COALESCE(s.last_error,''),s.idempotency_key,s.request_fingerprint,s.created_at,s.updated_at`

func (s *PostgresStore) ListRecoverable(ctx context.Context) ([]RecoveryRecord, error) {
	rows, err := s.db.QueryxContext(ctx, `SELECT s.organization_id,s.instance_id,s.created_by,`+recoverySessionColumns+` FROM openai_agent_sessions s WHERE s.cleanup_status<>'deleted' AND s.environment_cleanup_status='active' AND s.upstream_session_id IS NOT NULL AND s.status NOT IN ('deleted','failed')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecoveryRecord{}
	for rows.Next() {
		var r RecoveryRecord
		x := &r.Session
		if err = rows.Scan(&r.Ownership.OrganizationID, &r.Ownership.InstanceID, &r.Ownership.PrincipalID, &x.ID, &x.Provider, &x.ConnectionID, &x.ConversationID, &x.UpstreamSessionID, &x.UpstreamEnvironmentID, &x.SandboxID, &x.Model, &x.Environment.Type, &x.Environment.WorkspaceDirectory, &x.Status, &x.Readiness, &x.CleanupStatus, &x.EnvironmentCleanupStatus, &x.LastError, &x.IdempotencyKey, &x.RequestFingerprint, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) AcquireSessionLease(ctx context.Context, o Ownership, id, holder string, ttl time.Duration) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO openai_agent_session_leases (session_id,organization_id,instance_id,holder_id,expires_at)
	 SELECT id,organization_id,instance_id,$4,clock_timestamp()+$5::interval FROM openai_agent_sessions
	 WHERE id=$1 AND organization_id=$2 AND instance_id=$3 AND cleanup_status='active' AND environment_cleanup_status='active' AND status NOT IN ('deleted','failed')
	 ON CONFLICT (session_id) DO UPDATE SET holder_id=EXCLUDED.holder_id,expires_at=EXCLUDED.expires_at,updated_at=clock_timestamp()
	 WHERE openai_agent_session_leases.holder_id=EXCLUDED.holder_id OR openai_agent_session_leases.expires_at<=clock_timestamp()`, id, o.OrganizationID, o.InstanceID, holder, postgresInterval(ttl))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *PostgresStore) RenewSessionLease(ctx context.Context, o Ownership, id, holder string, ttl time.Duration) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE openai_agent_session_leases l SET expires_at=clock_timestamp()+$5::interval,updated_at=clock_timestamp()
	 FROM openai_agent_sessions s WHERE l.session_id=$1 AND l.organization_id=$2 AND l.instance_id=$3 AND l.holder_id=$4
	 AND l.expires_at>clock_timestamp() AND s.id=l.session_id AND s.organization_id=l.organization_id AND s.instance_id=l.instance_id
	 AND s.cleanup_status='active' AND s.environment_cleanup_status='active' AND s.status NOT IN ('deleted','failed')`, id, o.OrganizationID, o.InstanceID, holder, postgresInterval(ttl))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *PostgresStore) ReleaseSessionLease(ctx context.Context, o Ownership, id, holder string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM openai_agent_session_leases WHERE session_id=$1 AND organization_id=$2 AND instance_id=$3 AND holder_id=$4`, id, o.OrganizationID, o.InstanceID, holder); err != nil {
		return err
	}
	return tx.Commit()
}

func postgresInterval(d time.Duration) string { return fmt.Sprintf("%f seconds", d.Seconds()) }

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var x Session
	err := row.Scan(&x.ID, &x.Provider, &x.ConnectionID, &x.ConversationID, &x.UpstreamSessionID, &x.UpstreamEnvironmentID, &x.SandboxID, &x.Model, &x.Environment.Type, &x.Environment.WorkspaceDirectory, &x.Status, &x.Readiness, &x.CleanupStatus, &x.EnvironmentCleanupStatus, &x.LastError, &x.IdempotencyKey, &x.RequestFingerprint, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan OpenAI Agents session: %w", err)
	}
	return &x, nil
}
func (s *PostgresStore) BeginSessionCreate(ctx context.Context, o Ownership, x Session) (*Session, bool, error) {
	row := s.db.QueryRowxContext(ctx, `INSERT INTO openai_agent_sessions (id,organization_id,instance_id,provider,connection_id,model,environment_type,workspace_directory,status,readiness,cleanup_status,idempotency_key,request_fingerprint,created_by)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'creating','pending','active',$9,$10,$11)
	 ON CONFLICT (organization_id,instance_id,idempotency_key) DO NOTHING RETURNING `+sessionColumns, x.ID, o.OrganizationID, o.InstanceID, Provider, x.ConnectionID, x.Model, x.Environment.Type, x.Environment.WorkspaceDirectory, x.IdempotencyKey, x.RequestFingerprint, o.PrincipalID)
	got, err := scanSession(row)
	if err == nil {
		return got, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	got, err = s.getByIdem(ctx, o, x.IdempotencyKey)
	return got, false, err
}
func (s *PostgresStore) getByIdem(ctx context.Context, o Ownership, key string) (*Session, error) {
	return scanSession(s.db.QueryRowxContext(ctx, `SELECT `+sessionColumns+` FROM openai_agent_sessions WHERE organization_id=$1 AND instance_id=$2 AND idempotency_key=$3`, o.OrganizationID, o.InstanceID, key))
}
func (s *PostgresStore) CompleteSessionCreate(ctx context.Context, o Ownership, id string, u UpstreamSession) (*Session, error) {
	err := ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_sessions SET upstream_session_id=$4,upstream_environment_id=NULLIF($5,''),status=$6,readiness=$7,last_error=NULL,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID, u.ID, u.EnvironmentID, defaultString(u.Status, "idle"), defaultString(u.Readiness, "pending")))
	if err != nil {
		return nil, err
	}
	return s.GetSession(ctx, o, id)
}
func (s *PostgresStore) BindEnvironment(ctx context.Context, o Ownership, id, sandboxID, readiness string) error {
	return ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_sessions SET sandbox_id=CASE WHEN environment_cleanup_status='active' THEN $4 ELSE sandbox_id END,readiness=CASE WHEN environment_cleanup_status='active' THEN $5 ELSE readiness END,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID, sandboxID, readiness))
}
func (s *PostgresStore) MarkSessionCreateAmbiguous(ctx context.Context, o Ownership, id, msg string) error {
	return ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_sessions SET status='recovery_pending',readiness='pending',last_error=$4,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID, sanitizeError(msg)))
}
func (s *PostgresStore) ListSessions(ctx context.Context, o Ownership) ([]Session, error) {
	rows, err := s.db.QueryxContext(ctx, `SELECT `+sessionColumns+` FROM openai_agent_sessions WHERE organization_id=$1 AND instance_id=$2 AND cleanup_status<>'deleted' ORDER BY created_at DESC`, o.OrganizationID, o.InstanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		x, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}
func (s *PostgresStore) GetSession(ctx context.Context, o Ownership, id string) (*Session, error) {
	return scanSession(s.db.QueryRowxContext(ctx, `SELECT `+sessionColumns+` FROM openai_agent_sessions WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID))
}
func (s *PostgresStore) UpdateSessionState(ctx context.Context, o Ownership, id, status, readiness, lastErr string) error {
	return ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_sessions SET status=CASE WHEN environment_cleanup_status='active' THEN $4 ELSE status END,readiness=CASE WHEN environment_cleanup_status='active' THEN $5 ELSE readiness END,last_error=CASE WHEN environment_cleanup_status='active' THEN NULLIF($6,'') ELSE last_error END,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID, status, readiness, sanitizeError(lastErr)))
}

func (s *PostgresStore) UpdateSessionStatus(ctx context.Context, o Ownership, id, status, lastErr string) error {
	return ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_sessions SET status=CASE WHEN environment_cleanup_status='active' THEN $4 ELSE status END,last_error=CASE WHEN environment_cleanup_status='active' THEN NULLIF($5,'') ELSE last_error END,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID, status, sanitizeError(lastErr)))
}

func (s *PostgresStore) FenceEnvironmentTimeout(ctx context.Context, o Ownership, id, holder, msg string) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE openai_agent_sessions s SET environment_cleanup_status='timeout_terminal',status='failed',readiness='failed',last_error=$5,updated_at=clock_timestamp()
	 FROM openai_agent_session_leases l WHERE s.id=$1 AND s.organization_id=$2 AND s.instance_id=$3
	 AND s.environment_cleanup_status='active' AND l.session_id=s.id AND l.organization_id=s.organization_id AND l.instance_id=s.instance_id
	 AND l.holder_id=$4 AND l.expires_at>clock_timestamp()`, id, o.OrganizationID, o.InstanceID, holder, sanitizeError(msg))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}
func (s *PostgresStore) MarkCleanup(ctx context.Context, o Ownership, id, status, lastErr string) error {
	return ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_sessions SET cleanup_status=$4,last_error=CASE WHEN environment_cleanup_status='active' THEN NULLIF($5,'') ELSE last_error END,updated_at=NOW() WHERE id=$1 AND organization_id=$2 AND instance_id=$3`, id, o.OrganizationID, o.InstanceID, status, sanitizeError(lastErr)))
}
func (s *PostgresStore) PutEvent(ctx context.Context, o Ownership, sessionID string, e UpstreamEvent) (*Event, bool, error) {
	payload := sanitizeJSON(e.Payload)
	row := s.db.QueryRowxContext(ctx, `INSERT INTO openai_agent_events (organization_id,instance_id,session_id,event_id,event_type,event_version,upstream_item_id,payload,tool_failed,created_at)
 SELECT $1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,COALESCE(NULLIF($10::timestamptz,'0001-01-01T00:00:00Z'),NOW()) WHERE EXISTS (SELECT 1 FROM openai_agent_sessions WHERE id=$3 AND organization_id=$1 AND instance_id=$2)
 ON CONFLICT (session_id,event_id) DO NOTHING RETURNING cursor,event_id,event_type,event_version,COALESCE(upstream_item_id,''),payload,tool_failed,created_at`, o.OrganizationID, o.InstanceID, sessionID, e.ID, e.Type, e.Version, e.ItemID, payload, e.Failed, e.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	var out Event
	err := row.Scan(&out.Cursor, &out.EventID, &out.Type, &out.Version, &out.UpstreamItemID, &out.Payload, &out.ToolFailed, &out.CreatedAt)
	if err == nil {
		return &out, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	err = s.db.QueryRowxContext(ctx, `SELECT cursor,event_id,event_type,event_version,COALESCE(upstream_item_id,''),payload,tool_failed,created_at FROM openai_agent_events WHERE session_id=$1 AND event_id=$2 AND organization_id=$3 AND instance_id=$4`, sessionID, e.ID, o.OrganizationID, o.InstanceID).Scan(&out.Cursor, &out.EventID, &out.Type, &out.Version, &out.UpstreamItemID, &out.Payload, &out.ToolFailed, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	return &out, false, err
}
func (s *PostgresStore) ListEvents(ctx context.Context, o Ownership, id string, after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryxContext(ctx, `SELECT cursor,event_id,event_type,event_version,COALESCE(upstream_item_id,''),payload,tool_failed,created_at FROM openai_agent_events WHERE session_id=$1 AND organization_id=$2 AND instance_id=$3 AND cursor>$4 ORDER BY cursor LIMIT $5`, id, o.OrganizationID, o.InstanceID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Cursor, &e.EventID, &e.Type, &e.Version, &e.UpstreamItemID, &e.Payload, &e.ToolFailed, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ClaimInput(ctx context.Context, o Ownership, id, key, fingerprint string) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO openai_agent_input_intents (organization_id,instance_id,session_id,idempotency_key,request_fingerprint,status) SELECT $1,$2,$3,$4,$5,'sending' WHERE EXISTS(SELECT 1 FROM openai_agent_sessions WHERE id=$3 AND organization_id=$1 AND instance_id=$2 AND environment_cleanup_status='active') ON CONFLICT DO NOTHING`, o.OrganizationID, o.InstanceID, id, key, fingerprint)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}
func (s *PostgresStore) InputStatus(ctx context.Context, o Ownership, id, key string) (string, string, error) {
	var status, fingerprint string
	err := s.db.QueryRowxContext(ctx, `SELECT status,request_fingerprint FROM openai_agent_input_intents WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND idempotency_key=$4`, o.OrganizationID, o.InstanceID, id, key).Scan(&status, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return status, fingerprint, err
}
func (s *PostgresStore) CompleteInput(ctx context.Context, o Ownership, id, key, status string) error {
	return ownedExec(s.db.ExecContext(ctx, `UPDATE openai_agent_input_intents SET status=$5,updated_at=NOW() WHERE organization_id=$1 AND instance_id=$2 AND session_id=$3 AND idempotency_key=$4`, o.OrganizationID, o.InstanceID, id, key, status))
}
func ownedExec(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func defaultString(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}
