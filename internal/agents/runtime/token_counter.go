package runtime

import (
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// EstimateTokens provides a fast heuristic token count for a set of messages.
// Uses ~4 characters per token as a rough approximation. For precise counts,
// use the actual usage from the LLM response.
func EstimateTokens(messages []gw.Message) int {
	totalChars := 0
	for _, msg := range messages {
		// Role overhead (~4 tokens)
		totalChars += 16

		for _, part := range msg.Content {
			if part.Text != nil {
				totalChars += len(*part.Text)
			}
		}

		// Tool calls overhead
		for _, tc := range msg.ToolCalls {
			totalChars += len(tc.Function.Name) + len(tc.Function.Arguments) + 20
		}
	}

	// ~4 chars per token
	return totalChars / 4
}

// EstimateTokensForText estimates tokens for a plain text string.
func EstimateTokensForText(text string) int {
	return len(text) / 4
}
