package channels

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// DispatchMode indicates how the agent was resolved.
type DispatchMode string

const (
	DispatchModeDefault  DispatchMode = "default"   // Channel has a default agent
	DispatchModeMention  DispatchMode = "mention"    // User explicitly named an agent
	DispatchModeAutoHITL DispatchMode = "auto_hitl"  // LLM picked agents, awaiting confirmation
)

// DispatchResult is the outcome of dispatcher resolution.
type DispatchResult struct {
	AgentID   string
	AgentName string
	Mode      DispatchMode
	Pending   bool // true = HITL buttons sent, no session yet
}

// AgentSuggestion is a candidate from auto-route ranking.
type AgentSuggestion struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Reason    string `json:"reason"`
}

// PendingDispatch stores a message awaiting HITL confirmation.
type PendingDispatch struct {
	ID          string
	Message     InboundMessage
	Suggestions []AgentSuggestion
	CreatedAt   time.Time
}

// DispatchSendFunc allows the dispatcher to send messages (HITL buttons)
// without holding a Connector reference.
type DispatchSendFunc func(ctx context.Context, channelRef, threadRef string, msg OutboundMessage) (string, error)

// Dispatcher resolves inbound messages to agents when the channel
// has no default agent (or the user explicitly mentions an agent).
type Dispatcher interface {
	// Dispatch resolves the target agent for an inbound message.
	// Returns nil if the channel has a default agent and no override was detected.
	Dispatch(ctx context.Context, msg InboundMessage, agents []agentrt.AgentCatalogEntry, sendFn DispatchSendFunc) (*DispatchResult, error)

	// HandleDispatchInteraction handles a HITL button click for agent selection.
	// Returns the selected agentID and the original PendingDispatch.
	HandleDispatchInteraction(ctx context.Context, dispatchID, agentID string) (string, *PendingDispatch, error)

	// CleanupExpired removes pending dispatches older than the TTL.
	CleanupExpired()
}

// AgentDispatcher is the concrete implementation of Dispatcher.
type AgentDispatcher struct {
	db            *sqlx.DB
	agentLister   AgentLister
	pendingMu     sync.RWMutex
	pending       map[string]*PendingDispatch
	pendingTTL    time.Duration
	routerModel   string // model to use for auto-route LLM calls
}

// NewAgentDispatcher creates a new dispatcher.
func NewAgentDispatcher(agentLister AgentLister, db *sqlx.DB) *AgentDispatcher {
	return &AgentDispatcher{
		db:          db,
		agentLister: agentLister,
		pending:     make(map[string]*PendingDispatch),
		pendingTTL:  5 * time.Minute,
		routerModel: "gpt-4o-mini",
	}
}

// spawnPattern matches "spawn <agent-name> <task>" or "@bot spawn <agent-name> <task>"
var spawnPattern = regexp.MustCompile(`(?i)^(?:spawn\s+)(\S+)\s*(.*)$`)

// directMentionPattern matches "<agent-name> <task>" where agent-name is matched against catalog
// This is handled programmatically (not regex) since we match against dynamic agent names.

// Dispatch resolves the target agent. If the message text matches an agent by name
// or mention_alias, returns that agent. Otherwise sends HITL buttons with suggestions.
func (d *AgentDispatcher) Dispatch(ctx context.Context, msg InboundMessage, agents []agentrt.AgentCatalogEntry, sendFn DispatchSendFunc) (*DispatchResult, error) {
	if len(agents) == 0 {
		return nil, fmt.Errorf("no agents available for tenant %s", msg.TenantID)
	}

	// Build lookup maps (lowercase name → agent, lowercase alias → agent)
	nameMap, aliasMap := d.buildAgentMaps(ctx, agents, msg.TenantID)

	// 1. Try "spawn <agent-name> <task>" pattern
	if m := spawnPattern.FindStringSubmatch(msg.Text); len(m) == 3 {
		agentKey := strings.ToLower(m[1])
		if entry, ok := nameMap[agentKey]; ok {
			return &DispatchResult{AgentID: entry.ID, AgentName: entry.Name, Mode: DispatchModeMention}, nil
		}
		if entry, ok := aliasMap[agentKey]; ok {
			return &DispatchResult{AgentID: entry.ID, AgentName: entry.Name, Mode: DispatchModeMention}, nil
		}
	}

	// 2. Try direct agent name match at start of message
	if result := d.matchDirectMention(msg.Text, nameMap, aliasMap); result != nil {
		return result, nil
	}

	// 3. Single agent available — skip HITL, use it directly
	if len(agents) == 1 {
		return &DispatchResult{
			AgentID:   agents[0].ID,
			AgentName: agents[0].Name,
			Mode:      DispatchModeDefault,
		}, nil
	}

	// 4. Auto-route with HITL — rank agents and send buttons
	suggestions := d.rankAgents(msg.Text, agents)
	dispatchID := uuid.New().String()

	pd := &PendingDispatch{
		ID:          dispatchID,
		Message:     msg,
		Suggestions: suggestions,
		CreatedAt:   time.Now(),
	}

	d.pendingMu.Lock()
	d.pending[dispatchID] = pd
	d.pendingMu.Unlock()

	// Send HITL buttons to the platform
	buttons := make([]ActionButton, 0, len(suggestions))
	for _, s := range suggestions {
		buttons = append(buttons, ActionButton{
			ID:    fmt.Sprintf("dispatch:%s:%s", dispatchID, s.AgentID),
			Label: fmt.Sprintf("Use %s", s.AgentName),
			Style: "primary",
		})
	}

	text := "Which agent should handle this?\n"
	for _, s := range suggestions {
		text += fmt.Sprintf("**%s** — %s\n", s.AgentName, s.Reason)
	}

	if _, err := sendFn(ctx, msg.PlatformChannelRef, msg.PlatformThreadRef, OutboundMessage{
		Text:    text,
		Actions: buttons,
	}); err != nil {
		// Clean up pending on send failure
		d.pendingMu.Lock()
		delete(d.pending, dispatchID)
		d.pendingMu.Unlock()
		return nil, fmt.Errorf("send dispatch buttons: %w", err)
	}

	logger.WithFields(
		"dispatch_id", dispatchID,
		"suggestions", len(suggestions),
		"channel", msg.ChannelConfigID,
	).Info("channels: dispatch HITL buttons sent")

	return &DispatchResult{Pending: true, Mode: DispatchModeAutoHITL}, nil
}

// HandleDispatchInteraction resolves a HITL button click.
func (d *AgentDispatcher) HandleDispatchInteraction(_ context.Context, dispatchID, agentID string) (string, *PendingDispatch, error) {
	d.pendingMu.Lock()
	pd, ok := d.pending[dispatchID]
	if ok {
		delete(d.pending, dispatchID)
	}
	d.pendingMu.Unlock()

	if !ok {
		return "", nil, fmt.Errorf("dispatch %s not found or expired", dispatchID)
	}

	// Verify the agentID is one of the suggestions
	valid := false
	for _, s := range pd.Suggestions {
		if s.AgentID == agentID {
			valid = true
			break
		}
	}
	if !valid {
		return "", nil, fmt.Errorf("agent %s is not a valid suggestion for dispatch %s", agentID, dispatchID)
	}

	return agentID, pd, nil
}

// CleanupExpired removes pending dispatches that have exceeded the TTL.
func (d *AgentDispatcher) CleanupExpired() {
	cutoff := time.Now().Add(-d.pendingTTL)

	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()

	expired := 0
	for id, pd := range d.pending {
		if pd.CreatedAt.Before(cutoff) {
			delete(d.pending, id)
			expired++
		}
	}
	if expired > 0 {
		logger.WithFields("expired", expired).Debug("channels: cleaned up expired dispatches")
	}
}

// buildAgentMaps creates lowercase name→agent and alias→agent lookup maps.
func (d *AgentDispatcher) buildAgentMaps(ctx context.Context, agents []agentrt.AgentCatalogEntry, tenantID string) (nameMap, aliasMap map[string]agentrt.AgentCatalogEntry) {
	nameMap = make(map[string]agentrt.AgentCatalogEntry, len(agents))
	aliasMap = make(map[string]agentrt.AgentCatalogEntry, len(agents))

	for _, a := range agents {
		nameMap[strings.ToLower(a.Name)] = a
		// Also add kebab-case and slug variants
		slug := strings.ToLower(strings.ReplaceAll(a.Name, " ", "-"))
		if slug != strings.ToLower(a.Name) {
			nameMap[slug] = a
		}
	}

	// Load mention aliases from DB for richer matching
	if d.db != nil {
		for _, a := range agents {
			alias := agentRowMentionAlias(ctx, d.db, tenantID, a.ID)
			if alias != "" {
				aliasMap[strings.ToLower(alias)] = a
			}
		}
	}

	return nameMap, aliasMap
}

// matchDirectMention checks if the message starts with an agent name or alias.
func (d *AgentDispatcher) matchDirectMention(text string, nameMap, aliasMap map[string]agentrt.AgentCatalogEntry) *DispatchResult {
	lower := strings.ToLower(strings.TrimSpace(text))

	// Check all names/aliases — longest match first to avoid partial matches
	type candidate struct {
		key   string
		entry agentrt.AgentCatalogEntry
	}
	var candidates []candidate
	for k, v := range nameMap {
		candidates = append(candidates, candidate{k, v})
	}
	for k, v := range aliasMap {
		candidates = append(candidates, candidate{k, v})
	}

	var bestMatch *candidate
	for i := range candidates {
		c := &candidates[i]
		if strings.HasPrefix(lower, c.key) {
			// Must be followed by space, comma, or end-of-string
			rest := lower[len(c.key):]
			if rest == "" || rest[0] == ' ' || rest[0] == ',' || rest[0] == ':' {
				if bestMatch == nil || len(c.key) > len(bestMatch.key) {
					bestMatch = c
				}
			}
		}
	}

	if bestMatch != nil {
		return &DispatchResult{
			AgentID:   bestMatch.entry.ID,
			AgentName: bestMatch.entry.Name,
			Mode:      DispatchModeMention,
		}
	}
	return nil
}

// rankAgents returns top suggestions based on simple keyword matching.
// A future enhancement would use an LLM call (reusing the gateway router)
// for semantic ranking — for now we use description keyword overlap.
func (d *AgentDispatcher) rankAgents(text string, agents []agentrt.AgentCatalogEntry) []AgentSuggestion {
	type scored struct {
		agent agentrt.AgentCatalogEntry
		score int
	}

	words := strings.Fields(strings.ToLower(text))
	var results []scored

	for _, a := range agents {
		desc := strings.ToLower(a.Description + " " + a.Name)
		score := 0
		for _, w := range words {
			if len(w) > 2 && strings.Contains(desc, w) {
				score++
			}
		}
		// Boost agents with tools matching keywords
		for _, t := range a.Tools {
			tl := strings.ToLower(t)
			for _, w := range words {
				if len(w) > 2 && strings.Contains(tl, w) {
					score++
				}
			}
		}
		results = append(results, scored{agent: a, score: score})
	}

	// Sort by score descending (simple insertion sort for small slices)
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	// Take top 3
	limit := 3
	if len(results) < limit {
		limit = len(results)
	}

	suggestions := make([]AgentSuggestion, 0, limit)
	for i := 0; i < limit; i++ {
		reason := results[i].agent.Description
		if reason == "" {
			reason = fmt.Sprintf("Agent with tools: %s", strings.Join(results[i].agent.Tools, ", "))
		}
		suggestions = append(suggestions, AgentSuggestion{
			AgentID:   results[i].agent.ID,
			AgentName: results[i].agent.Name,
			Reason:    reason,
		})
	}

	return suggestions
}
