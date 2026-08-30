package autoscorer

import (
	"context"
	"strings"

	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// TaskCompletionScorer evaluates whether the agent successfully completed the turn.
//
// Scores produced:
//   - task_completion.finished:    boolean — did the turn end normally (stop) vs hitting limits?
//   - task_completion.responsive:  boolean — does the assistant text address the user's input?
//   - task_completion.efficiency:  numeric — ratio of effective iterations to total (penalizes loops)
type TaskCompletionScorer struct{}

func (s *TaskCompletionScorer) Name() string { return "task_completion" }

func (s *TaskCompletionScorer) Score(_ context.Context, tc *TurnContext) []*scores.Score {
	var out []*scores.Score

	// 1. Finished: did the turn end with a natural stop?
	normalFinish := tc.FinishReason == "stop" || tc.FinishReason == ""
	s1 := scores.BooleanScore(tc.TraceID, "task_completion.finished", normalFinish, scores.ScoreSourceEval)
	s1.Metadata = map[string]interface{}{
		"turn_number":   tc.TurnNumber,
		"finish_reason": tc.FinishReason,
	}
	out = append(out, s1)

	// 2. Responsive: does the assistant output reference entities from the user input?
	if tc.UserInput != "" && tc.AssistantText != "" {
		responsive := isResponsive(tc.UserInput, tc.AssistantText)
		s2 := scores.BooleanScore(tc.TraceID, "task_completion.responsive", responsive, scores.ScoreSourceEval)
		s2.Metadata = map[string]interface{}{
			"turn_number": tc.TurnNumber,
		}
		out = append(out, s2)
	}

	// 3. Efficiency: how many iterations were productive vs total?
	// A turn with 10 iterations where 3 were retries/empty = 0.7 efficiency.
	if tc.IterationCount > 0 {
		// Estimate wasted iterations from tool errors and known finish reasons
		wastedIterations := float64(tc.ToolErrors)
		if tc.FinishReason == "tool_loop_stalled" || tc.FinishReason == "tool_loop_no_results" {
			// The last few iterations were wasted
			wastedIterations += 2
		}
		if wastedIterations > float64(tc.IterationCount) {
			wastedIterations = float64(tc.IterationCount)
		}
		efficiency := 1.0 - (wastedIterations / float64(tc.IterationCount))
		if efficiency < 0 {
			efficiency = 0
		}

		s3 := scores.NumericScore(tc.TraceID, "task_completion.efficiency", efficiency, scores.ScoreSourceEval)
		s3.Metadata = map[string]interface{}{
			"turn_number":       tc.TurnNumber,
			"iteration_count":   tc.IterationCount,
			"tool_errors":       tc.ToolErrors,
			"wasted_iterations": wastedIterations,
		}
		out = append(out, s3)
	}

	return out
}

// isResponsive checks if the assistant output references significant words from the user input.
// This is a lightweight heuristic — not a semantic similarity check.
func isResponsive(userInput, assistantText string) bool {
	// Extract significant words (>= 4 chars, not common stop words)
	words := extractSignificantWords(userInput)
	if len(words) == 0 {
		return true // Can't tell, assume responsive
	}

	lower := strings.ToLower(assistantText)
	matches := 0
	for _, w := range words {
		if strings.Contains(lower, w) {
			matches++
		}
	}

	// At least 30% of significant words should appear in the response
	return float64(matches)/float64(len(words)) >= 0.3
}

var stopWords = map[string]bool{
	"this": true, "that": true, "with": true, "from": true, "have": true,
	"been": true, "will": true, "would": true, "could": true, "should": true,
	"what": true, "when": true, "where": true, "which": true, "there": true,
	"their": true, "about": true, "into": true, "your": true, "they": true,
	"some": true, "them": true, "then": true, "than": true, "also": true,
	"just": true, "more": true, "very": true, "each": true, "make": true,
	"like": true, "does": true, "please": true, "want": true, "need": true,
	"help": true, "know": true, "think": true, "here": true,
}

func extractSignificantWords(text string) []string {
	text = strings.ToLower(text)
	// Split on non-alphanumeric
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-')
	})

	var significant []string
	seen := make(map[string]bool)
	for _, w := range words {
		if len(w) < 4 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		significant = append(significant, w)
	}
	return significant
}
