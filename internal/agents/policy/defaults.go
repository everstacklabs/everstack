package policy

// DefaultPolicies returns a set of sensible built-in policies that are always active.
// These provide baseline guardrails even when the user hasn't configured custom policies.
// They can be overridden by agent-specific config (the evaluator merges both sets).
func DefaultPolicies() []Policy {
	return []Policy{
		{
			Name:     "builtin.pii_leak",
			Phase:    PhasePOSTTURN,
			Action:   ActionWARN,
			Severity: "critical",
			Enabled:  true,
			Condition: Condition{
				OutputMatchPatterns: []string{
					// US Social Security Number
					`\b\d{3}-\d{2}-\d{4}\b`,
					// AWS access key
					`\bAKIA[0-9A-Z]{16}\b`,
					// Generic long secret after key/token/secret
					`(?i)(?:api[_-]?key|secret|token)\s*[:=]\s*["']?[A-Za-z0-9+/=_-]{32,}`,
				},
			},
		},
		{
			Name:     "builtin.runaway_sandbox",
			Phase:    PhasePOSTTOOL,
			Action:   ActionWARN,
			Severity: "warning",
			Enabled:  true,
			Condition: Condition{
				MaxSandboxCallsPerTurn: 50,
			},
		},
		{
			Name:     "builtin.tool_failure_cascade",
			Phase:    PhasePOSTTOOL,
			Action:   ActionWARN,
			Severity: "warning",
			Enabled:  true,
			Condition: Condition{
				ToolFailureThreshold: 5,
			},
		},
		{
			Name:     "builtin.stall_detection",
			Phase:    PhasePOSTTURN,
			Action:   ActionWARN,
			Severity: "warning",
			Enabled:  true,
			Condition: Condition{
				FinishReasons: []string{
					"tool_loop_stalled",
					"tool_loop_no_results",
					"max_iterations",
				},
			},
		},
	}
}

// BuiltinPolicyNames returns the names of all built-in policies so users
// know exactly what they can override or disable.
func BuiltinPolicyNames() []string {
	defaults := DefaultPolicies()
	names := make([]string, len(defaults))
	for i, d := range defaults {
		names[i] = d.Name
	}
	return names
}

// MergeWithDefaults combines user-configured policies with built-in defaults.
//
// Override mechanisms (evaluated in order):
//  1. disable_builtins: list of built-in names to disable entirely
//     e.g. {"disable_builtins": ["builtin.pii_leak"]}
//  2. Same-name override: a user policy with the same name as a built-in
//     replaces it. Set enabled=false to disable without replacing.
//  3. All remaining built-ins are included as-is.
func MergeWithDefaults(userPolicies *PolicySet) *PolicySet {
	defaults := DefaultPolicies()

	if userPolicies == nil {
		return &PolicySet{Policies: defaults}
	}

	// Build disable set from explicit disable list
	disabled := make(map[string]bool, len(userPolicies.DisableBuiltins))
	for _, name := range userPolicies.DisableBuiltins {
		disabled[name] = true
	}

	// Index user policy names for same-name override
	userNames := make(map[string]bool, len(userPolicies.Policies))
	for _, p := range userPolicies.Policies {
		userNames[p.Name] = true
	}

	// Start with defaults that aren't disabled or overridden
	var merged []Policy
	for _, d := range defaults {
		if disabled[d.Name] || userNames[d.Name] {
			continue
		}
		merged = append(merged, d)
	}

	// Append all user policies (including same-name overrides)
	merged = append(merged, userPolicies.Policies...)

	return &PolicySet{Policies: merged}
}
