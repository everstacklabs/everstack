package revision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// PostgresStore stores revision metadata and immutable file content.
type PostgresStore struct {
	db *sqlx.DB
}

// NewPostgresStore creates a revision store.
func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// CreateAndActivate creates an immutable revision or reactivates an existing
// revision with the same digest. Agent row locking serializes revision numbers
// and the active pointer update.
func (s *PostgresStore) CreateAndActivate(
	ctx context.Context,
	tenantID, agentID, createdBy string,
	manifest *Manifest,
) (*Revision, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("revision store is not configured")
	}
	if tenantID == "" || agentID == "" {
		return nil, false, fmt.Errorf("tenant_id and agent_id are required")
	}
	if manifest == nil {
		return nil, false, fmt.Errorf("revision manifest is required")
	}
	canonical, err := NewManifest(manifest.Files, manifest.Functions)
	if err != nil {
		return nil, false, err
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin revision transaction: %w", err)
	}
	defer tx.Rollback()

	var lockedAgent string
	if err := tx.GetContext(ctx, &lockedAgent, `
		SELECT id FROM agent_definitions
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		FOR UPDATE
	`, agentID, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrAgentNotFound
		}
		return nil, false, fmt.Errorf("lock agent for revision: %w", err)
	}

	var existingID string
	err = tx.GetContext(ctx, &existingID, `
		SELECT id FROM agent_revisions
		WHERE tenant_id = $1 AND agent_id = $2 AND digest = $3
	`, tenantID, agentID, canonical.Digest)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_definitions SET active_revision_id = $1, updated_at = NOW()
			WHERE id = $2 AND tenant_id = $3
		`, existingID, agentID, tenantID); err != nil {
			return nil, false, fmt.Errorf("activate existing revision: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit existing revision activation: %w", err)
		}
		rev, err := s.Get(ctx, tenantID, existingID)
		return rev, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("lookup revision digest: %w", err)
	}

	var number int
	if err := tx.GetContext(ctx, &number, `
		SELECT COALESCE(MAX(revision_number), 0) + 1
		FROM agent_revisions WHERE agent_id = $1 AND tenant_id = $2
	`, agentID, tenantID); err != nil {
		return nil, false, fmt.Errorf("allocate revision number: %w", err)
	}
	manifestJSON, err := json.Marshal(canonical)
	if err != nil {
		return nil, false, fmt.Errorf("encode revision manifest: %w", err)
	}

	var revisionID string
	if err := tx.QueryRowxContext(ctx, `
		INSERT INTO agent_revisions (
			tenant_id, agent_id, revision_number, digest, manifest, created_by
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, tenantID, agentID, number, canonical.Digest, manifestJSON, createdBy).Scan(&revisionID); err != nil {
		return nil, false, fmt.Errorf("insert agent revision: %w", err)
	}
	for _, file := range canonical.Files {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_revision_files (
				tenant_id, revision_id, path, sha256, mode, size_bytes, content
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, tenantID, revisionID, file.Path, file.SHA256, file.Mode, file.Size, file.Content); err != nil {
			return nil, false, fmt.Errorf("insert revision file %q: %w", file.Path, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_definitions SET active_revision_id = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, revisionID, agentID, tenantID); err != nil {
		return nil, false, fmt.Errorf("activate agent revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit agent revision: %w", err)
	}
	rev, err := s.Get(ctx, tenantID, revisionID)
	return rev, true, err
}

// Get returns one revision with all file content.
func (s *PostgresStore) Get(ctx context.Context, tenantID, revisionID string) (*Revision, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revision store is not configured")
	}
	return getRevision(ctx, s.db, tenantID, revisionID)
}

// GetActive returns the active revision for an agent.
func (s *PostgresStore) GetActive(ctx context.Context, tenantID, agentID string) (*Revision, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revision store is not configured")
	}
	var revisionID string
	if err := s.db.GetContext(ctx, &revisionID, `
		SELECT active_revision_id
		FROM agent_definitions
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		  AND active_revision_id IS NOT NULL
	`, agentID, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get active agent revision: %w", err)
	}
	return s.Get(ctx, tenantID, revisionID)
}

// GetForSession pins a previously unpinned session to the agent's active
// revision, then resolves that exact immutable revision on every later turn.
func (s *PostgresStore) GetForSession(ctx context.Context, tenantID, agentID, sessionID string) (*Revision, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("revision store is not configured")
	}
	if tenantID == "" || agentID == "" || sessionID == "" {
		return nil, fmt.Errorf("tenant_id, agent_id and session_id are required")
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE agent_sessions AS session
		SET revision_id = agent.active_revision_id, updated_at = NOW()
		FROM agent_definitions AS agent
		WHERE session.id = $1
		  AND session.tenant_id = $2
		  AND session.agent_id = $3
		  AND agent.id = session.agent_id
		  AND agent.tenant_id = session.tenant_id
		  AND session.revision_id IS NULL
		  AND agent.active_revision_id IS NOT NULL
	`, sessionID, tenantID, agentID); err != nil {
		return nil, fmt.Errorf("pin session revision: %w", err)
	}
	var revisionID string
	if err := s.db.GetContext(ctx, &revisionID, `
		SELECT revision_id FROM agent_sessions
		WHERE id = $1 AND tenant_id = $2 AND agent_id = $3
		  AND revision_id IS NOT NULL
	`, sessionID, tenantID, agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get pinned session revision: %w", err)
	}
	return s.Get(ctx, tenantID, revisionID)
}

type queryer interface {
	GetContext(context.Context, interface{}, string, ...interface{}) error
	SelectContext(context.Context, interface{}, string, ...interface{}) error
}

func getRevision(ctx context.Context, db queryer, tenantID, revisionID string) (*Revision, error) {
	var row struct {
		ID             string    `db:"id"`
		TenantID       string    `db:"tenant_id"`
		AgentID        string    `db:"agent_id"`
		RevisionNumber int       `db:"revision_number"`
		Digest         string    `db:"digest"`
		Manifest       []byte    `db:"manifest"`
		CreatedBy      string    `db:"created_by"`
		CreatedAt      time.Time `db:"created_at"`
	}
	if err := db.GetContext(ctx, &row, `
		SELECT id, tenant_id, agent_id, revision_number, digest, manifest,
		       created_by, created_at
		FROM agent_revisions WHERE id = $1 AND tenant_id = $2
	`, revisionID, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get agent revision: %w", err)
	}
	var storedManifest Manifest
	if err := json.Unmarshal(row.Manifest, &storedManifest); err != nil {
		return nil, fmt.Errorf("decode agent revision %s: %w", revisionID, err)
	}
	var files []struct {
		Path    string `db:"path"`
		SHA256  string `db:"sha256"`
		Mode    int32  `db:"mode"`
		Size    int64  `db:"size_bytes"`
		Content []byte `db:"content"`
	}
	if err := db.SelectContext(ctx, &files, `
		SELECT path, sha256, mode, size_bytes, content
		FROM agent_revision_files
		WHERE revision_id = $1 AND tenant_id = $2
		ORDER BY path
	`, revisionID, tenantID); err != nil {
		return nil, fmt.Errorf("get agent revision files: %w", err)
	}
	materializedFiles := make([]File, len(files))
	for i, file := range files {
		materializedFiles[i] = File{
			Path: file.Path, SHA256: file.SHA256, Mode: file.Mode,
			Size: file.Size, Content: append([]byte(nil), file.Content...),
		}
	}
	canonical, err := NewManifest(materializedFiles, storedManifest.Functions)
	if err != nil {
		return nil, fmt.Errorf("validate agent revision %s: %w", revisionID, err)
	}
	if storedManifest.Format != FormatV1 || storedManifest.Digest != row.Digest || canonical.Digest != row.Digest {
		return nil, fmt.Errorf("validate agent revision %s: stored digest does not match materialized content", revisionID)
	}
	for i, file := range canonical.Files {
		stored := materializedFiles[i]
		if file.Path != stored.Path || file.SHA256 != stored.SHA256 || file.Size != stored.Size || file.Mode != stored.Mode {
			return nil, fmt.Errorf("validate agent revision %s: file %q metadata does not match content", revisionID, stored.Path)
		}
	}
	return &Revision{
		ID: row.ID, TenantID: row.TenantID, AgentID: row.AgentID,
		Number: row.RevisionNumber, Digest: row.Digest, Manifest: *canonical,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
	}, nil
}
