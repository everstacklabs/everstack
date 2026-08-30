package memory

import (
	"context"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	vecmem "github.com/everstacklabs/everstack/internal/memory"
)

// AgentMemoryProvider implements the Provider interface by composing a Retriever and Extractor.
type AgentMemoryProvider struct {
	retriever *Retriever
	extractor *Extractor
	store     Store
	config    MemoryConfig
}

// NewAgentMemoryProvider creates a new provider with all dependencies.
func NewAgentMemoryProvider(
	store Store,
	vectorStore vecmem.VectorStore,
	embedder vecmem.EmbedderInterface,
	router *gw.Router,
	extractionModel string,
	config MemoryConfig,
) *AgentMemoryProvider {
	return &AgentMemoryProvider{
		retriever: NewRetriever(store, vectorStore, embedder, config),
		extractor: NewExtractor(store, router, extractionModel),
		store:     store,
		config:    config,
	}
}

// RetrieveContext fetches relevant memories and formats them for system prompt injection.
func (p *AgentMemoryProvider) RetrieveContext(ctx context.Context, agentID, tenantID, userInput string, userID *string) (string, []string, error) {
	if !p.config.AutoRetrieve {
		return "", nil, nil
	}

	result, err := p.retriever.Retrieve(ctx, agentID, tenantID, userInput, userID)
	if err != nil {
		logger.WithFields("agent_id", agentID, "error", err.Error()).
			Warn("memory provider: retrieval failed, continuing without memory")
		return "", nil, nil // non-fatal
	}

	return result.ContextBlock, result.MemoryIDs, nil
}

// ExtractFromTurn extracts facts and instructions from a conversation turn.
func (p *AgentMemoryProvider) ExtractFromTurn(ctx context.Context, agentID, tenantID, sessionID, userInput, assistantOutput string, turnNumber int32, userID *string) error {
	if !p.config.AutoExtract {
		return nil
	}
	return p.extractor.Extract(ctx, agentID, tenantID, sessionID, userInput, assistantOutput, turnNumber, userID)
}

// SummarizeSession creates a session summary from the full conversation.
func (p *AgentMemoryProvider) SummarizeSession(ctx context.Context, agentID, tenantID, sessionID string, messages []gw.Message) error {
	return p.extractor.Summarize(ctx, agentID, tenantID, sessionID, messages)
}

// IncrementAccess bumps access counts for retrieved memories. Tenant-scoped
// so a stray retrieval against another tenant's ids cannot bump their
// counters.
func (p *AgentMemoryProvider) IncrementAccess(ctx context.Context, tenantID string, memoryIDs []string) error {
	return p.store.IncrementAccess(ctx, tenantID, memoryIDs)
}

// ParseMemoryConfig parses the memory config from the agent's config JSONB.
// Returns nil if memory is not configured or not enabled.
func ParseMemoryConfig(agentConfig map[string]interface{}) *MemoryConfig {
	raw, ok := agentConfig["memory"]
	if !ok {
		return nil
	}

	memMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}

	enabled, _ := memMap["enabled"].(bool)
	if !enabled {
		return nil
	}

	cfg := DefaultMemoryConfig()
	cfg.Enabled = true

	if scope, ok := memMap["scope"].(string); ok {
		cfg.Scope = MemoryScope(scope)
	}
	if ar, ok := memMap["auto_retrieve"].(bool); ok {
		cfg.AutoRetrieve = ar
	}
	if topK, ok := memMap["auto_retrieve_top_k"].(float64); ok && topK > 0 {
		cfg.AutoRetrieveTopK = int32(topK)
	}
	if ae, ok := memMap["auto_extract"].(bool); ok {
		cfg.AutoExtract = ae
	}
	if em, ok := memMap["embedding_model"].(string); ok && em != "" {
		cfg.EmbeddingModel = em
	}
	if collections, ok := memMap["collections"].([]interface{}); ok {
		for _, c := range collections {
			if s, ok := c.(string); ok && s != "" {
				cfg.Collections = append(cfg.Collections, s)
			}
		}
	}

	return &cfg
}
