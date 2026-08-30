package autoscorer

import (
	"context"
	"strings"

	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// LoopHealthScorer detects pathological agent behaviors: looping, stalling, excessive tool use.
//
// Scores produced:
//   - loop_health.looping:      boolean — did the agent repeat identical tool calls?
//   - loop_health.stalled:      boolean — did the turn end due to stall/no-results?
//   - loop_health.tool_density: numeric — tool calls per iteration (high = potential over-tooling)
type LoopHealthScorer struct{}

func (s *LoopHealthScorer) Name() string { return "loop_health" }

func (s *LoopHealthScorer) Score(_ context.Context, tc *TurnContext) []*scores.Score {
	var out []*scores.Score

	// 1. Looping: detected by the loop's own stall detector
	looping := tc.FinishReason == "tool_loop_stalled"
	s1 := scores.BooleanScore(tc.TraceID, "loop_health.looping", looping, scores.ScoreSourceEval)
	s1.Metadata = map[string]interface{}{
		"turn_number":   tc.TurnNumber,
		"finish_reason": tc.FinishReason,
	}
	out = append(out, s1)

	// 2. Stalled: turn ended without producing results
	stalled := tc.FinishReason == "tool_loop_no_results" ||
		tc.FinishReason == "max_iterations" ||
		tc.FinishReason == "timeout"
	s2 := scores.BooleanScore(tc.TraceID, "loop_health.stalled", stalled, scores.ScoreSourceEval)
	s2.Metadata = map[string]interface{}{
		"turn_number":   tc.TurnNumber,
		"finish_reason": tc.FinishReason,
	}
	out = append(out, s2)

	// 3. Tool density: tool calls per iteration
	if tc.IterationCount > 0 {
		density := float64(tc.ToolCalls) / float64(tc.IterationCount)
		s3 := scores.NumericScore(tc.TraceID, "loop_health.tool_density", density, scores.ScoreSourceEval)
		s3.Metadata = map[string]interface{}{
			"turn_number":    tc.TurnNumber,
			"tool_calls":     tc.ToolCalls,
			"iteration_count": tc.IterationCount,
		}
		out = append(out, s3)
	}

	return out
}

// SandboxHygieneScorer evaluates the quality of sandbox tool usage.
//
// Scores produced:
//   - sandbox_hygiene.exit_code_rate:  ratio of zero exit codes (0.0-1.0)
//   - sandbox_hygiene.stderr_volume:   boolean — excessive stderr output detected
type SandboxHygieneScorer struct{}

func (s *SandboxHygieneScorer) Name() string { return "sandbox_hygiene" }

func (s *SandboxHygieneScorer) Score(_ context.Context, tc *TurnContext) []*scores.Score {
	if tc.SandboxToolCalls == 0 {
		return nil
	}

	var out []*scores.Score

	// 1. Success rate of sandbox tools specifically
	sandboxSuccess := 0
	stderrHeavy := false
	for _, tr := range tc.ToolResults {
		if !isSandboxTool(tr.Name) {
			continue
		}
		if tr.Success {
			sandboxSuccess++
		}
		// Heuristic: if result contains many lines starting with common error prefixes
		if hasExcessiveStderr(tr.Result) {
			stderrHeavy = true
		}
	}

	exitCodeRate := float64(sandboxSuccess) / float64(tc.SandboxToolCalls)
	s1 := scores.NumericScore(tc.TraceID, "sandbox_hygiene.exit_code_rate", exitCodeRate, scores.ScoreSourceEval)
	s1.Metadata = map[string]interface{}{
		"turn_number":        tc.TurnNumber,
		"sandbox_calls":      tc.SandboxToolCalls,
		"sandbox_success":    sandboxSuccess,
	}
	out = append(out, s1)

	// 2. Excessive stderr
	s2 := scores.BooleanScore(tc.TraceID, "sandbox_hygiene.stderr_volume", stderrHeavy, scores.ScoreSourceEval)
	s2.Metadata = map[string]interface{}{
		"turn_number": tc.TurnNumber,
	}
	out = append(out, s2)

	return out
}

func isSandboxTool(name string) bool {
	return strings.HasPrefix(name, "sandbox_")
}

// hasExcessiveStderr checks if a tool result contains a lot of error-like output.
func hasExcessiveStderr(result string) bool {
	errorLines := 0
	totalLines := 0
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		totalLines++
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "error") ||
			strings.HasPrefix(lower, "err:") ||
			strings.HasPrefix(lower, "warning:") ||
			strings.HasPrefix(lower, "fatal") ||
			strings.Contains(lower, "traceback") ||
			strings.Contains(lower, "panic:") ||
			strings.Contains(lower, "stack trace") {
			errorLines++
		}
	}
	// More than 50% error lines in a result with 5+ lines = excessive
	return totalLines >= 5 && float64(errorLines)/float64(totalLines) > 0.5
}
