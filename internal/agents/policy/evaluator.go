package policy

import (
	"regexp"
	"strings"
)

// Evaluator checks policies against an EvalContext and returns decisions.
type Evaluator struct {
	policies []Policy
	// Compiled regex cache (keyed by pattern string)
	regexCache map[string]*regexp.Regexp
}

// NewEvaluator creates an evaluator for the given policy set.
// Invalid regex patterns in conditions are silently skipped.
func NewEvaluator(ps *PolicySet) *Evaluator {
	if ps == nil {
		return nil
	}
	e := &Evaluator{
		policies:   ps.Policies,
		regexCache: make(map[string]*regexp.Regexp),
	}
	// Pre-compile all regex patterns
	for _, p := range ps.Policies {
		for _, pattern := range p.Condition.OutputMatchPatterns {
			if _, exists := e.regexCache[pattern]; !exists {
				if re, err := regexp.Compile(pattern); err == nil {
					e.regexCache[pattern] = re
				}
			}
		}
	}
	return e
}

// EvaluatePhase runs all policies for the given phase and returns decisions.
// Only matched policies are returned.
func (e *Evaluator) EvaluatePhase(phase Phase, ec *EvalContext) []Decision {
	if e == nil {
		return nil
	}

	var decisions []Decision
	for _, p := range e.policies {
		if !p.Enabled || p.Phase != phase {
			continue
		}
		if d, matched := e.evaluate(p, ec); matched {
			decisions = append(decisions, d)
		}
	}
	return decisions
}

// HasBlockingDecision returns true if any decision has Action=block.
func HasBlockingDecision(decisions []Decision) bool {
	for _, d := range decisions {
		if d.Matched && d.Action == ActionBLOCK {
			return true
		}
	}
	return false
}

// FirstRetryDecision returns the first retry decision, or nil if none.
func FirstRetryDecision(decisions []Decision) *Decision {
	for _, d := range decisions {
		if d.Matched && d.Action == ActionRETRY {
			return &d
		}
	}
	return nil
}

// evaluate checks a single policy against the context.
func (e *Evaluator) evaluate(p Policy, ec *EvalContext) (Decision, bool) {
	d := Decision{
		PolicyName: p.Name,
		Action:     p.Action,
		Guidance:   p.Guidance,
		Severity:   p.Severity,
		Details:    make(map[string]interface{}),
	}

	c := p.Condition

	// Budget guard
	if c.MaxSessionTokens > 0 && ec.SessionTotalTokens > c.MaxSessionTokens {
		d.Matched = true
		d.Details["session_tokens"] = ec.SessionTotalTokens
		d.Details["max_session_tokens"] = c.MaxSessionTokens
		return d, true
	}

	// Turn rate guard
	if c.MaxTurnsPerSession > 0 && ec.TurnNumber > c.MaxTurnsPerSession {
		d.Matched = true
		d.Details["turn_number"] = ec.TurnNumber
		d.Details["max_turns"] = c.MaxTurnsPerSession
		return d, true
	}

	// Output pattern match (post_turn)
	if len(c.OutputMatchPatterns) > 0 && ec.AssistantText != "" {
		for _, pattern := range c.OutputMatchPatterns {
			if re, ok := e.regexCache[pattern]; ok && re.MatchString(ec.AssistantText) {
				d.Matched = true
				d.Details["matched_pattern"] = pattern
				return d, true
			}
		}
	}

	// Output keyword match (post_turn)
	if len(c.OutputContainsKeywords) > 0 && ec.AssistantText != "" {
		lower := strings.ToLower(ec.AssistantText)
		for _, kw := range c.OutputContainsKeywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				d.Matched = true
				d.Details["matched_keyword"] = kw
				return d, true
			}
		}
	}

	// Tool failure threshold (post_tool)
	if c.ToolFailureThreshold > 0 && ec.ToolErrors >= c.ToolFailureThreshold {
		d.Matched = true
		d.Details["tool_errors"] = ec.ToolErrors
		d.Details["threshold"] = c.ToolFailureThreshold
		return d, true
	}

	// Sandbox call limit (post_tool)
	if c.MaxSandboxCallsPerTurn > 0 && ec.SandboxToolCalls > c.MaxSandboxCallsPerTurn {
		d.Matched = true
		d.Details["sandbox_calls"] = ec.SandboxToolCalls
		d.Details["max_sandbox_calls"] = c.MaxSandboxCallsPerTurn
		return d, true
	}

	// Iteration limit (evaluated during loop)
	if c.MaxIterationsPerTurn > 0 && ec.IterationCount > c.MaxIterationsPerTurn {
		d.Matched = true
		d.Details["iteration_count"] = ec.IterationCount
		d.Details["max_iterations"] = c.MaxIterationsPerTurn
		return d, true
	}

	// Finish reason match (post_turn)
	if len(c.FinishReasons) > 0 && ec.FinishReason != "" {
		for _, fr := range c.FinishReasons {
			if strings.EqualFold(ec.FinishReason, fr) {
				d.Matched = true
				d.Details["finish_reason"] = ec.FinishReason
				return d, true
			}
		}
	}

	// Output length (post_turn)
	if c.MaxOutputLength > 0 && len(ec.AssistantText) > c.MaxOutputLength {
		d.Matched = true
		d.Details["output_length"] = len(ec.AssistantText)
		d.Details["max_output_length"] = c.MaxOutputLength
		return d, true
	}

	return d, false
}
