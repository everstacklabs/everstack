package v1

import (
	"testing"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// A client on the OpenAI-compatible surface reconstructs its response from
// these chunks. When the streaming conversion dropped usage, every such client
// received token counts of zero and could not attribute cost at all, while the
// non-streaming path returned them correctly the whole time.
func TestChunkConversionCarriesUsage(t *testing.T) {
	got := toProtoResponseFromChunk(gw.ChatResponseChunk{
		ID:      "chatcmpl-1",
		Created: time.Now(),
		Model:   "gpt-4o-mini",
		Usage: &gw.Usage{
			PromptTokens:     15,
			CompletionTokens: 24,
			TotalTokens:      39,
		},
	})

	if got.Usage == nil {
		t.Fatal("usage was dropped; a client cannot attribute cost without it")
	}
	if got.Usage.PromptTokens != 15 || got.Usage.CompletionTokens != 24 || got.Usage.TotalTokens != 39 {
		t.Errorf("usage = %d/%d/%d, want 15/24/39",
			got.Usage.PromptTokens, got.Usage.CompletionTokens, got.Usage.TotalTokens)
	}
}

// Chunks without usage (every chunk but the last) must not invent one, or a
// consumer accumulating frames would overwrite the real totals with zeroes.
func TestChunkWithoutUsageStaysNil(t *testing.T) {
	got := toProtoResponseFromChunk(gw.ChatResponseChunk{ID: "c", Created: time.Now(), Model: "m"})
	if got.Usage != nil {
		t.Errorf("expected no usage on a mid-stream chunk, got %+v", got.Usage)
	}
}

func TestResponseUsageFromChatCarriesCacheBuckets(t *testing.T) {
	t.Parallel()

	got := responseUsageFromChat(&gw.Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
		TotalTokens:      1100,
		PromptDetails: &gw.TokenDetails{
			CachedTokens:     700,
			CacheReadTokens:  500,
			CacheWriteTokens: 200,
		},
	})
	if got == nil || got.InputTokensDetails == nil {
		t.Fatalf("response usage = %+v, want input details", got)
	}
	if got.CacheReadCount() != 500 || got.CacheWriteCount() != 200 {
		t.Fatalf("response cache read/write = %d/%d, want 500/200", got.CacheReadCount(), got.CacheWriteCount())
	}

	protoUsage := convertResponseToProto(&gw.CreateResponseResponse{Usage: got}).Usage
	if protoUsage == nil || protoUsage.InputTokensDetails == nil {
		t.Fatalf("proto response usage = %+v, want input details", protoUsage)
	}
	if protoUsage.InputTokensDetails.CachedTokens != 500 || protoUsage.InputTokensDetails.CacheWriteTokens != 200 {
		t.Fatalf("proto response cache read/write = %d/%d, want 500/200",
			protoUsage.InputTokensDetails.CachedTokens,
			protoUsage.InputTokensDetails.CacheWriteTokens,
		)
	}
}

// finish_reason only arrives on the final chunk. Reconstructing every response
// with a hardcoded "stop" misreports a completion truncated by max_tokens or
// halted by a content filter.
func TestChunkConversionCarriesFinishReason(t *testing.T) {
	got := toProtoResponseFromChunk(gw.ChatResponseChunk{
		ID: "c", Created: time.Now(), Model: "m",
		Choices: []gw.ChoiceDelta{{FinishReason: "length"}},
	})
	if len(got.Choices) != 1 {
		t.Fatalf("expected one choice, got %d", len(got.Choices))
	}
	if got.Choices[0].FinishReason != "length" {
		t.Errorf("finish_reason = %q, want %q", got.Choices[0].FinishReason, "length")
	}
}
