package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/compact"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// CompactionTier is preserved as a re-export over compact.Tier for
// callers that still import the runtime constants. New code should use
// the compact package directly.
type CompactionTier = compact.Tier

const (
	CompactionTierBackground = compact.TierBackground
	CompactionTierAggressive = compact.TierAggressive
	CompactionTierEmergency  = compact.TierEmergency
)

// MonitorConfig is a thin alias around compact.Config kept for
// backwards compatibility with code that constructs a Monitor.
type MonitorConfig = compact.Config

// DefaultMonitorConfig returns the canonical defaults.
func DefaultMonitorConfig() MonitorConfig { return compact.DefaultConfig() }

// ParseMonitorConfig extracts monitor configuration from agent config.
// The shape mirrors what gateway.yaml's features.compact block uses,
// so the same JSON fragment can drive both the agent runtime and the
// gateway middleware.
func ParseMonitorConfig(config map[string]interface{}) MonitorConfig {
	cfg := DefaultMonitorConfig()
	if config == nil {
		return cfg
	}
	monitorRaw, ok := config["monitor"]
	if !ok {
		return cfg
	}
	monitorMap, ok := monitorRaw.(map[string]interface{})
	if !ok {
		return cfg
	}

	if enabled, ok := monitorMap["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if maxTokens, ok := monitorMap["max_context_tokens"].(float64); ok && maxTokens >= 1000 {
		cfg.MaxContextTokens = int(maxTokens)
	}
	if bg, ok := monitorMap["background_threshold"].(float64); ok && bg > 0 && bg < 1 {
		cfg.BackgroundThreshold = bg
	}
	if ag, ok := monitorMap["aggressive_threshold"].(float64); ok && ag > 0 && ag < 1 {
		cfg.AggressiveThreshold = ag
	}
	if em, ok := monitorMap["emergency_threshold"].(float64); ok && em > 0 && em < 1 {
		cfg.EmergencyThreshold = em
	}
	if model, ok := monitorMap["summarization_model"].(string); ok && model != "" {
		cfg.SummarizationModel = model
	}
	return cfg
}

// CompactRequest tells the loop to replace a range of messages with a
// summary. Field shape is preserved so loop.go's existing consumer
// (drained from CompactCh) doesn't need to change.
type CompactRequest struct {
	ReplaceStart   int            // index in messages to start replacing
	ReplaceEnd     int            // index in messages to stop replacing (exclusive)
	SummaryMessage gw.Message     // the replacement summary message
	Tier           CompactionTier // urgency tier
	FreedTokens    int            // estimated tokens freed
}

// ContextSummary tracks a single compaction event for diagnostics.
type ContextSummary struct {
	Tier        CompactionTier
	Content     string
	FreedTokens int
	CreatedAt   time.Time
}

// Monitor observes token usage per-session and triggers compaction
// when context window utilization exceeds configured thresholds. The
// algorithm itself lives in internal/lib/handlers/gateway/compact —
// this Monitor is the agent-runtime adapter that adds session
// identity, async event emission, and channel-based handoff to the
// run loop.
type Monitor struct {
	config       MonitorConfig
	engine       *Engine
	emitter      *Emitter
	compactor    *compact.Compactor
	compactCh    chan CompactRequest
	summaryStack []ContextSummary
	mu           sync.Mutex
	sessionID    string
}

// NewMonitor creates a new context compaction monitor.
func NewMonitor(config MonitorConfig, engine *Engine, emitter *Emitter, sessionID string) *Monitor {
	return &Monitor{
		config:    config,
		engine:    engine,
		emitter:   emitter,
		compactor: compact.New(config),
		compactCh: make(chan CompactRequest, 4),
		sessionID: sessionID,
	}
}

// SetEmitter sets the emitter after construction (for deferred wiring).
func (m *Monitor) SetEmitter(e *Emitter) { m.emitter = e }

// CompactCh returns the channel the loop reads compaction requests from.
func (m *Monitor) CompactCh() <-chan CompactRequest { return m.compactCh }

// ObserveTurnEnd is called by the loop after each LLM call with the
// actual prompt token count from the response. It asks the compactor
// what to do, then either emits a hard-truncate request straight to
// the channel or kicks off a summarisation.
func (m *Monitor) ObserveTurnEnd(ctx context.Context, messages []gw.Message, promptTokens int) {
	if m.compactor == nil {
		// Defensive — should never happen since NewMonitor always wires it.
		return
	}
	decision := m.compactor.Decide(messages, promptTokens)
	switch decision.Action {
	case compact.ActionNone:
		return
	case compact.ActionTruncate:
		m.handleTruncate(decision)
	case compact.ActionSummarize:
		m.handleSummarize(ctx, messages, decision)
	}
}

// handleTruncate sends a hard-truncation request to the loop with no
// LLM call — the emergency-tier path.
func (m *Monitor) handleTruncate(d compact.Decision) {
	m.emitter.Emit(Event{
		Type:      EventCompactionTriggered,
		SessionID: m.sessionID,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"tier":          string(d.Tier),
			"replace_start": d.ReplaceStart,
			"replace_end":   d.ReplaceEnd,
		},
	})

	dropped := d.ReplaceEnd - d.ReplaceStart
	summaryText := fmt.Sprintf("[Context compacted: %d earlier messages were removed to free context space. The conversation continues below.]", dropped)
	m.dispatch(CompactRequest{
		ReplaceStart: d.ReplaceStart,
		ReplaceEnd:   d.ReplaceEnd,
		SummaryMessage: gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: ptr(summaryText)}},
		},
		Tier:        d.Tier,
		FreedTokens: d.FreedTokens,
	}, summaryText)
}

// handleSummarize runs the summariser model over the compacted range
// and dispatches the resulting summary message to the loop. On error
// emits EventCompactionFailed and skips this turn rather than sending
// a bogus summary.
func (m *Monitor) handleSummarize(ctx context.Context, messages []gw.Message, d compact.Decision) {
	m.emitter.Emit(Event{
		Type:      EventCompactionTriggered,
		SessionID: m.sessionID,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"tier":            string(d.Tier),
			"message_count":   len(messages),
			"summarize_count": d.ReplaceEnd - d.ReplaceStart,
		},
	})

	provider, _, err := m.engine.ResolveProvider(ctx, m.config.SummarizationModel)
	if err != nil {
		m.summarizationFailed(d.Tier, fmt.Errorf("resolve summarisation model %s: %w", m.config.SummarizationModel, err))
		return
	}

	summarizer := compact.NewSummarizer(provider, m.config.SummarizationModel)
	summaryText, err := summarizer.Summarize(ctx, messages[d.ReplaceStart:d.ReplaceEnd])
	if err != nil {
		m.summarizationFailed(d.Tier, err)
		return
	}

	formattedSummary := fmt.Sprintf("[Context summary (%s compaction)]\n%s", d.Tier, summaryText)
	m.dispatch(CompactRequest{
		ReplaceStart: d.ReplaceStart,
		ReplaceEnd:   d.ReplaceEnd,
		SummaryMessage: gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: ptr(formattedSummary)}},
		},
		Tier:        d.Tier,
		FreedTokens: d.FreedTokens,
	}, formattedSummary)
}

// summarizationFailed logs + emits EventCompactionFailed without
// blocking the run loop. We intentionally do not fall back to
// truncation here — the agent will hit the emergency threshold on the
// next turn and truncate then.
func (m *Monitor) summarizationFailed(tier CompactionTier, err error) {
	logger.WithFields("session_id", m.sessionID, "error", err.Error()).
		Warn("monitor: summarization failed")
	m.emitter.Emit(Event{
		Type:      EventCompactionFailed,
		SessionID: m.sessionID,
		Timestamp: time.Now(),
		Error:     err.Error(),
		Data:      map[string]interface{}{"tier": string(tier)},
	})
}

// dispatch sends a CompactRequest down the channel and records the
// event in the session's summary stack. Drops if the channel is full
// (caller already consumed back-pressure).
func (m *Monitor) dispatch(req CompactRequest, summaryText string) {
	select {
	case m.compactCh <- req:
		m.mu.Lock()
		m.summaryStack = append(m.summaryStack, ContextSummary{
			Tier:        req.Tier,
			Content:     summaryText,
			FreedTokens: req.FreedTokens,
			CreatedAt:   time.Now(),
		})
		m.mu.Unlock()
	default:
		logger.WithFields("session_id", m.sessionID, "tier", string(req.Tier)).
			Warn("monitor: compact channel full, skipping dispatch")
	}
}

// SummaryStack returns a copy of the current summary history.
func (m *Monitor) SummaryStack() []ContextSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]ContextSummary, len(m.summaryStack))
	copy(result, m.summaryStack)
	return result
}

// ptr is a local helper for *string — strPtr already exists in
// engine.go but lives in the same package, so we'd shadow it. Keep
// this one local to monitor.go to make the file self-contained.
func ptr(s string) *string { return &s }
