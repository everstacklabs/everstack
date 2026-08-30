package compact

import (
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Action describes what the compactor decided to do for a given turn.
type Action int

const (
	// ActionNone means the request is below all thresholds; nothing to do.
	ActionNone Action = iota
	// ActionSummarize means the caller should run a summariser over
	// messages[ReplaceStart:ReplaceEnd] and splice the summary back in.
	ActionSummarize
	// ActionTruncate means the caller should hard-replace the range
	// (no LLM call) — used by the emergency tier when we're already
	// over the context window and have no latency budget to summarise.
	ActionTruncate
)

// Decision is what Decide returns. ReplaceStart..ReplaceEnd is the
// half-open range of messages the caller should remove and replace
// with a single summary/sentinel message. Tier is informational
// (logging, metrics, span attributes).
type Decision struct {
	Action       Action
	Tier         Tier
	ReplaceStart int
	ReplaceEnd   int // exclusive
	FreedTokens  int // estimated tokens reclaimed
}

// Compactor makes the "should we compact, and if so how much" decision
// without doing any I/O. The actual summarisation LLM call is the
// caller's job (see Summarizer in this package, or the agent runtime's
// Monitor for the channel-based variant).
type Compactor struct {
	cfg Config
}

// New constructs a Compactor with the given config. Config is taken by
// value so callers can swap config without touching live state.
func New(cfg Config) *Compactor {
	return &Compactor{cfg: cfg}
}

// Decide inspects the message slice and the actual prompt token count
// reported by the provider (or estimated via Estimate when not
// available yet) and returns the appropriate action.
//
// The function is pure — same inputs, same outputs — so it can be
// unit-tested without any provider plumbing.
func (c *Compactor) Decide(messages []gw.Message, promptTokens int) Decision {
	if !c.cfg.Enabled || c.cfg.MaxContextTokens <= 0 {
		return Decision{Action: ActionNone}
	}

	ratio := float64(promptTokens) / float64(c.cfg.MaxContextTokens)

	switch {
	case ratio >= c.cfg.EmergencyThreshold:
		return c.decideEmergency(messages)
	case ratio >= c.cfg.AggressiveThreshold:
		return c.decideSummarize(messages, 0.60, TierAggressive)
	case ratio >= c.cfg.BackgroundThreshold:
		return c.decideSummarize(messages, 0.30, TierBackground)
	}
	return Decision{Action: ActionNone}
}

// decideEmergency keeps the system prompt (index 0 if present) and
// the last 20 messages. No LLM call. Returns ActionNone if the
// transcript is already short enough that truncation would be a no-op.
func (c *Compactor) decideEmergency(messages []gw.Message) Decision {
	if len(messages) <= 21 {
		return Decision{Action: ActionNone}
	}
	keepStart := len(messages) - 20
	if keepStart <= 1 {
		return Decision{Action: ActionNone}
	}
	return Decision{
		Action:       ActionTruncate,
		Tier:         TierEmergency,
		ReplaceStart: 1,
		ReplaceEnd:   keepStart,
		FreedTokens:  Estimate(messages[1:keepStart]),
	}
}

// decideSummarize chooses how many of the oldest non-system messages
// to summarise based on the fraction (0.30 for background, 0.60 for
// aggressive). Skips when there are too few messages for summarisation
// to be worthwhile (< 2 messages).
func (c *Compactor) decideSummarize(messages []gw.Message, fraction float64, tier Tier) Decision {
	startIdx := 0
	if len(messages) > 0 && messages[0].Role == gw.RoleSystem {
		startIdx = 1
	}

	nonSystemCount := len(messages) - startIdx
	summarizeCount := int(float64(nonSystemCount) * fraction)
	if summarizeCount < 2 {
		return Decision{Action: ActionNone}
	}
	endIdx := startIdx + summarizeCount

	return Decision{
		Action:       ActionSummarize,
		Tier:         tier,
		ReplaceStart: startIdx,
		ReplaceEnd:   endIdx,
		FreedTokens:  Estimate(messages[startIdx:endIdx]),
	}
}

// Estimate provides a fast heuristic token count for a set of
// messages. Uses ~4 characters per token. For precise counts use the
// provider's reported usage.
//
// This is the same algorithm previously living in
// internal/agents/runtime/token_counter.go EstimateTokens; consolidated
// here so the gateway middleware doesn't depend on the agent runtime.
func Estimate(messages []gw.Message) int {
	totalChars := 0
	for _, msg := range messages {
		// Role overhead (~4 tokens worth of role/format markers).
		totalChars += 16

		for _, part := range msg.Content {
			if part.Text != nil {
				totalChars += len(*part.Text)
			}
		}

		// Tool calls add their name + arguments + framing tokens.
		for _, tc := range msg.ToolCalls {
			totalChars += len(tc.Function.Name) + len(tc.Function.Arguments) + 20
		}
	}

	return totalChars / 4
}

// EstimateText estimates tokens for a plain text string.
func EstimateText(text string) int {
	return len(text) / 4
}
