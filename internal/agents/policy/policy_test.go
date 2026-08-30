package policy

import (
	"testing"
)

func TestPolicyValidation(t *testing.T) {
	tests := []struct {
		name    string
		policy  Policy
		wantErr bool
	}{
		{
			name: "valid pre_turn block",
			policy: Policy{
				Name: "budget_guard", Phase: PhasePRETURN, Action: ActionBLOCK, Enabled: true,
				Condition: Condition{MaxSessionTokens: 100000},
			},
		},
		{
			name: "valid post_turn retry with guidance",
			policy: Policy{
				Name: "hallucination_gate", Phase: PhasePOSTTURN, Action: ActionRETRY, Enabled: true,
				Guidance:  "Only reference information from tool results.",
				Condition: Condition{OutputMatchPatterns: []string{`\bfact\b`}},
			},
		},
		{
			name: "retry on pre_turn is invalid",
			policy: Policy{
				Name: "bad", Phase: PhasePRETURN, Action: ActionRETRY, Enabled: true,
				Guidance:  "guidance",
				Condition: Condition{MaxSessionTokens: 100},
			},
			wantErr: true,
		},
		{
			name: "retry without guidance is invalid",
			policy: Policy{
				Name: "bad", Phase: PhasePOSTTURN, Action: ActionRETRY, Enabled: true,
				Condition: Condition{MaxSessionTokens: 100},
			},
			wantErr: true,
		},
		{
			name: "empty condition is invalid",
			policy: Policy{
				Name: "bad", Phase: PhasePOSTTURN, Action: ActionWARN, Enabled: true,
				Condition: Condition{},
			},
			wantErr: true,
		},
		{
			name:    "empty name is invalid",
			policy:  Policy{Phase: PhasePOSTTURN, Action: ActionWARN, Condition: Condition{MaxSessionTokens: 1}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- Evaluator tests ---

func TestEvaluator_BudgetGuard(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "budget", Phase: PhasePRETURN, Action: ActionBLOCK, Enabled: true,
			Condition: Condition{MaxSessionTokens: 50000},
		},
	}}
	eval := NewEvaluator(ps)

	// Under budget
	decisions := eval.EvaluatePhase(PhasePRETURN, &EvalContext{
		SessionTotalTokens: 30000,
	})
	if len(decisions) != 0 {
		t.Errorf("expected no decisions under budget, got %d", len(decisions))
	}

	// Over budget
	decisions = eval.EvaluatePhase(PhasePRETURN, &EvalContext{
		SessionTotalTokens: 60000,
	})
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision over budget, got %d", len(decisions))
	}
	if decisions[0].Action != ActionBLOCK {
		t.Errorf("expected BLOCK, got %s", decisions[0].Action)
	}
	if !decisions[0].Matched {
		t.Errorf("expected matched=true")
	}
}

func TestEvaluator_TurnRateGuard(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "rate", Phase: PhasePRETURN, Action: ActionBLOCK, Enabled: true,
			Condition: Condition{MaxTurnsPerSession: 10},
		},
	}}
	eval := NewEvaluator(ps)

	decisions := eval.EvaluatePhase(PhasePRETURN, &EvalContext{TurnNumber: 11})
	if !HasBlockingDecision(decisions) {
		t.Errorf("expected blocking decision for turn 11")
	}

	decisions = eval.EvaluatePhase(PhasePRETURN, &EvalContext{TurnNumber: 5})
	if HasBlockingDecision(decisions) {
		t.Errorf("did not expect blocking for turn 5")
	}
}

func TestEvaluator_OutputPatternMatch(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "pii", Phase: PhasePOSTTURN, Action: ActionWARN, Enabled: true,
			Severity: "critical",
			Condition: Condition{
				OutputMatchPatterns: []string{`\b\d{3}-\d{2}-\d{4}\b`},
			},
		},
	}}
	eval := NewEvaluator(ps)

	// No SSN
	decisions := eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{
		AssistantText: "The user was created successfully.",
	})
	if len(decisions) != 0 {
		t.Errorf("expected no matches for clean output")
	}

	// Contains SSN
	decisions = eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{
		AssistantText: "Found SSN: 123-45-6789 in the records.",
	})
	if len(decisions) != 1 {
		t.Fatalf("expected 1 match, got %d", len(decisions))
	}
	if decisions[0].Severity != "critical" {
		t.Errorf("expected severity=critical")
	}
}

func TestEvaluator_KeywordMatch(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "banned_words", Phase: PhasePOSTTURN, Action: ActionBLOCK, Enabled: true,
			Condition: Condition{
				OutputContainsKeywords: []string{"CONFIDENTIAL", "top secret"},
			},
		},
	}}
	eval := NewEvaluator(ps)

	// Case insensitive match
	decisions := eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{
		AssistantText: "This document is marked confidential.",
	})
	if !HasBlockingDecision(decisions) {
		t.Errorf("expected blocking for 'confidential' keyword")
	}

	// No match
	decisions = eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{
		AssistantText: "Here is the public report.",
	})
	if len(decisions) != 0 {
		t.Errorf("expected no match for clean text")
	}
}

func TestEvaluator_ToolFailureThreshold(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "tool_failures", Phase: PhasePOSTTOOL, Action: ActionWARN, Enabled: true,
			Condition: Condition{ToolFailureThreshold: 3},
		},
	}}
	eval := NewEvaluator(ps)

	decisions := eval.EvaluatePhase(PhasePOSTTOOL, &EvalContext{ToolErrors: 2})
	if len(decisions) != 0 {
		t.Errorf("expected no match for 2 errors")
	}

	decisions = eval.EvaluatePhase(PhasePOSTTOOL, &EvalContext{ToolErrors: 5})
	if len(decisions) != 1 {
		t.Errorf("expected match for 5 errors")
	}
}

func TestEvaluator_SandboxCallLimit(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "sandbox_limit", Phase: PhasePOSTTOOL, Action: ActionWARN, Enabled: true,
			Condition: Condition{MaxSandboxCallsPerTurn: 20},
		},
	}}
	eval := NewEvaluator(ps)

	decisions := eval.EvaluatePhase(PhasePOSTTOOL, &EvalContext{SandboxToolCalls: 25})
	if len(decisions) != 1 {
		t.Errorf("expected match for 25 sandbox calls")
	}
}

func TestEvaluator_FinishReason(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "stall", Phase: PhasePOSTTURN, Action: ActionWARN, Enabled: true,
			Condition: Condition{FinishReasons: []string{"tool_loop_stalled", "max_iterations"}},
		},
	}}
	eval := NewEvaluator(ps)

	decisions := eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{FinishReason: "stop"})
	if len(decisions) != 0 {
		t.Errorf("expected no match for stop")
	}

	decisions = eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{FinishReason: "tool_loop_stalled"})
	if len(decisions) != 1 {
		t.Errorf("expected match for tool_loop_stalled")
	}
}

func TestEvaluator_OutputLength(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "output_limit", Phase: PhasePOSTTURN, Action: ActionWARN, Enabled: true,
			Condition: Condition{MaxOutputLength: 100},
		},
	}}
	eval := NewEvaluator(ps)

	short := "short"
	decisions := eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{AssistantText: short})
	if len(decisions) != 0 {
		t.Errorf("expected no match for short output")
	}

	long := string(make([]byte, 200))
	decisions = eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{AssistantText: long})
	if len(decisions) != 1 {
		t.Errorf("expected match for long output")
	}
}

func TestEvaluator_RetryDecision(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "retry_on_keyword", Phase: PhasePOSTTURN, Action: ActionRETRY, Enabled: true,
			Guidance: "Please revise your response.",
			Condition: Condition{
				OutputContainsKeywords: []string{"I don't know"},
			},
		},
	}}
	eval := NewEvaluator(ps)

	decisions := eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{
		AssistantText: "I don't know the answer to that.",
	})
	retry := FirstRetryDecision(decisions)
	if retry == nil {
		t.Fatal("expected retry decision")
	}
	if retry.Guidance != "Please revise your response." {
		t.Errorf("expected guidance text")
	}
}

func TestEvaluator_DisabledPolicy(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "disabled", Phase: PhasePRETURN, Action: ActionBLOCK, Enabled: false,
			Condition: Condition{MaxSessionTokens: 1}, // Would always trigger
		},
	}}
	eval := NewEvaluator(ps)

	decisions := eval.EvaluatePhase(PhasePRETURN, &EvalContext{SessionTotalTokens: 100000})
	if len(decisions) != 0 {
		t.Errorf("disabled policy should not match")
	}
}

func TestEvaluator_PhaseFiltering(t *testing.T) {
	ps := &PolicySet{Policies: []Policy{
		{
			Name: "pre", Phase: PhasePRETURN, Action: ActionBLOCK, Enabled: true,
			Condition: Condition{MaxSessionTokens: 1},
		},
		{
			Name: "post", Phase: PhasePOSTTURN, Action: ActionWARN, Enabled: true,
			Condition: Condition{MaxOutputLength: 1},
		},
	}}
	eval := NewEvaluator(ps)

	// Pre-turn should only see the budget policy
	decisions := eval.EvaluatePhase(PhasePRETURN, &EvalContext{
		SessionTotalTokens: 100,
		AssistantText:      "long text",
	})
	if len(decisions) != 1 || decisions[0].PolicyName != "pre" {
		t.Errorf("expected only pre_turn policy, got %v", decisions)
	}
}

func TestEvaluator_NilSafe(t *testing.T) {
	var eval *Evaluator
	decisions := eval.EvaluatePhase(PhasePRETURN, &EvalContext{})
	if decisions != nil {
		t.Errorf("nil evaluator should return nil")
	}
}

// --- Defaults ---

func TestMergeWithDefaults(t *testing.T) {
	// No user policies: should get defaults
	merged := MergeWithDefaults(nil)
	if len(merged.Policies) != len(DefaultPolicies()) {
		t.Errorf("expected %d defaults, got %d", len(DefaultPolicies()), len(merged.Policies))
	}

	// User overrides a built-in
	user := &PolicySet{Policies: []Policy{
		{
			Name: "builtin.pii_leak", Phase: PhasePOSTTURN, Action: ActionBLOCK, Enabled: true,
			Condition: Condition{OutputMatchPatterns: []string{`custom_pattern`}},
		},
	}}
	merged = MergeWithDefaults(user)
	// Should have defaults minus overridden + user policy
	expectedCount := len(DefaultPolicies()) // -1 for override +1 for user = same count
	if len(merged.Policies) != expectedCount {
		t.Errorf("expected %d policies, got %d", expectedCount, len(merged.Policies))
	}

	// The user's version should be present (Action=BLOCK, not WARN)
	found := false
	for _, p := range merged.Policies {
		if p.Name == "builtin.pii_leak" {
			found = true
			if p.Action != ActionBLOCK {
				t.Errorf("expected user override with BLOCK, got %s", p.Action)
			}
		}
	}
	if !found {
		t.Errorf("user override policy not found in merged set")
	}
}

// --- ParsePolicyConfig ---

func TestParsePolicyConfig(t *testing.T) {
	config := map[string]interface{}{
		"policies": []interface{}{
			map[string]interface{}{
				"name":    "my_budget",
				"phase":   "pre_turn",
				"action":  "block",
				"enabled": true,
				"condition": map[string]interface{}{
					"max_session_tokens": float64(100000),
				},
			},
			map[string]interface{}{
				"name":     "my_retry",
				"phase":    "post_turn",
				"action":   "retry",
				"enabled":  true,
				"guidance": "Try harder",
				"condition": map[string]interface{}{
					"output_contains_keywords": []interface{}{"I cannot"},
				},
			},
		},
	}

	ps := ParsePolicyConfig(config)
	if ps == nil {
		t.Fatal("expected non-nil PolicySet")
	}
	if len(ps.Policies) != 2 {
		t.Errorf("expected 2 policies, got %d", len(ps.Policies))
	}
}

func TestParsePolicyConfig_Empty(t *testing.T) {
	ps := ParsePolicyConfig(map[string]interface{}{})
	if ps != nil {
		t.Errorf("expected nil for empty config")
	}
}

// --- DisableBuiltins ---

func TestMergeWithDefaults_DisableBuiltins(t *testing.T) {
	user := &PolicySet{
		DisableBuiltins: []string{"builtin.pii_leak", "builtin.runaway_sandbox"},
	}
	merged := MergeWithDefaults(user)

	for _, p := range merged.Policies {
		if p.Name == "builtin.pii_leak" || p.Name == "builtin.runaway_sandbox" {
			t.Errorf("disabled built-in %q should not be in merged set", p.Name)
		}
	}

	expectedCount := len(DefaultPolicies()) - 2
	if len(merged.Policies) != expectedCount {
		t.Errorf("expected %d policies, got %d", expectedCount, len(merged.Policies))
	}
}

func TestMergeWithDefaults_DisableAndOverride(t *testing.T) {
	// Disable one, override another
	user := &PolicySet{
		DisableBuiltins: []string{"builtin.runaway_sandbox"},
		Policies: []Policy{
			{
				Name: "builtin.pii_leak", Phase: PhasePOSTTURN, Action: ActionBLOCK, Enabled: true,
				Condition: Condition{OutputMatchPatterns: []string{`custom`}},
			},
		},
	}
	merged := MergeWithDefaults(user)

	// runaway_sandbox: disabled
	// pii_leak: overridden to BLOCK
	// tool_failure_cascade + stall_detection: kept as defaults
	expectedCount := len(DefaultPolicies()) - 1 // -2 (disabled + overridden) +1 (user override)
	if len(merged.Policies) != expectedCount {
		t.Errorf("expected %d policies, got %d", expectedCount, len(merged.Policies))
	}

	for _, p := range merged.Policies {
		if p.Name == "builtin.runaway_sandbox" {
			t.Errorf("disabled built-in should not be present")
		}
		if p.Name == "builtin.pii_leak" && p.Action != ActionBLOCK {
			t.Errorf("overridden pii_leak should have BLOCK action, got %s", p.Action)
		}
	}
}

func TestMergeWithDefaults_DisableViaEnabledFalse(t *testing.T) {
	// Override a built-in with enabled=false to disable it
	user := &PolicySet{
		Policies: []Policy{
			{
				Name: "builtin.pii_leak", Phase: PhasePOSTTURN, Action: ActionWARN, Enabled: false,
				Condition: Condition{OutputMatchPatterns: []string{`anything`}},
			},
		},
	}
	merged := MergeWithDefaults(user)
	eval := NewEvaluator(merged)

	// pii_leak is now disabled, so SSN should not trigger
	decisions := eval.EvaluatePhase(PhasePOSTTURN, &EvalContext{
		AssistantText: "SSN: 123-45-6789",
	})
	for _, d := range decisions {
		if d.PolicyName == "builtin.pii_leak" {
			t.Errorf("disabled pii_leak should not fire")
		}
	}
}

func TestParsePolicyConfig_DisableBuiltinsOnly(t *testing.T) {
	config := map[string]interface{}{
		"disable_builtins": []interface{}{"builtin.pii_leak"},
	}
	ps := ParsePolicyConfig(config)
	if ps == nil {
		t.Fatal("expected non-nil PolicySet with disable_builtins")
	}
	if len(ps.DisableBuiltins) != 1 {
		t.Errorf("expected 1 disable, got %d", len(ps.DisableBuiltins))
	}
	if ps.DisableBuiltins[0] != "builtin.pii_leak" {
		t.Errorf("expected builtin.pii_leak, got %s", ps.DisableBuiltins[0])
	}
}

func TestBuiltinPolicyNames(t *testing.T) {
	names := BuiltinPolicyNames()
	if len(names) != len(DefaultPolicies()) {
		t.Errorf("expected %d names, got %d", len(DefaultPolicies()), len(names))
	}
	for _, n := range names {
		if n == "" {
			t.Errorf("empty built-in policy name")
		}
	}
}
