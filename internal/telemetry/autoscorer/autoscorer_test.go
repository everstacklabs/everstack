package autoscorer

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// mockRecorder captures scores in memory for assertions.
type mockRecorder struct {
	batches [][]*scores.Score
}

func (m *mockRecorder) RecordBatch(_ context.Context, s []*scores.Score) error {
	m.batches = append(m.batches, s)
	return nil
}

func (m *mockRecorder) allScores() []*scores.Score {
	var all []*scores.Score
	for _, b := range m.batches {
		all = append(all, b...)
	}
	return all
}

func findScore(ss []*scores.Score, name string) *scores.Score {
	for _, s := range ss {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// --- ToolQualityScorer ---

func TestToolQualityScorer_NoTools(t *testing.T) {
	s := &ToolQualityScorer{}
	results := s.Score(context.Background(), &TurnContext{ToolCalls: 0})
	if len(results) != 0 {
		t.Errorf("expected no scores when no tools used, got %d", len(results))
	}
}

func TestToolQualityScorer_AllSuccess(t *testing.T) {
	tc := &TurnContext{
		TraceID:   "trace-1",
		SessionID: "sess-1",
		ToolCalls: 2,
		ToolResults: map[string]ToolResult{
			"tc1": {Name: "sandbox_exec", Success: true, Result: "output line one here"},
			"tc2": {Name: "web_search", Success: true, Result: "search results here yeah"},
		},
		AssistantText: "Based on the output line one here, I found search results here yeah.",
	}

	s := &ToolQualityScorer{}
	results := s.Score(context.Background(), tc)

	sr := findScore(results, "tool_quality.success_rate")
	if sr == nil || *sr.NumericValue != 1.0 {
		t.Errorf("expected success_rate=1.0, got %v", sr)
	}

	av := findScore(results, "tool_quality.args_valid")
	if av == nil || *av.BooleanValue != true {
		t.Errorf("expected args_valid=true, got %v", av)
	}
}

func TestToolQualityScorer_PartialFailure(t *testing.T) {
	tc := &TurnContext{
		TraceID:   "trace-1",
		SessionID: "sess-1",
		ToolCalls: 3,
		ToolResults: map[string]ToolResult{
			"tc1": {Name: "sandbox_exec", Success: true, Result: "ok"},
			"tc2": {Name: "sandbox_exec", Success: false, Result: "Error: command not found"},
			"tc3": {Name: "web_search", Success: true, Result: "results"},
		},
	}

	s := &ToolQualityScorer{}
	results := s.Score(context.Background(), tc)

	sr := findScore(results, "tool_quality.success_rate")
	if sr == nil {
		t.Fatal("expected success_rate score")
	}
	expected := 2.0 / 3.0
	if diff := *sr.NumericValue - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("expected success_rate=%.2f, got %.2f", expected, *sr.NumericValue)
	}
}

// --- TaskCompletionScorer ---

func TestTaskCompletionScorer_NormalFinish(t *testing.T) {
	tc := &TurnContext{
		TraceID:       "trace-1",
		SessionID:     "sess-1",
		FinishReason:  "stop",
		UserInput:     "explain the database schema for users table",
		AssistantText: "The users table in the database schema contains...",
		IterationCount: 3,
		ToolErrors:    0,
	}

	s := &TaskCompletionScorer{}
	results := s.Score(context.Background(), tc)

	finished := findScore(results, "task_completion.finished")
	if finished == nil || *finished.BooleanValue != true {
		t.Errorf("expected finished=true for stop")
	}

	responsive := findScore(results, "task_completion.responsive")
	if responsive == nil || *responsive.BooleanValue != true {
		t.Errorf("expected responsive=true")
	}

	efficiency := findScore(results, "task_completion.efficiency")
	if efficiency == nil || *efficiency.NumericValue != 1.0 {
		t.Errorf("expected efficiency=1.0, got %v", efficiency)
	}
}

func TestTaskCompletionScorer_Stalled(t *testing.T) {
	tc := &TurnContext{
		TraceID:        "trace-1",
		SessionID:      "sess-1",
		FinishReason:   "max_iterations",
		IterationCount: 10,
		ToolErrors:     3,
	}

	s := &TaskCompletionScorer{}
	results := s.Score(context.Background(), tc)

	finished := findScore(results, "task_completion.finished")
	if finished == nil || *finished.BooleanValue != false {
		t.Errorf("expected finished=false for max_iterations")
	}

	efficiency := findScore(results, "task_completion.efficiency")
	if efficiency == nil || *efficiency.NumericValue >= 1.0 {
		t.Errorf("expected efficiency < 1.0, got %v", efficiency.NumericValue)
	}
}

// --- LoopHealthScorer ---

func TestLoopHealthScorer_Healthy(t *testing.T) {
	tc := &TurnContext{
		TraceID:        "trace-1",
		SessionID:      "sess-1",
		FinishReason:   "stop",
		IterationCount: 5,
		ToolCalls:      5,
	}

	s := &LoopHealthScorer{}
	results := s.Score(context.Background(), tc)

	looping := findScore(results, "loop_health.looping")
	if looping == nil || *looping.BooleanValue != false {
		t.Errorf("expected looping=false")
	}

	stalled := findScore(results, "loop_health.stalled")
	if stalled == nil || *stalled.BooleanValue != false {
		t.Errorf("expected stalled=false")
	}

	density := findScore(results, "loop_health.tool_density")
	if density == nil || *density.NumericValue != 1.0 {
		t.Errorf("expected density=1.0, got %v", density)
	}
}

func TestLoopHealthScorer_Looping(t *testing.T) {
	tc := &TurnContext{
		TraceID:      "trace-1",
		SessionID:    "sess-1",
		FinishReason: "tool_loop_stalled",
	}

	s := &LoopHealthScorer{}
	results := s.Score(context.Background(), tc)

	looping := findScore(results, "loop_health.looping")
	if looping == nil || *looping.BooleanValue != true {
		t.Errorf("expected looping=true for tool_loop_stalled")
	}
}

// --- PolicyComplianceScorer ---

func TestPolicyComplianceScorer_Clean(t *testing.T) {
	s := NewPolicyComplianceScorer(PolicyConfig{
		BlockedPatterns: BuiltInPolicyPatterns(),
		BlockedKeywords: []string{"confidential"},
	})

	tc := &TurnContext{
		TraceID:       "trace-1",
		SessionID:     "sess-1",
		AssistantText: "Here is the result of the database query.",
	}

	results := s.Score(context.Background(), tc)
	compliant := findScore(results, "policy.compliant")
	if compliant == nil || *compliant.BooleanValue != true {
		t.Errorf("expected compliant=true for clean output")
	}
}

func TestPolicyComplianceScorer_SSN(t *testing.T) {
	s := NewPolicyComplianceScorer(PolicyConfig{
		BlockedPatterns: BuiltInPolicyPatterns(),
	})

	tc := &TurnContext{
		TraceID:       "trace-1",
		SessionID:     "sess-1",
		AssistantText: "The user's SSN is 123-45-6789 which we found in the database.",
	}

	results := s.Score(context.Background(), tc)
	patternViolation := findScore(results, "policy.blocked_pattern")
	if patternViolation == nil || *patternViolation.BooleanValue != true {
		t.Errorf("expected blocked_pattern=true for SSN")
	}

	compliant := findScore(results, "policy.compliant")
	if compliant == nil || *compliant.BooleanValue != false {
		t.Errorf("expected compliant=false when SSN detected")
	}
}

func TestPolicyComplianceScorer_BlockedKeyword(t *testing.T) {
	s := NewPolicyComplianceScorer(PolicyConfig{
		BlockedKeywords: []string{"internal only"},
	})

	tc := &TurnContext{
		TraceID:       "trace-1",
		SessionID:     "sess-1",
		AssistantText: "This document is marked Internal Only and should not be shared.",
	}

	results := s.Score(context.Background(), tc)
	compliant := findScore(results, "policy.compliant")
	if compliant == nil || *compliant.BooleanValue != false {
		t.Errorf("expected compliant=false for blocked keyword")
	}
}

// --- Pipeline integration ---

func TestPipeline_ScoreTurn(t *testing.T) {
	rec := &mockRecorder{}
	pipeline := DefaultPipeline(rec, nil)

	tc := &TurnContext{
		TraceID:        "trace-1",
		SessionID:      "sess-1",
		AgentID:        "agent-1",
		TenantID:       "tenant-1",
		TurnNumber:     1,
		UserInput:      "fix the authentication bug in the login handler",
		AssistantText:  "I've fixed the authentication bug in the login handler by updating the token validation logic.",
		FinishReason:   "stop",
		ToolCalls:      2,
		ToolErrors:     0,
		IterationCount: 3,
		ToolResults: map[string]ToolResult{
			"tc1": {Name: "sandbox_exec", Success: true, Result: "File updated successfully"},
			"tc2": {Name: "sandbox_exec", Success: true, Result: "All tests passing"},
		},
		SandboxToolCalls: 2,
	}

	pipeline.ScoreTurn(context.Background(), tc)

	all := rec.allScores()
	if len(all) == 0 {
		t.Fatal("expected scores to be recorded")
	}

	// Should have scores from all scorers
	scoreNames := make(map[string]bool)
	for _, s := range all {
		scoreNames[s.Name] = true
	}

	expectedPrefixes := []string{
		"tool_quality.", "task_completion.", "loop_health.", "sandbox_hygiene.", "policy.",
	}
	for _, prefix := range expectedPrefixes {
		found := false
		for name := range scoreNames {
			if len(name) > len(prefix) && name[:len(prefix)] == prefix {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected at least one score with prefix %q", prefix)
		}
	}

	// All scores should have correct trace ID
	for _, s := range all {
		if s.TraceID != "trace-1" {
			t.Errorf("expected trace_id=trace-1, got %s for score %s", s.TraceID, s.Name)
		}
		if s.Source != scores.ScoreSourceEval {
			t.Errorf("expected source=EVAL, got %s for score %s", s.Source, s.Name)
		}
	}
}

// --- SandboxHygieneScorer ---

func TestSandboxHygieneScorer_NoSandbox(t *testing.T) {
	s := &SandboxHygieneScorer{}
	results := s.Score(context.Background(), &TurnContext{SandboxToolCalls: 0})
	if len(results) != 0 {
		t.Errorf("expected no scores when no sandbox tools used")
	}
}

func TestSandboxHygieneScorer_AllSuccess(t *testing.T) {
	tc := &TurnContext{
		TraceID:          "trace-1",
		SessionID:        "sess-1",
		SandboxToolCalls: 2,
		ToolResults: map[string]ToolResult{
			"tc1": {Name: "sandbox_exec", Success: true, Result: "ok"},
			"tc2": {Name: "sandbox_write_file", Success: true, Result: "written"},
		},
	}

	s := &SandboxHygieneScorer{}
	results := s.Score(context.Background(), tc)

	rate := findScore(results, "sandbox_hygiene.exit_code_rate")
	if rate == nil || *rate.NumericValue != 1.0 {
		t.Errorf("expected exit_code_rate=1.0")
	}

	stderr := findScore(results, "sandbox_hygiene.stderr_volume")
	if stderr == nil || *stderr.BooleanValue != false {
		t.Errorf("expected stderr_volume=false")
	}
}
