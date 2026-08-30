package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// BranchRecord represents a persisted conversation branch from parallel_tasks, spawn, or fork.
type BranchRecord struct {
	ID               string          `db:"id"`
	SessionID        string          `db:"session_id"`
	BatchID          *string         `db:"batch_id"` // nil for single spawns/forks
	TenantID         string          `db:"tenant_id"`
	AgentID          string          `db:"agent_id"`
	Source           string          `db:"source"` // "parallel_task", "spawn", "fork"
	Instruction      string          `db:"instruction"`
	Conclusion       string          `db:"conclusion"`
	Status           string          `db:"status"`
	Messages         json.RawMessage `db:"messages"` // serialized []gw.Message
	ToolCallsCount   int             `db:"tool_calls_count"`
	PromptTokens     int             `db:"prompt_tokens"`
	CompletionTokens int             `db:"completion_tokens"`
	TotalTokens      int             `db:"total_tokens"`
	DurationMs       int64           `db:"duration_ms"`
	CreatedAt        time.Time       `db:"created_at"`
	CompletedAt      *time.Time      `db:"completed_at"`
	// ParentAttemptID points to the agent_branches row this attempt was
	// retried/forked from. Nil for first attempts. Phase 3a — used by
	// verdict-rate dashboards to follow fix lineages and by Phase 3d CI
	// gating to surface attempts that flipped vs the baseline run.
	ParentAttemptID *string `db:"parent_attempt_id"`

	// HypothesisText is the free-form "what we're testing" string
	// supplied at fork/retry time (e.g. "does sonnet do better with the
	// stricter system prompt?"). Empty for attempts that weren't tagged.
	HypothesisText string `db:"hypothesis_text"`
	// HypothesisDiff is the structured "what changed vs the parent
	// attempt": {model, prompt_template_id, prompt_version,
	// sampling_params, tool_list, system_prompt_diff}. Stored as JSON so
	// callers can extend without schema changes; key set kept stable by
	// the populating Go layer.
	HypothesisDiff json.RawMessage `db:"hypothesis_diff"`
}

// BranchStore persists conversation branches to the database.
type BranchStore struct {
	db *sqlx.DB
}

// NewBranchStore creates a new BranchStore.
func NewBranchStore(db *sqlx.DB) *BranchStore {
	if db == nil {
		return nil
	}
	return &BranchStore{db: db}
}

// SaveBranch persists a branch record.
func (s *BranchStore) SaveBranch(ctx context.Context, b *BranchRecord) error {
	if s == nil || s.db == nil {
		return nil
	}

	now := time.Now()
	if b.CompletedAt == nil {
		b.CompletedAt = &now
	}

	hypothesisDiff := b.HypothesisDiff
	if len(hypothesisDiff) == 0 {
		hypothesisDiff = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_branches
			(id, session_id, batch_id, tenant_id, agent_id, source, instruction,
			 conclusion, status, messages, tool_calls_count, prompt_tokens,
			 completion_tokens, total_tokens, duration_ms, completed_at,
			 parent_attempt_id, hypothesis_text, hypothesis_diff)
		VALUES ($1, $2, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17::uuid, $18, $19::jsonb)
	`, b.ID, b.SessionID, b.BatchID, b.TenantID, b.AgentID, b.Source,
		b.Instruction, b.Conclusion, b.Status, b.Messages,
		b.ToolCallsCount, b.PromptTokens, b.CompletionTokens, b.TotalTokens,
		b.DurationMs, b.CompletedAt, b.ParentAttemptID,
		b.HypothesisText, hypothesisDiff)
	if err != nil {
		logger.WithFields("error", err.Error(), "branch_id", b.ID, "source", b.Source).
			Warn("branch_store: failed to save branch")
	}
	return err
}

// GetBranch retrieves a branch by ID, scoped to tenant. Empty
// tenantID returns (nil, nil) — pre-fix this filtered by id alone,
// which let any caller read another tenant's branch (full
// conversation transcript) by guessing or harvesting an id.
func (s *BranchStore) GetBranch(ctx context.Context, id, tenantID string) (*BranchRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if tenantID == "" {
		return nil, nil
	}
	var b BranchRecord
	err := s.db.GetContext(ctx, &b, `
		SELECT id, session_id, batch_id, tenant_id, agent_id, source, instruction,
		       conclusion, status, messages, tool_calls_count, prompt_tokens,
		       completion_tokens, total_tokens, duration_ms, created_at, completed_at,
		       parent_attempt_id, hypothesis_text, hypothesis_diff
		FROM agent_branches WHERE id = $1 AND tenant_id = $2::uuid
	`, id, tenantID)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBySession returns all branches for a session, scoped to tenant.
func (s *BranchStore) ListBySession(ctx context.Context, sessionID, tenantID string) ([]*BranchRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if tenantID == "" {
		return nil, nil
	}
	var branches []*BranchRecord
	err := s.db.SelectContext(ctx, &branches, `
		SELECT id, session_id, batch_id, tenant_id, agent_id, source, instruction,
		       conclusion, status, messages, tool_calls_count, prompt_tokens,
		       completion_tokens, total_tokens, duration_ms, created_at, completed_at,
		       parent_attempt_id, hypothesis_text, hypothesis_diff
		FROM agent_branches WHERE session_id = $1 AND tenant_id = $2::uuid ORDER BY created_at
	`, sessionID, tenantID)
	return branches, err
}

// ListByBatch returns all branches for a batch (parallel_tasks
// invocation), scoped to tenant.
func (s *BranchStore) ListByBatch(ctx context.Context, batchID, tenantID string) ([]*BranchRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if tenantID == "" {
		return nil, nil
	}
	var branches []*BranchRecord
	err := s.db.SelectContext(ctx, &branches, `
		SELECT id, session_id, batch_id, tenant_id, agent_id, source, instruction,
		       conclusion, status, messages, tool_calls_count, prompt_tokens,
		       completion_tokens, total_tokens, duration_ms, created_at, completed_at,
		       parent_attempt_id, hypothesis_text, hypothesis_diff
		FROM agent_branches WHERE batch_id = $1::uuid AND tenant_id = $2::uuid ORDER BY created_at
	`, batchID, tenantID)
	return branches, err
}
