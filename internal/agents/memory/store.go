package memory

import "context"

// Store is the persistence layer for agent memories.
//
// Every read / mutate method takes a tenantID and the SQL filters on it.
// Earlier versions only filtered by row id (or agent id), which let any
// caller who guessed an id from another tenant read or mutate that
// tenant's memories — the same shape of leak as the workspace_repo and
// api_keys cases closed in #30 and #33.
type Store interface {
	// Save persists a single memory entry. The ID field is set on return.
	Save(ctx context.Context, m *AgentMemory) error

	// SaveBatch persists multiple memory entries in a single transaction.
	SaveBatch(ctx context.Context, memories []*AgentMemory) error

	// Get retrieves a single memory by id, scoped to a tenant.
	Get(ctx context.Context, tenantID, id string) (*AgentMemory, error)

	// List retrieves memories matching the given options. opts.TenantID
	// is required; the SQL refuses to run with an empty value.
	List(ctx context.Context, opts ListOptions) ([]*AgentMemory, error)

	// Count returns the number of memories matching the given options.
	// Same tenant requirement as List.
	Count(ctx context.Context, opts ListOptions) (int64, error)

	// Deactivate marks a memory as inactive (soft delete), tenant-scoped.
	Deactivate(ctx context.Context, tenantID, id string) error

	// Supersede marks a memory as superseded by another (for fact updates),
	// tenant-scoped.
	Supersede(ctx context.Context, tenantID, oldID, newID string) error

	// IncrementAccess bumps access_count and sets last_accessed_at for the
	// given IDs, scoped to a tenant.
	IncrementAccess(ctx context.Context, tenantID string, ids []string) error

	// DeleteByAgent hard-deletes all memories for an agent within a
	// tenant. Use with care.
	DeleteByAgent(ctx context.Context, tenantID, agentID string) error

	// Update modifies the content, fact_key, and confidence of a memory
	// entry, tenant-scoped.
	Update(ctx context.Context, tenantID, id string, content string, factKey *string, confidence float64) error

	// Delete hard-deletes a single memory by id, tenant-scoped.
	Delete(ctx context.Context, tenantID, id string) error

	// FindByFactKey returns the active memory with the given fact_key for
	// an agent within a tenant. Returns nil, nil if not found.
	FindByFactKey(ctx context.Context, tenantID, agentID, factKey string) (*AgentMemory, error)

	// UpsertSummary inserts a session summary or updates it if one already
	// exists for the same (tenant_id, agent_id, source_session_id).
	UpsertSummary(ctx context.Context, m *AgentMemory) error

	// FindByContent returns an active memory matching exact content for
	// the given agent and type within a tenant. Returns nil, nil if not
	// found.
	FindByContent(ctx context.Context, tenantID, agentID string, memoryType MemoryType, content string) (*AgentMemory, error)
}
