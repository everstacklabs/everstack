package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/memory"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// DigestConfig controls the periodic bulletin synthesis.
type DigestConfig struct {
	Enabled         bool          `json:"enabled"`
	RefreshInterval time.Duration `json:"refresh_interval"`
	DigestModel     string        `json:"digest_model"`
	MaxBulletinSize int           `json:"max_bulletin_size"` // chars
}

// DefaultDigestConfig returns sensible defaults.
func DefaultDigestConfig() DigestConfig {
	return DigestConfig{
		Enabled:         false,
		RefreshInterval: 60 * time.Minute,
		DigestModel:     "gpt-4o-mini",
		MaxBulletinSize: 4000,
	}
}

// ParseDigestConfig extracts digest configuration from agent config.
func ParseDigestConfig(config map[string]interface{}) DigestConfig {
	cfg := DefaultDigestConfig()
	if config == nil {
		return cfg
	}
	digestRaw, ok := config["digest"]
	if !ok {
		return cfg
	}
	digestMap, ok := digestRaw.(map[string]interface{})
	if !ok {
		return cfg
	}

	if enabled, ok := digestMap["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if interval, ok := digestMap["refresh_interval"].(float64); ok && interval >= 60 {
		cfg.RefreshInterval = time.Duration(interval) * time.Second
	}
	if model, ok := digestMap["digest_model"].(string); ok && model != "" {
		cfg.DigestModel = model
	}
	if maxSize, ok := digestMap["max_bulletin_size"].(float64); ok && maxSize >= 500 {
		cfg.MaxBulletinSize = int(maxSize)
	}
	return cfg
}

// DigestBulletin represents a cached agent knowledge bulletin.
type DigestBulletin struct {
	AgentID   string
	TenantID  string
	Content   string
	Version   int
	UpdatedAt time.Time
}

// digestWorker runs periodically for a single agent, synthesizing memories into a bulletin.
type digestWorker struct {
	agentID  string
	tenantID string
	config   DigestConfig
	engine   *Engine
	store    memory.Store
	db       *sqlx.DB
	cancel   context.CancelFunc
	done     chan struct{}
}

// DigestManager is a registry of digest workers, one per agent.
type DigestManager struct {
	mu        sync.RWMutex
	workers   map[string]*digestWorker   // keyed by agentID
	bulletins map[string]*DigestBulletin // cached bulletins keyed by agentID
	config    DigestConfig
	engine    *Engine
	store     memory.Store
	db        *sqlx.DB
}

// NewDigestManager creates a new digest manager.
func NewDigestManager(config DigestConfig, engine *Engine, store memory.Store, db *sqlx.DB) *DigestManager {
	return &DigestManager{
		workers:   make(map[string]*digestWorker),
		bulletins: make(map[string]*DigestBulletin),
		config:    config,
		engine:    engine,
		store:     store,
		db:        db,
	}
}

// EnsureWorker starts a digest worker for the given agent (idempotent).
func (dm *DigestManager) EnsureWorker(agentID, tenantID string) {
	if !dm.config.Enabled {
		return
	}

	dm.mu.Lock()
	defer dm.mu.Unlock()

	if _, ok := dm.workers[agentID]; ok {
		return // already running
	}

	ctx, cancel := context.WithCancel(bgCtxForTenant(tenantID))
	w := &digestWorker{
		agentID:  agentID,
		tenantID: tenantID,
		config:   dm.config,
		engine:   dm.engine,
		store:    dm.store,
		db:       dm.db,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	dm.workers[agentID] = w

	go w.run(ctx, dm)

	logger.WithFields("agent_id", agentID).Info("digest_manager: started worker")
}

// GetBulletin returns the cached bulletin for an agent, scoped to
// tenant. Pre-fix the DB fallback ran `WHERE agent_id = $1` only —
// any caller could pull another tenant's digest content (which
// summarises conversation history) by guessing an agent id.
func (dm *DigestManager) GetBulletin(agentID, tenantID string) string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	if b, ok := dm.bulletins[agentID]; ok {
		return b.Content
	}

	// Try loading from DB on first access
	if dm.db != nil && tenantID != "" {
		var content string
		err := dm.db.QueryRow(
			`SELECT content FROM agent_digests WHERE agent_id = $1 AND tenant_id = $2 ORDER BY version DESC LIMIT 1`,
			agentID, tenantID,
		).Scan(&content)
		if err == nil && content != "" {
			dm.mu.RUnlock()
			dm.mu.Lock()
			dm.bulletins[agentID] = &DigestBulletin{
				AgentID: agentID,
				Content: content,
			}
			dm.mu.Unlock()
			dm.mu.RLock()
			return content
		}
	}

	return ""
}

// Shutdown stops all workers and waits for them to finish.
func (dm *DigestManager) Shutdown(ctx context.Context) error {
	dm.mu.Lock()
	workers := make([]*digestWorker, 0, len(dm.workers))
	for _, w := range dm.workers {
		w.cancel()
		workers = append(workers, w)
	}
	dm.mu.Unlock()

	done := make(chan struct{})
	go func() {
		for _, w := range workers {
			<-w.done
		}
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the main loop for a digest worker.
func (w *digestWorker) run(ctx context.Context, dm *DigestManager) {
	defer close(w.done)

	// Initial refresh on startup
	w.refresh(ctx, dm)

	ticker := time.NewTicker(w.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.refresh(ctx, dm)
		}
	}
}

// refresh synthesizes a new bulletin from the agent's memories.
func (w *digestWorker) refresh(ctx context.Context, dm *DigestManager) {
	refreshCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Fetch all active memories for this agent
	memories, err := w.store.List(refreshCtx, memory.ListOptions{
		AgentID:    w.agentID,
		TenantID:   w.tenantID,
		ActiveOnly: true,
		Limit:      200,
	})
	if err != nil {
		logger.WithFields("agent_id", w.agentID, "error", err.Error()).
			Warn("digest: failed to fetch memories")
		return
	}

	if len(memories) == 0 {
		return
	}

	// Build a memory dump for the LLM
	var memoryDump strings.Builder
	var memoryIDs []string
	for _, m := range memories {
		memoryDump.WriteString(fmt.Sprintf("[%s/%s] %s\n", m.MemoryType, m.Scope, m.Content))
		memoryIDs = append(memoryIDs, m.ID)
	}

	synthesisPrompt := fmt.Sprintf(`You are synthesizing an agent's knowledge bulletin from its stored memories. The bulletin should be a concise, well-organized summary that captures all important facts, instructions, and context the agent has accumulated.

Format it as clear sections with headers. Keep it under %d characters.

Memories:
%s

Synthesize a knowledge bulletin:`, w.config.MaxBulletinSize, memoryDump.String())

	// Call LLM for synthesis
	model := w.config.DigestModel
	if model == "" {
		model = "gpt-4o-mini"
	}

	provider, _, err := w.engine.ResolveProvider(refreshCtx, model)
	if err != nil {
		logger.WithFields("agent_id", w.agentID, "error", err.Error()).
			Warn("digest: failed to resolve model")
		return
	}

	req := gw.ChatCompletionRequest{
		Model: model,
		Messages: []gw.Message{
			{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(synthesisPrompt)}},
			},
		},
		Sampling: gw.SamplingParams{
			Temperature: 0.3,
			MaxTokens:   2000,
		},
	}

	resp, err := provider.Chat(refreshCtx, req)
	if err != nil {
		logger.WithFields("agent_id", w.agentID, "error", err.Error()).
			Warn("digest: LLM synthesis failed")
		return
	}

	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.Content) == 0 {
		logger.WithFields("agent_id", w.agentID).Warn("digest: LLM produced no output")
		return
	}

	content := ""
	if resp.Choices[0].Message.Content[0].Text != nil {
		content = *resp.Choices[0].Message.Content[0].Text
	}
	if content == "" {
		return
	}

	// Truncate to max bulletin size
	if len(content) > w.config.MaxBulletinSize {
		content = content[:w.config.MaxBulletinSize]
	}

	bulletin := &DigestBulletin{
		AgentID:   w.agentID,
		TenantID:  w.tenantID,
		Content:   content,
		UpdatedAt: time.Now(),
	}

	// Cache in memory
	dm.mu.Lock()
	existing := dm.bulletins[w.agentID]
	version := 1
	if existing != nil {
		version = existing.Version + 1
	}
	bulletin.Version = version
	dm.bulletins[w.agentID] = bulletin
	dm.mu.Unlock()

	// Persist to DB
	if w.db != nil {
		_, dbErr := w.db.ExecContext(refreshCtx, `
			INSERT INTO agent_digests (agent_id, tenant_id, content, version, memories_included, prompt_tokens, completion_tokens, total_tokens, created_at, updated_at)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, NOW(), NOW())
			ON CONFLICT (agent_id, version) DO UPDATE SET content = $3, updated_at = NOW()
		`, w.agentID, w.tenantID, content, version, len(memoryIDs),
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		if dbErr != nil {
			logger.WithFields("agent_id", w.agentID, "error", dbErr.Error()).
				Warn("digest: failed to persist bulletin")
		}
	}

	logger.WithFields(
		"agent_id", w.agentID,
		"version", version,
		"memories_included", len(memoryIDs),
		"bulletin_chars", len(content),
	).Info("digest: bulletin refreshed")
}
