package policy

import (
	"fmt"
	"strings"
)

// Phase indicates when a policy is evaluated in the turn lifecycle.
type Phase string

const (
	PhasePRETURN  Phase = "pre_turn"  // Before the first LLM call of a turn
	PhasePOSTTURN Phase = "post_turn" // After the turn completes (all iterations done)
	PhasePOSTTOOL Phase = "post_tool" // After each tool call result
)

// Action is what happens when a policy condition matches.
type Action string

const (
	ActionBLOCK Action = "block" // Terminate the turn immediately
	ActionWARN  Action = "warn"  // Log + emit event, but continue
	ActionRETRY Action = "retry" // Inject guidance and re-run the LLM (post_turn only)
	ActionLOG   Action = "log"   // Silent log only (no event emitted)
)

// Policy is a single declarative rule evaluated at a specific phase.
type Policy struct {
	Name      string    `json:"name"`               // Unique identifier
	Phase     Phase     `json:"phase"`               // When to evaluate
	Condition Condition `json:"condition"`            // What triggers this policy
	Action    Action    `json:"action"`               // What to do when triggered
	Guidance  string    `json:"guidance,omitempty"`    // Injected into system prompt on retry
	Severity  string    `json:"severity,omitempty"`    // "critical" | "warning" | "info" (for events)
	Enabled   bool      `json:"enabled"`              // Toggle without removing
}

// Validate checks that a policy definition is well-formed.
func (p *Policy) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("policy name is required")
	}
	switch p.Phase {
	case PhasePRETURN, PhasePOSTTURN, PhasePOSTTOOL:
	default:
		return fmt.Errorf("invalid phase %q", p.Phase)
	}
	switch p.Action {
	case ActionBLOCK, ActionWARN, ActionRETRY, ActionLOG:
	default:
		return fmt.Errorf("invalid action %q", p.Action)
	}
	if p.Action == ActionRETRY && p.Phase == PhasePRETURN {
		return fmt.Errorf("retry action is not supported for pre_turn phase")
	}
	if p.Action == ActionRETRY && p.Guidance == "" {
		return fmt.Errorf("retry action requires guidance text")
	}
	return p.Condition.Validate()
}

// Decision is the result of evaluating a single policy.
type Decision struct {
	PolicyName string                 `json:"policy_name"`
	Matched    bool                   `json:"matched"`    // Whether the condition was true
	Action     Action                 `json:"action"`     // What action to take (only meaningful when Matched)
	Guidance   string                 `json:"guidance"`   // Guidance text for retry
	Severity   string                 `json:"severity"`
	Details    map[string]interface{} `json:"details,omitempty"` // Extra context for logging/events
}

// Condition defines what triggers a policy. Only one field should be set.
// This keeps the JSON representation flat and avoids expression language complexity.
type Condition struct {
	// Budget: trigger when session tokens exceed this threshold
	MaxSessionTokens int64 `json:"max_session_tokens,omitempty"`

	// Rate: trigger when turn count per session exceeds this
	MaxTurnsPerSession int32 `json:"max_turns_per_session,omitempty"`

	// Output: trigger when assistant output matches any of these patterns (regex)
	OutputMatchPatterns []string `json:"output_match_patterns,omitempty"`

	// Output: trigger when assistant output contains any of these keywords (case-insensitive)
	OutputContainsKeywords []string `json:"output_contains_keywords,omitempty"`

	// Tool: trigger when a specific tool has failed N+ times this turn
	ToolFailureThreshold int `json:"tool_failure_threshold,omitempty"`

	// Tool: trigger when sandbox tool calls exceed this count in a single turn
	MaxSandboxCallsPerTurn int `json:"max_sandbox_calls_per_turn,omitempty"`

	// Iterations: trigger when iteration count exceeds this (catches runaway loops early)
	MaxIterationsPerTurn int32 `json:"max_iterations_per_turn,omitempty"`

	// Finish: trigger on specific finish reasons
	FinishReasons []string `json:"finish_reasons,omitempty"`

	// Output length: trigger when assistant output exceeds this character count
	MaxOutputLength int `json:"max_output_length,omitempty"`
}

// Validate checks that at least one condition field is set.
func (c *Condition) Validate() error {
	if c.MaxSessionTokens == 0 &&
		c.MaxTurnsPerSession == 0 &&
		len(c.OutputMatchPatterns) == 0 &&
		len(c.OutputContainsKeywords) == 0 &&
		c.ToolFailureThreshold == 0 &&
		c.MaxSandboxCallsPerTurn == 0 &&
		c.MaxIterationsPerTurn == 0 &&
		len(c.FinishReasons) == 0 &&
		c.MaxOutputLength == 0 {
		return fmt.Errorf("at least one condition field must be set")
	}
	return nil
}

// EvalContext provides all the data needed for policy evaluation.
// Different phases populate different fields.
type EvalContext struct {
	// Identity
	AgentID   string
	SessionID string
	TenantID  string

	// Turn state
	TurnNumber     int32
	IterationCount int32
	UserInput      string
	AssistantText  string
	FinishReason   string

	// Token usage
	SessionTotalTokens int64 // PriorSessionTokens + CumulativeUsage.TotalTokens
	TurnTokens         int

	// Tool state (post_tool)
	ToolCalls         int
	SandboxToolCalls  int
	ToolErrors        int
	LastToolName      string
	LastToolSuccess   bool
	LastToolResult    string
	LastToolExitCode  int
}

// PolicySet is a named collection of policies, typically loaded from agent config.
type PolicySet struct {
	Policies []Policy `json:"policies"`
	// DisableBuiltins is a list of built-in policy names to disable entirely.
	// e.g. ["builtin.pii_leak", "builtin.runaway_sandbox"]
	DisableBuiltins []string `json:"disable_builtins,omitempty"`
}

// ForPhase returns only the enabled policies for the given phase.
func (ps *PolicySet) ForPhase(phase Phase) []Policy {
	var out []Policy
	for _, p := range ps.Policies {
		if p.Enabled && p.Phase == phase {
			out = append(out, p)
		}
	}
	return out
}

// ParsePolicyConfig extracts a PolicySet from agent config JSONB.
// Returns nil if no policy configuration is present at all.
//
// Supported config keys:
//
//	"policies":          []Policy   — custom policy rules
//	"disable_builtins":  []string   — built-in policy names to disable
//
// Example:
//
//	{
//	  "policies": [{"name": "my_rule", ...}],
//	  "disable_builtins": ["builtin.pii_leak"]
//	}
func ParsePolicyConfig(agentConfig map[string]interface{}) *PolicySet {
	var policies []Policy
	var disableBuiltins []string

	// Parse disable_builtins
	if raw, ok := agentConfig["disable_builtins"]; ok {
		if items, ok := raw.([]interface{}); ok {
			for _, item := range items {
				if s, ok := item.(string); ok {
					disableBuiltins = append(disableBuiltins, s)
				}
			}
		}
	}

	// Parse policies
	if raw, ok := agentConfig["policies"]; ok {
		if policiesSlice, ok := raw.([]interface{}); ok {
			for _, item := range policiesSlice {
				m, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				p := Policy{
					Name:     getString(m, "name"),
					Phase:    Phase(getString(m, "phase")),
					Action:   Action(getString(m, "action")),
					Guidance: getString(m, "guidance"),
					Severity: getString(m, "severity"),
					Enabled:  getBool(m, "enabled", true),
				}

				// Parse condition
				if condMap, ok := m["condition"].(map[string]interface{}); ok {
					p.Condition = parseCondition(condMap)
				}

				if err := p.Validate(); err == nil {
					policies = append(policies, p)
				}
			}
		}
	}

	if len(policies) == 0 && len(disableBuiltins) == 0 {
		return nil
	}
	return &PolicySet{
		Policies:        policies,
		DisableBuiltins: disableBuiltins,
	}
}

func parseCondition(m map[string]interface{}) Condition {
	c := Condition{}
	if v, ok := m["max_session_tokens"].(float64); ok {
		c.MaxSessionTokens = int64(v)
	}
	if v, ok := m["max_turns_per_session"].(float64); ok {
		c.MaxTurnsPerSession = int32(v)
	}
	if v, ok := m["tool_failure_threshold"].(float64); ok {
		c.ToolFailureThreshold = int(v)
	}
	if v, ok := m["max_sandbox_calls_per_turn"].(float64); ok {
		c.MaxSandboxCallsPerTurn = int(v)
	}
	if v, ok := m["max_iterations_per_turn"].(float64); ok {
		c.MaxIterationsPerTurn = int32(v)
	}
	if v, ok := m["max_output_length"].(float64); ok {
		c.MaxOutputLength = int(v)
	}
	if patterns, ok := m["output_match_patterns"].([]interface{}); ok {
		for _, p := range patterns {
			if s, ok := p.(string); ok {
				c.OutputMatchPatterns = append(c.OutputMatchPatterns, s)
			}
		}
	}
	if keywords, ok := m["output_contains_keywords"].([]interface{}); ok {
		for _, k := range keywords {
			if s, ok := k.(string); ok {
				c.OutputContainsKeywords = append(c.OutputContainsKeywords, s)
			}
		}
	}
	if reasons, ok := m["finish_reasons"].([]interface{}); ok {
		for _, r := range reasons {
			if s, ok := r.(string); ok {
				c.FinishReasons = append(c.FinishReasons, s)
			}
		}
	}
	return c
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]interface{}, key string, defaultVal bool) bool {
	v, ok := m[key]
	if !ok {
		return defaultVal
	}
	if b, ok := v.(bool); ok {
		return b
	}
	// JSON numbers: 0 = false, non-zero = true
	if f, ok := v.(float64); ok {
		return f != 0
	}
	if s, ok := v.(string); ok {
		return strings.EqualFold(s, "true")
	}
	return defaultVal
}
