package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	"github.com/jmoiron/sqlx"
)

// Consolidator merges duplicate/overlapping memories in the background.
// It groups facts by key prefix, merges duplicates via LLM, and collapses
// old session summaries into long-term summaries.
type Consolidator struct {
	store     Store
	extractor *Extractor
	db        *sqlx.DB

	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewConsolidator creates a new background consolidator.
func NewConsolidator(store Store, extractor *Extractor, db *sqlx.DB, interval time.Duration) *Consolidator {
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	return &Consolidator{
		store:     store,
		extractor: extractor,
		db:        db,
		interval:  interval,
		stopCh:    make(chan struct{}),
	}
}

// Start begins the background consolidation loop.
func (c *Consolidator) Start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				if err := c.RunAll(context.Background()); err != nil {
					logger.WithFields("error", err.Error()).Error("memory consolidator: run failed")
				}
			}
		}
	}()
}

// Stop signals the consolidator to stop and waits for completion.
func (c *Consolidator) Stop() {
	close(c.stopCh)
	c.wg.Wait()
}

// RunAll runs consolidation for all agents that have active memories.
func (c *Consolidator) RunAll(ctx context.Context) error {
	rows, err := c.db.QueryxContext(ctx,
		`SELECT DISTINCT agent_id, tenant_id FROM agent_memories WHERE is_active = TRUE`,
	)
	if err != nil {
		return fmt.Errorf("list agents with memories: %w", err)
	}
	defer rows.Close()

	type agentTenant struct {
		AgentID  string `db:"agent_id"`
		TenantID string `db:"tenant_id"`
	}

	var agents []agentTenant
	for rows.Next() {
		var at agentTenant
		if err := rows.StructScan(&at); err != nil {
			continue
		}
		agents = append(agents, at)
	}

	for _, at := range agents {
		if err := c.ConsolidateAgent(ctx, at.AgentID, at.TenantID); err != nil {
			logger.WithFields(
				"agent_id", at.AgentID,
				"error", err.Error(),
			).Warn("memory consolidator: agent consolidation failed")
		}
	}
	return nil
}

// ConsolidateAgent runs consolidation for a single agent.
func (c *Consolidator) ConsolidateAgent(ctx context.Context, agentID, tenantID string) error {
	ctx, span := telemetry.StartMemorySpan(ctx, "consolidate", agentID)
	defer span.End()

	run, err := c.createRun(ctx, agentID, tenantID)
	if err != nil {
		telemetry.RecordError(span, err)
		return fmt.Errorf("create consolidation run: %w", err)
	}

	var processed, merged int32
	var runErr error

	defer func() {
		c.completeRun(ctx, run.ID, processed, merged, runErr)
	}()

	// 1. Merge duplicate facts by key
	m1, p1, err := c.mergeDuplicateFacts(ctx, agentID, tenantID)
	if err != nil {
		runErr = err
		telemetry.RecordError(span, err)
		return err
	}
	merged += m1
	processed += p1

	// 2. Collapse old session summaries (>30 days)
	m2, p2, err := c.collapseOldSummaries(ctx, agentID, tenantID)
	if err != nil {
		runErr = err
		telemetry.RecordError(span, err)
		return err
	}
	merged += m2
	processed += p2

	logger.WithFields(
		"agent_id", agentID,
		"processed", processed,
		"merged", merged,
	).Info("memory consolidator: completed")

	return nil
}

// mergeDuplicateFacts finds facts that share the same fact_key and merges them.
func (c *Consolidator) mergeDuplicateFacts(ctx context.Context, agentID, tenantID string) (merged, processed int32, err error) {
	// Find fact_keys with multiple active entries, scoped to the caller's
	// tenant — without the tenant predicate the count would aggregate
	// across every tenant's memories for the same agent_id.
	rows, err := c.db.QueryxContext(ctx,
		`SELECT fact_key, COUNT(*) as cnt
		 FROM agent_memories
		 WHERE agent_id = $1 AND tenant_id = $2 AND is_active = TRUE AND memory_type = 'fact' AND fact_key IS NOT NULL
		 GROUP BY fact_key
		 HAVING COUNT(*) > 1`,
		agentID, tenantID,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("find duplicate fact keys: %w", err)
	}
	defer rows.Close()

	type factKeyCount struct {
		FactKey string `db:"fact_key"`
		Count   int32  `db:"cnt"`
	}

	var duplicates []factKeyCount
	for rows.Next() {
		var fk factKeyCount
		if err := rows.StructScan(&fk); err != nil {
			continue
		}
		duplicates = append(duplicates, fk)
	}

	for _, dup := range duplicates {
		// Fetch all entries for this key
		factType := MemoryTypeFact
		entries, err := c.store.List(ctx, ListOptions{
			TenantID:   tenantID,
			AgentID:    agentID,
			MemoryType: &factType,
			ActiveOnly: true,
			Limit:      100,
		})
		if err != nil {
			continue
		}

		// Filter to matching fact_key
		var matching []*AgentMemory
		for _, e := range entries {
			if e.FactKey != nil && *e.FactKey == dup.FactKey {
				matching = append(matching, e)
			}
		}
		if len(matching) <= 1 {
			continue
		}

		processed += int32(len(matching))

		// Use LLM to merge
		mergedFact, err := c.mergeFacts(ctx, matching)
		if err != nil {
			logger.WithFields(
				"agent_id", agentID,
				"fact_key", dup.FactKey,
				"error", err.Error(),
			).Warn("memory consolidator: failed to merge facts")
			continue
		}

		// Save merged fact
		if err := c.store.Save(ctx, mergedFact); err != nil {
			continue
		}

		// Supersede old entries
		for _, old := range matching {
			if err := c.store.Supersede(ctx, tenantID, old.ID, mergedFact.ID); err != nil {
				logger.WithFields("old_id", old.ID, "error", err.Error()).
					Warn("memory consolidator: failed to supersede")
			}
		}
		merged += int32(len(matching))
	}

	return merged, processed, nil
}

// collapseOldSummaries merges session summaries older than 30 days into
// a single long-term summary.
func (c *Consolidator) collapseOldSummaries(ctx context.Context, agentID, tenantID string) (merged, processed int32, err error) {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	summaryType := MemoryTypeSessionSummary
	summaries, err := c.store.List(ctx, ListOptions{
		TenantID:   tenantID,
		AgentID:    agentID,
		MemoryType: &summaryType,
		ActiveOnly: true,
		Limit:      200,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list old summaries: %w", err)
	}

	// Filter to old summaries only
	var old []*AgentMemory
	for _, s := range summaries {
		if s.CreatedAt.Before(cutoff) {
			old = append(old, s)
		}
	}

	if len(old) < 3 {
		return 0, 0, nil // not enough to justify consolidation
	}

	processed = int32(len(old))

	// Build combined text for LLM
	var sb strings.Builder
	for _, s := range old {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", s.CreatedAt.Format("2006-01-02"), s.Content))
	}

	resp, err := c.extractor.callLLM(ctx,
		`You are a memory consolidation system. Given multiple session summaries from an AI agent's past conversations, produce a single consolidated long-term summary.
Keep the most important patterns, decisions, and outcomes. Limit to 3-5 sentences.
Respond with just the summary text.`,
		sb.String(),
	)
	if err != nil {
		return 0, processed, fmt.Errorf("consolidation LLM call: %w", err)
	}

	consolidated := strings.TrimSpace(resp)
	if consolidated == "" {
		return 0, processed, nil
	}

	// Save the merged summary
	m := &AgentMemory{
		TenantID:   tenantID,
		AgentID:    agentID,
		Scope:      MemoryScopeAgent,
		MemoryType: MemoryTypeSessionSummary,
		Content:    consolidated,
		Confidence: 1.0,
		Source:     MemorySourceConsolidation,
		IsActive:   true,
	}
	if err := c.store.Save(ctx, m); err != nil {
		return 0, processed, fmt.Errorf("save consolidated summary: %w", err)
	}

	// Deactivate old summaries
	for _, s := range old {
		if err := c.store.Deactivate(ctx, tenantID, s.ID); err != nil {
			logger.WithFields("id", s.ID, "error", err.Error()).
				Warn("memory consolidator: failed to deactivate old summary")
		}
	}

	merged = int32(len(old))
	return merged, processed, nil
}

// mergeFacts uses the LLM to merge multiple facts with the same key into one.
func (c *Consolidator) mergeFacts(ctx context.Context, facts []*AgentMemory) (*AgentMemory, error) {
	var sb strings.Builder
	for _, f := range facts {
		key := ""
		if f.FactKey != nil {
			key = *f.FactKey
		}
		sb.WriteString(fmt.Sprintf("- key=%s value=%q confidence=%.2f created=%s\n",
			key, f.Content, f.Confidence, f.CreatedAt.Format(time.RFC3339)))
	}

	resp, err := c.extractor.callLLM(ctx, consolidationPrompt, sb.String())
	if err != nil {
		return nil, fmt.Errorf("merge LLM call: %w", err)
	}

	var result struct {
		Key        string  `json:"key"`
		Value      string  `json:"value"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse merge result: %w", err)
	}

	// Use the first fact as a template for tenant/agent IDs
	template := facts[0]
	return &AgentMemory{
		TenantID:   template.TenantID,
		AgentID:    template.AgentID,
		Scope:      template.Scope,
		MemoryType: MemoryTypeFact,
		Content:    result.Value,
		FactKey:    &result.Key,
		Confidence: result.Confidence,
		Source:     MemorySourceConsolidation,
		IsActive:   true,
	}, nil
}

// createRun creates a new consolidation run record.
func (c *Consolidator) createRun(ctx context.Context, agentID, tenantID string) (*ConsolidationRun, error) {
	now := time.Now()
	run := &ConsolidationRun{
		TenantID:  tenantID,
		AgentID:   agentID,
		Status:    "running",
		StartedAt: &now,
	}

	const q = `
		INSERT INTO agent_memory_consolidation_runs (tenant_id, agent_id, status, started_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	err := c.db.QueryRowxContext(ctx, q, tenantID, agentID, run.Status, now).Scan(&run.ID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

// completeRun updates a consolidation run with final results.
func (c *Consolidator) completeRun(ctx context.Context, runID string, processed, merged int32, runErr error) {
	now := time.Now()
	status := "completed"
	var errStr *string
	if runErr != nil {
		status = "failed"
		s := runErr.Error()
		errStr = &s
	}

	_, err := c.db.ExecContext(ctx,
		`UPDATE agent_memory_consolidation_runs
		 SET status = $1, memories_processed = $2, memories_merged = $3, completed_at = $4, error = $5
		 WHERE id = $6`,
		status, processed, merged, now, errStr, runID,
	)
	if err != nil {
		logger.WithFields("run_id", runID, "error", err.Error()).
			Warn("memory consolidator: failed to update run record")
	}
}
