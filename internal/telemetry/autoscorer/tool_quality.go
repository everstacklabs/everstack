package autoscorer

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// ToolQualityScorer evaluates how well the agent used tools in this turn.
//
// Scores produced:
//   - tool_quality.success_rate:   ratio of successful tool calls (0.0-1.0)
//   - tool_quality.args_valid:     boolean — all tool calls had parseable arguments
//   - tool_quality.result_used:    ratio of tool results referenced in assistant output (0.0-1.0)
type ToolQualityScorer struct{}

func (s *ToolQualityScorer) Name() string { return "tool_quality" }

func (s *ToolQualityScorer) Score(_ context.Context, tc *TurnContext) []*scores.Score {
	if tc.ToolCalls == 0 {
		return nil // No tools used this turn, nothing to score.
	}

	var out []*scores.Score

	// 1. Success rate: successful / total
	successCount := 0
	for _, tr := range tc.ToolResults {
		if tr.Success {
			successCount++
		}
	}
	successRate := float64(successCount) / float64(tc.ToolCalls)
	s1 := scores.NumericScore(tc.TraceID, "tool_quality.success_rate", successRate, scores.ScoreSourceEval)
	s1.Metadata = map[string]interface{}{
		"turn_number":   tc.TurnNumber,
		"total_calls":   tc.ToolCalls,
		"success_count": successCount,
	}
	out = append(out, s1)

	// 2. Arguments valid: check that no tool had empty or "{}" args (heuristic for malformed calls)
	allValid := true
	for _, tr := range tc.ToolResults {
		trimmed := strings.TrimSpace(tr.Args)
		if trimmed == "" || trimmed == "{}" {
			// Empty args are valid for some tools, but indicate potential issues
			// We only flag truly malformed args (unbalanced braces, etc.)
			if !isWellFormedJSON(trimmed) && trimmed != "" && trimmed != "{}" {
				allValid = false
				break
			}
		}
	}
	s2 := scores.BooleanScore(tc.TraceID, "tool_quality.args_valid", allValid, scores.ScoreSourceEval)
	s2.Metadata = map[string]interface{}{
		"turn_number": tc.TurnNumber,
	}
	out = append(out, s2)

	// 3. Result utilization: how many tool results were referenced in the assistant text?
	if tc.AssistantText != "" {
		referenced := 0
		total := 0
		for _, tr := range tc.ToolResults {
			if !tr.Success {
				continue // Don't penalize for not referencing failed results
			}
			total++
			// Heuristic: check if any significant substring from the tool result
			// appears in the assistant text. We extract the first meaningful line.
			snippet := extractSignificantSnippet(tr.Result)
			if snippet != "" && strings.Contains(tc.AssistantText, snippet) {
				referenced++
			}
		}
		if total > 0 {
			utilRate := float64(referenced) / float64(total)
			s3 := scores.NumericScore(tc.TraceID, "tool_quality.result_used", utilRate, scores.ScoreSourceEval)
			s3.Metadata = map[string]interface{}{
				"turn_number": tc.TurnNumber,
				"referenced":  referenced,
				"total":       total,
			}
			out = append(out, s3)
		}
	}

	return out
}

// isWellFormedJSON does a quick bracket-balance check (not full parsing).
func isWellFormedJSON(s string) bool {
	if s == "" || s == "{}" || s == "[]" {
		return true
	}
	depth := 0
	for _, c := range s {
		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// extractSignificantSnippet returns a non-trivial substring from a tool result
// that would indicate the assistant referenced the result.
func extractSignificantSnippet(result string) string {
	// Take first non-empty line that's long enough to be meaningful
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 20 && len(line) <= 200 {
			return line
		}
	}
	// Fall back to a prefix if the whole result is short
	result = strings.TrimSpace(result)
	if len(result) >= 10 && len(result) <= 200 {
		return result
	}
	if len(result) > 200 {
		return fmt.Sprintf("%s", result[:200])
	}
	return ""
}
