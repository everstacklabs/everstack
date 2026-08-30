package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// errTenantRequired is returned when a tenant-scoped store call is invoked
// without a tenantID. Every read/mutate call has to carry a tenant — see
// the comment on Store. Returning a sentinel error rather than running an
// unscoped query is how the repository layer fails closed.
var errTenantRequired = errors.New("agent memory: tenant id is required")

// PostgresStore implements Store using Postgres.
type PostgresStore struct {
	db *sqlx.DB
}

// NewPostgresStore creates a new Postgres-backed memory store.
func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Save(ctx context.Context, m *AgentMemory) error {
	if m.TenantID == "" {
		return errTenantRequired
	}
	const q = `
		INSERT INTO agent_memories (
			tenant_id, agent_id, scope, user_id, memory_type, content,
			fact_key, confidence, source, source_session_id, source_turn_number,
			metadata, embedding_collection_id, is_active
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14
		) RETURNING id, created_at, updated_at`

	metadata := m.Metadata
	if metadata == nil {
		metadata = []byte("{}")
	}

	return s.db.QueryRowContext(ctx, q,
		m.TenantID, m.AgentID, m.Scope, m.UserID, m.MemoryType, m.Content,
		m.FactKey, m.Confidence, m.Source, m.SourceSessionID, m.SourceTurnNumber,
		metadata, m.EmbeddingCollectionID, m.IsActive,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
}

func (s *PostgresStore) SaveBatch(ctx context.Context, memories []*AgentMemory) error {
	if len(memories) == 0 {
		return nil
	}
	for _, m := range memories {
		if m.TenantID == "" {
			return errTenantRequired
		}
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	const q = `
		INSERT INTO agent_memories (
			tenant_id, agent_id, scope, user_id, memory_type, content,
			fact_key, confidence, source, source_session_id, source_turn_number,
			metadata, embedding_collection_id, is_active
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14
		) RETURNING id, created_at, updated_at`

	for _, m := range memories {
		metadata := m.Metadata
		if metadata == nil {
			metadata = []byte("{}")
		}
		if err := tx.QueryRowContext(ctx, q,
			m.TenantID, m.AgentID, m.Scope, m.UserID, m.MemoryType, m.Content,
			m.FactKey, m.Confidence, m.Source, m.SourceSessionID, m.SourceTurnNumber,
			metadata, m.EmbeddingCollectionID, m.IsActive,
		).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return fmt.Errorf("insert memory: %w", err)
		}
	}

	return tx.Commit()
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, id string) (*AgentMemory, error) {
	if tenantID == "" {
		return nil, errTenantRequired
	}
	var m AgentMemory
	err := s.db.GetContext(ctx, &m,
		`SELECT * FROM agent_memories WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get memory %s: %w", id, err)
	}
	return &m, nil
}

func (s *PostgresStore) List(ctx context.Context, opts ListOptions) ([]*AgentMemory, error) {
	if opts.TenantID == "" {
		return nil, errTenantRequired
	}
	where, args := buildWhereClause(opts)
	q := fmt.Sprintf(
		`SELECT * FROM agent_memories %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		where, len(args)+1, len(args)+2,
	)
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit, opts.Offset)

	var memories []*AgentMemory
	if err := s.db.SelectContext(ctx, &memories, q, args...); err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	return memories, nil
}

func (s *PostgresStore) Count(ctx context.Context, opts ListOptions) (int64, error) {
	if opts.TenantID == "" {
		return 0, errTenantRequired
	}
	where, args := buildWhereClause(opts)
	q := fmt.Sprintf(`SELECT COUNT(*) FROM agent_memories %s`, where)

	var count int64
	if err := s.db.GetContext(ctx, &count, q, args...); err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return count, nil
}

func (s *PostgresStore) Deactivate(ctx context.Context, tenantID, id string) error {
	if tenantID == "" {
		return errTenantRequired
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_memories SET is_active = FALSE, updated_at = NOW() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID)
	return err
}

func (s *PostgresStore) Supersede(ctx context.Context, tenantID, oldID, newID string) error {
	if tenantID == "" {
		return errTenantRequired
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_memories SET is_active = FALSE, superseded_by = $2, updated_at = NOW() WHERE id = $1 AND tenant_id = $3`,
		oldID, newID, tenantID)
	return err
}

func (s *PostgresStore) IncrementAccess(ctx context.Context, tenantID string, ids []string) error {
	if tenantID == "" {
		return errTenantRequired
	}
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	placeholders := make([]string, len(ids))
	qArgs := []interface{}{now, now, tenantID}
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+4)
		qArgs = append(qArgs, id)
	}
	query := fmt.Sprintf(
		`UPDATE agent_memories SET access_count = access_count + 1, last_accessed_at = $1, updated_at = $2 WHERE tenant_id = $3 AND id IN (%s)`,
		strings.Join(placeholders, ","),
	)
	_, err := s.db.ExecContext(ctx, query, qArgs...)
	return err
}

func (s *PostgresStore) DeleteByAgent(ctx context.Context, tenantID, agentID string) error {
	if tenantID == "" {
		return errTenantRequired
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_memories WHERE agent_id = $1 AND tenant_id = $2`,
		agentID, tenantID)
	return err
}

func (s *PostgresStore) Update(ctx context.Context, tenantID, id string, content string, factKey *string, confidence float64) error {
	if tenantID == "" {
		return errTenantRequired
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_memories SET content = $2, fact_key = $3, confidence = $4, updated_at = NOW() WHERE id = $1 AND tenant_id = $5`,
		id, content, factKey, confidence, tenantID)
	return err
}

func (s *PostgresStore) Delete(ctx context.Context, tenantID, id string) error {
	if tenantID == "" {
		return errTenantRequired
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_memories WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return err
}

func (s *PostgresStore) FindByFactKey(ctx context.Context, tenantID, agentID, factKey string) (*AgentMemory, error) {
	if tenantID == "" {
		return nil, errTenantRequired
	}
	var m AgentMemory
	err := s.db.GetContext(ctx, &m,
		`SELECT * FROM agent_memories WHERE tenant_id = $1 AND agent_id = $2 AND fact_key = $3 AND is_active = TRUE LIMIT 1`,
		tenantID, agentID, factKey)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("find by fact key: %w", err)
	}
	return &m, nil
}

func (s *PostgresStore) UpsertSummary(ctx context.Context, m *AgentMemory) error {
	if m.TenantID == "" {
		return errTenantRequired
	}
	const q = `
		INSERT INTO agent_memories (
			tenant_id, agent_id, scope, user_id, memory_type, content,
			fact_key, confidence, source, source_session_id, source_turn_number,
			metadata, embedding_collection_id, is_active
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14
		)
		ON CONFLICT (agent_id, source_session_id) WHERE memory_type = 'session_summary' AND is_active = TRUE
		DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()
		WHERE agent_memories.tenant_id = EXCLUDED.tenant_id
		RETURNING id, created_at, updated_at`

	metadata := m.Metadata
	if metadata == nil {
		metadata = []byte("{}")
	}

	return s.db.QueryRowContext(ctx, q,
		m.TenantID, m.AgentID, m.Scope, m.UserID, m.MemoryType, m.Content,
		m.FactKey, m.Confidence, m.Source, m.SourceSessionID, m.SourceTurnNumber,
		metadata, m.EmbeddingCollectionID, m.IsActive,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
}

func (s *PostgresStore) FindByContent(ctx context.Context, tenantID, agentID string, memoryType MemoryType, content string) (*AgentMemory, error) {
	if tenantID == "" {
		return nil, errTenantRequired
	}
	var m AgentMemory
	err := s.db.GetContext(ctx, &m,
		`SELECT * FROM agent_memories WHERE tenant_id = $1 AND agent_id = $2 AND memory_type = $3 AND content = $4 AND is_active = TRUE LIMIT 1`,
		tenantID, agentID, memoryType, content)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("find by content: %w", err)
	}
	return &m, nil
}

// buildWhereClause constructs a WHERE clause from ListOptions. The tenant
// predicate is always present — callers must supply opts.TenantID, and
// List/Count both reject empty before calling this. Without that check,
// any caller could enumerate every tenant's memories by passing an
// agent_id from another tenant.
func buildWhereClause(opts ListOptions) (string, []interface{}) {
	conditions := []string{"tenant_id = $1"}
	args := []interface{}{opts.TenantID}
	argIdx := 2

	if opts.AgentID != "" {
		conditions = append(conditions, fmt.Sprintf("agent_id = $%d", argIdx))
		args = append(args, opts.AgentID)
		argIdx++
	}
	if opts.MemoryType != nil {
		conditions = append(conditions, fmt.Sprintf("memory_type = $%d", argIdx))
		args = append(args, *opts.MemoryType)
		argIdx++
	}
	if opts.Scope != nil {
		conditions = append(conditions, fmt.Sprintf("scope = $%d", argIdx))
		args = append(args, *opts.Scope)
		argIdx++
	}
	if opts.UserID != nil {
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argIdx))
		args = append(args, *opts.UserID)
		argIdx++
	}
	if opts.ActiveOnly {
		conditions = append(conditions, "is_active = TRUE")
	}

	return "WHERE " + strings.Join(conditions, " AND "), args
}
