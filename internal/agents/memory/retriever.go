package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	vecmem "github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
)

// Retriever fetches relevant memories and formats them for system prompt injection.
type Retriever struct {
	store       Store
	vectorStore vecmem.VectorStore // may be nil if no vector backend
	embedder    vecmem.EmbedderInterface
	config      MemoryConfig
}

// NewRetriever creates a new memory retriever.
func NewRetriever(store Store, vectorStore vecmem.VectorStore, embedder vecmem.EmbedderInterface, config MemoryConfig) *Retriever {
	return &Retriever{
		store:       store,
		vectorStore: vectorStore,
		embedder:    embedder,
		config:      config,
	}
}

// RetrieveResult contains retrieved memories and their IDs for access tracking.
type RetrieveResult struct {
	ContextBlock string
	MemoryIDs    []string
}

// Retrieve fetches relevant memories across all scopes and formats them into a prompt block.
// The hierarchical priority is: user-scoped > agent-scoped > global-scoped.
// When duplicate fact_keys exist across scopes, the narrower scope wins.
func (r *Retriever) Retrieve(ctx context.Context, agentID, tenantID, userInput string, userID *string) (*RetrieveResult, error) {
	ctx, span := telemetry.StartMemorySpan(ctx, "retrieve", agentID)
	defer span.End()

	topK := r.config.AutoRetrieveTopK
	if topK <= 0 {
		topK = 10
	}

	// Fetch memories from all three scopes
	userFacts, userInstructions := r.fetchByScope(ctx, agentID, tenantID, MemoryScopeUser, userID, topK)
	agentFacts, agentInstructions := r.fetchByScope(ctx, agentID, tenantID, MemoryScopeAgent, nil, topK)
	globalFacts, globalInstructions := r.fetchByScope(ctx, agentID, tenantID, MemoryScopeGlobal, nil, topK)

	// Fetch session summaries (agent-scoped only — summaries are per-agent)
	summType := MemoryTypeSessionSummary
	summaries, err := r.store.List(ctx, ListOptions{
		AgentID:    agentID,
		TenantID:   tenantID,
		MemoryType: &summType,
		ActiveOnly: true,
		Limit:      5,
	})
	if err != nil {
		logger.WithFields("agent_id", agentID, "error", err.Error()).
			Warn("memory retriever: failed to fetch session summaries")
		summaries = nil
	}

	// Deduplicate facts by fact_key: user > agent > global
	deduped := deduplicateFacts(userFacts, agentFacts, globalFacts)

	// Merge instructions (no dedup needed — just union)
	allInstructions := mergeMemories(userInstructions, agentInstructions, globalInstructions)

	// Collect all memory IDs for access tracking
	var memoryIDs []string
	for _, m := range deduped {
		memoryIDs = append(memoryIDs, m.ID)
	}
	for _, m := range allInstructions {
		memoryIDs = append(memoryIDs, m.ID)
	}
	for _, m := range summaries {
		memoryIDs = append(memoryIDs, m.ID)
	}

	// Cap total memories
	totalCap := int(topK) * 3 // generous cap
	if len(memoryIDs) > totalCap {
		memoryIDs = memoryIDs[:totalCap]
	}

	// Auto-RAG from vector collections
	var ragResults []string
	if len(r.config.Collections) > 0 && r.vectorStore != nil && r.embedder != nil && userInput != "" {
		ragResults = r.queryCollections(ctx, tenantID, userInput)
	}

	// Format into prompt block
	block := formatScopedMemoryBlock(deduped, allInstructions, summaries, ragResults)

	span.SetAttributes(attribute.Int(attrs.MemoryResultCount, len(memoryIDs)))
	return &RetrieveResult{
		ContextBlock: block,
		MemoryIDs:    memoryIDs,
	}, nil
}

// fetchByScope retrieves facts and instructions for a given scope.
func (r *Retriever) fetchByScope(ctx context.Context, agentID, tenantID string, scope MemoryScope, userID *string, topK int32) ([]*AgentMemory, []*AgentMemory) {
	scopeVal := scope
	factType := MemoryTypeFact
	instrType := MemoryTypeInstruction

	baseOpts := ListOptions{
		TenantID:   tenantID,
		Scope:      &scopeVal,
		ActiveOnly: true,
	}

	// For global scope, don't filter by agentID
	if scope != MemoryScopeGlobal {
		baseOpts.AgentID = agentID
	}
	if scope == MemoryScopeUser && userID != nil {
		baseOpts.UserID = userID
	} else if scope == MemoryScopeUser && userID == nil {
		// Can't fetch user-scoped without a user ID
		return nil, nil
	}

	factOpts := baseOpts
	factOpts.MemoryType = &factType
	factOpts.Limit = topK

	facts, err := r.store.List(ctx, factOpts)
	if err != nil {
		logger.WithFields("agent_id", agentID, "scope", string(scope), "error", err.Error()).
			Warn("memory retriever: failed to fetch facts")
		facts = nil
	}

	instrOpts := baseOpts
	instrOpts.MemoryType = &instrType
	instrOpts.Limit = 20

	instructions, err := r.store.List(ctx, instrOpts)
	if err != nil {
		logger.WithFields("agent_id", agentID, "scope", string(scope), "error", err.Error()).
			Warn("memory retriever: failed to fetch instructions")
		instructions = nil
	}

	return facts, instructions
}

// deduplicateFacts merges facts from multiple scopes, preferring narrower scope on duplicate fact_key.
// Priority: user (highest) > agent > global (lowest).
func deduplicateFacts(userFacts, agentFacts, globalFacts []*AgentMemory) []*AgentMemory {
	seen := make(map[string]bool) // fact_key -> already added
	var result []*AgentMemory

	addFacts := func(facts []*AgentMemory) {
		for _, f := range facts {
			key := ""
			if f.FactKey != nil {
				key = *f.FactKey
			}
			if key != "" && seen[key] {
				continue // already have a higher-priority version
			}
			if key != "" {
				seen[key] = true
			}
			result = append(result, f)
		}
	}

	// Add in priority order: user first, then agent, then global
	addFacts(userFacts)
	addFacts(agentFacts)
	addFacts(globalFacts)

	return result
}

// mergeMemories combines memories from multiple slices, removing duplicates by ID.
func mergeMemories(slices ...[]*AgentMemory) []*AgentMemory {
	seen := make(map[string]bool)
	var result []*AgentMemory
	for _, slice := range slices {
		for _, m := range slice {
			if !seen[m.ID] {
				seen[m.ID] = true
				result = append(result, m)
			}
		}
	}
	return result
}

// queryCollections performs auto-RAG by querying vector memory collections.
func (r *Retriever) queryCollections(ctx context.Context, tenantID, userInput string) []string {
	embeddingModel := r.config.EmbeddingModel
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}

	embedding, err := r.embedder.Embed(ctx, embeddingModel, userInput)
	if err != nil {
		logger.WithFields("error", err.Error()).
			Warn("memory retriever: failed to embed user input for auto-RAG")
		return nil
	}

	var results []string
	for _, collectionID := range r.config.Collections {
		docs, err := r.vectorStore.Query(ctx, collectionID, embedding, vecmem.QueryOptions{TopK: 3})
		if err != nil {
			logger.WithFields("collection_id", collectionID, "error", err.Error()).
				Warn("memory retriever: failed to query collection")
			continue
		}
		for _, doc := range docs {
			if doc.ChunkText != "" {
				results = append(results, doc.ChunkText)
			}
		}
	}
	return results
}

// formatScopedMemoryBlock creates the <agent_memory> prompt block with scoped sections.
func formatScopedMemoryBlock(facts, instructions, summaries []*AgentMemory, ragResults []string) string {
	if len(facts) == 0 && len(instructions) == 0 && len(summaries) == 0 && len(ragResults) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n<agent_memory>\n")

	// Group facts by scope for display
	userFacts, agentFacts, globalFacts := groupByScope(facts)

	if len(userFacts) > 0 {
		sb.WriteString("## Personal Context (User-specific)\n")
		writeFacts(&sb, userFacts)
		sb.WriteString("\n")
	}

	if len(agentFacts) > 0 {
		sb.WriteString("## Agent Knowledge\n")
		writeFacts(&sb, agentFacts)
		sb.WriteString("\n")
	}

	if len(globalFacts) > 0 {
		sb.WriteString("## Organization Knowledge\n")
		writeFacts(&sb, globalFacts)
		sb.WriteString("\n")
	}

	if len(instructions) > 0 {
		sb.WriteString("## Instructions\n")
		for _, i := range instructions {
			sb.WriteString(fmt.Sprintf("- %s\n", i.Content))
		}
		sb.WriteString("\n")
	}

	if len(summaries) > 0 {
		sb.WriteString("## Recent Session Context\n")
		for _, s := range summaries {
			age := formatAge(s.CreatedAt)
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", age, s.Content))
		}
		sb.WriteString("\n")
	}

	if len(ragResults) > 0 {
		sb.WriteString("## Reference Documents\n")
		for _, doc := range ragResults {
			sb.WriteString(fmt.Sprintf("- %s\n", doc))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("</agent_memory>")
	return sb.String()
}

// groupByScope splits memories into user/agent/global buckets.
func groupByScope(memories []*AgentMemory) (user, agent, global []*AgentMemory) {
	for _, m := range memories {
		switch m.Scope {
		case MemoryScopeUser:
			user = append(user, m)
		case MemoryScopeGlobal:
			global = append(global, m)
		default:
			agent = append(agent, m)
		}
	}
	return
}

// writeFacts writes a list of facts to a string builder.
func writeFacts(sb *strings.Builder, facts []*AgentMemory) {
	for _, f := range facts {
		key := ""
		if f.FactKey != nil {
			key = *f.FactKey + ": "
		}
		sb.WriteString(fmt.Sprintf("- %s%s\n", key, f.Content))
	}
}

// formatAge returns a human-readable age string.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
