package memory

import (
	"context"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// MemoryType identifies the kind of memory entry.
type MemoryType string

const (
	MemoryTypeFact           MemoryType = "fact"
	MemoryTypeInstruction    MemoryType = "instruction"
	MemoryTypeSessionSummary MemoryType = "session_summary"
	MemoryTypeDocument       MemoryType = "document"
)

// MemoryScope controls the visibility of a memory entry.
type MemoryScope string

const (
	MemoryScopeAgent  MemoryScope = "agent"
	MemoryScopeUser   MemoryScope = "user"
	MemoryScopeGlobal MemoryScope = "global"
)

// MemorySource identifies how a memory entry was created.
type MemorySource string

const (
	MemorySourceAutoExtracted MemorySource = "auto_extracted"
	MemorySourceUserExplicit  MemorySource = "user_explicit"
	MemorySourceToolStored    MemorySource = "tool_stored"
	MemorySourceConsolidation MemorySource = "consolidation"
)

// AgentMemory represents a single persistent memory entry.
type AgentMemory struct {
	ID                    string       `db:"id" json:"id"`
	TenantID              string       `db:"tenant_id" json:"tenant_id"`
	AgentID               string       `db:"agent_id" json:"agent_id"`
	Scope                 MemoryScope  `db:"scope" json:"scope"`
	UserID                *string      `db:"user_id" json:"user_id,omitempty"`
	MemoryType            MemoryType   `db:"memory_type" json:"memory_type"`
	Content               string       `db:"content" json:"content"`
	FactKey               *string      `db:"fact_key" json:"fact_key,omitempty"`
	Confidence            float64      `db:"confidence" json:"confidence"`
	Source                MemorySource `db:"source" json:"source"`
	SourceSessionID       *string      `db:"source_session_id" json:"source_session_id,omitempty"`
	SourceTurnNumber      *int32       `db:"source_turn_number" json:"source_turn_number,omitempty"`
	Metadata              []byte       `db:"metadata" json:"metadata,omitempty"`
	EmbeddingCollectionID *string      `db:"embedding_collection_id" json:"embedding_collection_id,omitempty"`
	IsActive              bool         `db:"is_active" json:"is_active"`
	AccessCount           int32        `db:"access_count" json:"access_count"`
	LastAccessedAt        *time.Time   `db:"last_accessed_at" json:"last_accessed_at,omitempty"`
	SupersededBy          *string      `db:"superseded_by" json:"superseded_by,omitempty"`
	CreatedAt             time.Time    `db:"created_at" json:"created_at"`
	UpdatedAt             time.Time    `db:"updated_at" json:"updated_at"`
}

// ConsolidationRun tracks a background consolidation execution.
type ConsolidationRun struct {
	ID                string     `db:"id" json:"id"`
	TenantID          string     `db:"tenant_id" json:"tenant_id"`
	AgentID           string     `db:"agent_id" json:"agent_id"`
	Status            string     `db:"status" json:"status"`
	MemoriesProcessed int32      `db:"memories_processed" json:"memories_processed"`
	MemoriesMerged    int32      `db:"memories_merged" json:"memories_merged"`
	StartedAt         *time.Time `db:"started_at" json:"started_at,omitempty"`
	CompletedAt       *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	Error             *string    `db:"error" json:"error,omitempty"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
}

// MemoryConfig is the agent-level configuration for persistent memory.
// Stored in the agent's config JSONB under the "memory" key.
type MemoryConfig struct {
	Enabled          bool        `json:"enabled"`
	Scope            MemoryScope `json:"scope"`
	AutoRetrieve     bool        `json:"auto_retrieve"`
	AutoRetrieveTopK int32       `json:"auto_retrieve_top_k"`
	AutoExtract      bool        `json:"auto_extract"`
	Collections      []string    `json:"collections"`       // vector collection IDs for auto-RAG
	EmbeddingModel   string      `json:"embedding_model"`   // model for embedding queries (e.g. "text-embedding-3-small")
}

// DefaultMemoryConfig returns the default memory configuration.
func DefaultMemoryConfig() MemoryConfig {
	return MemoryConfig{
		Scope:            MemoryScopeAgent,
		AutoRetrieve:     true,
		AutoRetrieveTopK: 10,
		AutoExtract:      true,
	}
}

// ListOptions controls filtering and pagination for memory queries.
type ListOptions struct {
	AgentID    string
	TenantID   string
	MemoryType *MemoryType
	Scope      *MemoryScope
	UserID     *string
	ActiveOnly bool
	Limit      int32
	Offset     int32
}

// Provider is the interface consumed by the agent loop for persistent memory.
// If nil on LoopInput, persistent memory is disabled.
type Provider interface {
	// RetrieveContext fetches relevant memories and formats them for injection
	// into the system prompt. Returns the formatted context string and IDs of
	// accessed memories (for access tracking).
	RetrieveContext(ctx context.Context, agentID, tenantID, userInput string, userID *string) (string, []string, error)

	// ExtractFromTurn extracts facts and instructions from a conversation turn.
	// Runs asynchronously — should not block the user response.
	// userID is optional — when provided, user-scoped memories will be associated with this user.
	ExtractFromTurn(ctx context.Context, agentID, tenantID, sessionID, userInput, assistantOutput string, turnNumber int32, userID *string) error

	// SummarizeSession creates a session summary from the full conversation.
	SummarizeSession(ctx context.Context, agentID, tenantID, sessionID string, messages []gw.Message) error

	// IncrementAccess bumps access counts for the given memory IDs,
	// scoped to a tenant.
	IncrementAccess(ctx context.Context, tenantID string, memoryIDs []string) error
}
