// Package tokens provides token estimation utilities for tracing and observability.
package tokens

import (
	"strings"
	"unicode"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Estimator provides token count estimation for messages.
// It uses a simple character-based heuristic (~4 chars per token on average)
// which is accurate enough for observability purposes without the overhead
// of loading model-specific tokenizers.
type Estimator struct {
	// charsPerToken is the average number of characters per token.
	// The default of 4 works well for English text with most LLM tokenizers.
	charsPerToken float64
}

// NewEstimator creates a new token estimator with default settings.
func NewEstimator() *Estimator {
	return &Estimator{
		charsPerToken: 4.0,
	}
}

// NewEstimatorWithRatio creates a new token estimator with a custom chars-per-token ratio.
func NewEstimatorWithRatio(charsPerToken float64) *Estimator {
	if charsPerToken <= 0 {
		charsPerToken = 4.0
	}
	return &Estimator{
		charsPerToken: charsPerToken,
	}
}

// MessageTokenEstimate represents the estimated token count for a single message.
type MessageTokenEstimate struct {
	Role       string `json:"role"`
	Tokens     int    `json:"tokens"`
	CharCount  int    `json:"char_count"`
	WordCount  int    `json:"word_count"`
	HasImage   bool   `json:"has_image,omitempty"`
	HasToolUse bool   `json:"has_tool_use,omitempty"`
}

// RequestEstimate represents the total token estimate for a request.
type RequestEstimate struct {
	TotalTokens        int                    `json:"total_tokens"`
	PerMessage         []MessageTokenEstimate `json:"per_message"`
	SystemPromptTokens int                    `json:"system_prompt_tokens,omitempty"`
	UserTokens         int                    `json:"user_tokens"`
	AssistantTokens    int                    `json:"assistant_tokens"`
	ToolTokens         int                    `json:"tool_tokens,omitempty"`
}

// EstimateRequest estimates token counts for a chat completion request.
func (e *Estimator) EstimateRequest(messages []gw.Message) RequestEstimate {
	result := RequestEstimate{
		PerMessage: make([]MessageTokenEstimate, 0, len(messages)),
	}

	for _, msg := range messages {
		estimate := e.EstimateMessage(msg)
		result.PerMessage = append(result.PerMessage, estimate)
		result.TotalTokens += estimate.Tokens

		// Categorize by role
		switch msg.Role {
		case gw.RoleSystem:
			result.SystemPromptTokens += estimate.Tokens
		case gw.RoleUser:
			result.UserTokens += estimate.Tokens
		case gw.RoleAssistant:
			result.AssistantTokens += estimate.Tokens
		case gw.RoleTool, gw.RoleFunction:
			result.ToolTokens += estimate.Tokens
		}
	}

	return result
}

// EstimateMessage estimates token counts for a single message.
func (e *Estimator) EstimateMessage(msg gw.Message) MessageTokenEstimate {
	estimate := MessageTokenEstimate{
		Role: string(msg.Role),
	}

	// Accumulate content from all parts
	var textBuilder strings.Builder
	for _, part := range msg.Content {
		switch part.Type {
		case "text":
			if part.Text != nil {
				textBuilder.WriteString(*part.Text)
			}
		case "image_url":
			estimate.HasImage = true
			// Images typically consume 85-170 tokens depending on size
			// We use a conservative estimate of 100 tokens per image
			estimate.Tokens += 100
		}
		if part.ToolCallID != nil || part.FunctionCall != nil {
			estimate.HasToolUse = true
		}
	}

	text := textBuilder.String()
	estimate.CharCount = len(text)
	estimate.WordCount = countWords(text)

	// Estimate tokens from character count
	if estimate.CharCount > 0 {
		estimate.Tokens += int(float64(estimate.CharCount) / e.charsPerToken)
	}

	// Add overhead for message structure (role, content wrappers, etc.)
	// Each message has ~4 tokens of overhead
	estimate.Tokens += 4

	// Ensure minimum of 1 token per message
	if estimate.Tokens < 1 {
		estimate.Tokens = 1
	}

	return estimate
}

// EstimateText estimates token count for raw text.
func (e *Estimator) EstimateText(text string) int {
	if len(text) == 0 {
		return 0
	}
	tokens := int(float64(len(text)) / e.charsPerToken)
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// GetPerMessageTokenCounts returns just the token counts as an int slice
// for use with SetLLMTokenBreakdown.
func (e *Estimator) GetPerMessageTokenCounts(messages []gw.Message) []int {
	counts := make([]int, len(messages))
	for i, msg := range messages {
		estimate := e.EstimateMessage(msg)
		counts[i] = estimate.Tokens
	}
	return counts
}

// countWords counts the number of words in text (space-separated).
func countWords(text string) int {
	if len(text) == 0 {
		return 0
	}

	count := 0
	inWord := false

	for _, r := range text {
		if unicode.IsSpace(r) {
			if inWord {
				count++
				inWord = false
			}
		} else {
			inWord = true
		}
	}

	// Count the last word if text doesn't end with whitespace
	if inWord {
		count++
	}

	return count
}

