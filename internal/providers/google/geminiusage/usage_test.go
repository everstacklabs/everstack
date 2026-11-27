package geminiusage

import (
	"encoding/json"
	"testing"
)

// Recorded-shape usageMetadata blocks. Field names and the totalTokenCount
// identity (prompt + candidates + toolUsePrompt + thoughts) follow the Gemini
// API / Vertex AI GenerateContentResponse.UsageMetadata schema.
const (
	// gemini-2.0-flash, no thinking.
	noThoughts = `{
		"promptTokenCount": 8,
		"candidatesTokenCount": 12,
		"totalTokenCount": 20,
		"promptTokensDetails": [{"modality": "TEXT", "tokenCount": 8}],
		"candidatesTokensDetails": [{"modality": "TEXT", "tokenCount": 12}]
	}`
	// gemini-2.5-flash with default dynamic thinking.
	withThoughts = `{
		"promptTokenCount": 12,
		"candidatesTokenCount": 9,
		"totalTokenCount": 1043,
		"promptTokensDetails": [{"modality": "TEXT", "tokenCount": 12}],
		"thoughtsTokenCount": 1022
	}`
	// gemini-2.5-pro with an explicit cache and a built-in tool (search
	// grounding) whose results were fed back to the model.
	cachedAndToolUse = `{
		"promptTokenCount": 2100,
		"cachedContentTokenCount": 2048,
		"candidatesTokenCount": 50,
		"toolUsePromptTokenCount": 300,
		"thoughtsTokenCount": 200,
		"totalTokenCount": 2650,
		"promptTokensDetails": [{"modality": "TEXT", "tokenCount": 2100}],
		"cacheTokensDetails": [{"modality": "TEXT", "tokenCount": 2048}],
		"toolUsePromptTokensDetails": [{"modality": "TEXT", "tokenCount": 300}]
	}`
)

func decode(t *testing.T, raw string) Metadata {
	t.Helper()
	var m Metadata
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode usageMetadata: %v", err)
	}
	return m
}

func TestToUsageWithoutThoughts(t *testing.T) {
	u := decode(t, noThoughts).ToUsage()
	if u.PromptTokens != 8 || u.CompletionTokens != 12 || u.TotalTokens != 20 {
		t.Fatalf("tokens = %d/%d/%d, want 8/12/20", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails != nil {
		t.Fatalf("CompletionDetails = %+v, want nil without thoughts", u.CompletionDetails)
	}
	if u.PromptDetails != nil {
		t.Fatalf("PromptDetails = %+v, want nil without cache", u.PromptDetails)
	}
}

func TestToUsageCountsThoughtsAsCompletion(t *testing.T) {
	u := decode(t, withThoughts).ToUsage()
	if u.CompletionTokens != 9+1022 {
		t.Fatalf("CompletionTokens = %d, want candidates+thoughts = %d", u.CompletionTokens, 9+1022)
	}
	if u.PromptTokens != 12 {
		t.Fatalf("PromptTokens = %d, want 12", u.PromptTokens)
	}
	if u.TotalTokens != 1043 || u.PromptTokens+u.CompletionTokens != u.TotalTokens {
		t.Fatalf("TotalTokens = %d (prompt+completion = %d), want Gemini's totalTokenCount 1043", u.TotalTokens, u.PromptTokens+u.CompletionTokens)
	}
	if u.CompletionDetails == nil || u.CompletionDetails.ReasoningTokens != 1022 || u.CompletionDetails.TextTokens != 9 {
		t.Fatalf("CompletionDetails = %+v, want reasoning 1022 text 9", u.CompletionDetails)
	}
}

func TestToUsageCacheAndToolUse(t *testing.T) {
	u := decode(t, cachedAndToolUse).ToUsage()
	if u.PromptTokens != 2100+300 {
		t.Fatalf("PromptTokens = %d, want prompt+toolUsePrompt = %d", u.PromptTokens, 2100+300)
	}
	if u.CompletionTokens != 50+200 {
		t.Fatalf("CompletionTokens = %d, want candidates+thoughts = %d", u.CompletionTokens, 50+200)
	}
	if u.TotalTokens != 2650 || u.PromptTokens+u.CompletionTokens != u.TotalTokens {
		t.Fatalf("TotalTokens = %d (prompt+completion = %d), want 2650", u.TotalTokens, u.PromptTokens+u.CompletionTokens)
	}
	if got := u.CacheReadCount(); got != 2048 {
		t.Fatalf("CacheReadCount = %d, want 2048", got)
	}
	if got := u.CacheWriteCount(); got != 0 {
		t.Fatalf("CacheWriteCount = %d, want 0", got)
	}
	if u.CompletionDetails == nil || u.CompletionDetails.ReasoningTokens != 200 {
		t.Fatalf("CompletionDetails = %+v, want reasoning 200", u.CompletionDetails)
	}
}

// The misspelled key the decoder used to read is not part of either API, so
// it must not be counted.
func TestToUsageIgnoresMisspelledThoughtKey(t *testing.T) {
	u := decode(t, `{"promptTokenCount":5,"candidatesTokenCount":5,"totalTokenCount":10,"thoughtTokenCount":99}`).ToUsage()
	if u.CompletionTokens != 5 || u.CompletionDetails != nil {
		t.Fatalf("usage = %+v, want misspelled key ignored", u)
	}
}

func TestToUsageTotalFallsBackToSum(t *testing.T) {
	u := Metadata{PromptTokenCount: 3, CandidatesTokenCount: 4, ThoughtsTokenCount: 5}.ToUsage()
	if u.TotalTokens != 12 {
		t.Fatalf("TotalTokens = %d, want 12 when totalTokenCount is absent", u.TotalTokens)
	}
}
