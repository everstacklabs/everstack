// Package geminiusage maps the usageMetadata block that Gemini returns, from
// both the Gemini API and Vertex AI, onto the gateway's OpenAI-shaped Usage.
package geminiusage

import (
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Metadata is GenerateContentResponse.usageMetadata. The Gemini API and Vertex
// AI share the schema, and streamGenerateContent repeats it on every chunk
// with cumulative counts, the last chunk being complete.
//
// Gemini reports the buckets disjointly:
//
//	totalTokenCount = promptTokenCount + candidatesTokenCount
//	                + toolUsePromptTokenCount + thoughtsTokenCount
//
// promptTokenCount already includes cachedContentTokenCount.
type Metadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	// ToolUsePromptTokenCount counts results of built-in tool executions
	// (search grounding, code execution, URL context) fed back to the model
	// as input.
	ToolUsePromptTokenCount int `json:"toolUsePromptTokenCount,omitempty"`
	// ThoughtsTokenCount counts the thinking tokens of thinking models.
	// Google bills them at the output rate, but candidatesTokenCount
	// excludes them.
	ThoughtsTokenCount int `json:"thoughtsTokenCount,omitempty"`
	TotalTokenCount    int `json:"totalTokenCount"`
}

// ToUsage folds Gemini's disjoint buckets into OpenAI semantics so that the
// cost calculator and billing meter, which price only PromptTokens (less the
// cached share) at the input rate and CompletionTokens at the output rate,
// charge what Google charges:
//
//   - PromptTokens     = prompt + toolUsePrompt (both input-priced)
//   - CompletionTokens = candidates + thoughts (both output-priced)
//   - TotalTokens      = totalTokenCount, which then equals Prompt + Completion
//
// Thoughts are also reported as completion_tokens_details.reasoning_tokens,
// like OpenAI's reasoning_tokens: a breakdown of CompletionTokens, never
// priced on top of it.
func (m Metadata) ToUsage() gw.Usage {
	u := gw.Usage{
		PromptTokens:     m.PromptTokenCount + m.ToolUsePromptTokenCount,
		CompletionTokens: m.CandidatesTokenCount + m.ThoughtsTokenCount,
		TotalTokens:      m.TotalTokenCount,
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	if m.CachedContentTokenCount > 0 {
		u.PromptDetails = &gw.TokenDetails{
			CachedTokens:    m.CachedContentTokenCount,
			CacheReadTokens: m.CachedContentTokenCount,
			TextTokens:      m.PromptTokenCount - m.CachedContentTokenCount,
		}
	}
	if m.ThoughtsTokenCount > 0 {
		u.CompletionDetails = &gw.TokenDetails{
			ReasoningTokens: m.ThoughtsTokenCount,
			TextTokens:      m.CandidatesTokenCount,
		}
	}
	return u
}
