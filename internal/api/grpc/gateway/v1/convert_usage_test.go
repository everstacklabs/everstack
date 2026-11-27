package v1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/encoding/protojson"
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

// /v1/chat/completions serves these protos as OpenAI JSON, so the token
// breakdown has to survive the conversion for a client to see
// usage.completion_tokens_details.reasoning_tokens (OpenAI reasoning, Gemini
// thoughts) and usage.prompt_tokens_details.cached_tokens.
func TestUsageConversionCarriesTokenDetails(t *testing.T) {
	t.Parallel()

	usage := gw.Usage{
		PromptTokens:      2100,
		CompletionTokens:  1031,
		TotalTokens:       3131,
		PromptDetails:     &gw.TokenDetails{CachedTokens: 2048, CacheReadTokens: 2048, TextTokens: 52},
		CompletionDetails: &gw.TokenDetails{ReasoningTokens: 1022, TextTokens: 9},
	}
	unary := toProtoResponse(gw.ChatCompletionResponse{ID: "c", Created: time.Now(), Model: "gemini-2.5-flash", Usage: usage})
	stream := toProtoResponseFromChunk(gw.ChatResponseChunk{ID: "c", Created: time.Now(), Model: "gemini-2.5-flash", Usage: &usage})

	for name, got := range map[string]*gatewaypb.Usage{"unary": unary.Usage, "stream": stream.Usage} {
		if got.GetCompletionTokens() != 1031 {
			t.Errorf("%s: completion_tokens = %d, want 1031", name, got.GetCompletionTokens())
		}
		if got.GetCompletionTokensDetails().GetReasoningTokens() != 1022 {
			t.Errorf("%s: completion_tokens_details = %+v, want reasoning_tokens 1022", name, got.GetCompletionTokensDetails())
		}
		if got.GetPromptTokensDetails().GetCachedTokens() != 2048 {
			t.Errorf("%s: prompt_tokens_details = %+v, want cached_tokens 2048", name, got.GetPromptTokensDetails())
		}
	}
}

func TestUsageConversionOmitsEmptyTokenDetails(t *testing.T) {
	t.Parallel()

	got := toProtoResponse(gw.ChatCompletionResponse{ID: "c", Created: time.Now(), Model: "m", Usage: gw.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}})
	if got.Usage.GetPromptTokensDetails() != nil || got.Usage.GetCompletionTokensDetails() != nil {
		t.Errorf("usage = %+v, want no details when the provider reports none", got.Usage)
	}
}

// The OpenAI surface emits these protos through the JSON gateway's marshal
// options (internal/api/grpc/server/gateway.go). Assert the bytes a client
// receives, not just the proto fields.
func TestUsageDetailsWireJSON(t *testing.T) {
	t.Parallel()

	opts := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}
	withDetails := toProtoUsage(&gw.Usage{
		PromptTokens:      12,
		CompletionTokens:  1031,
		TotalTokens:       1043,
		CompletionDetails: &gw.TokenDetails{ReasoningTokens: 1022, TextTokens: 9},
	})
	b, err := opts.Marshal(withDetails)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	details, ok := got["completion_tokens_details"].(map[string]any)
	if !ok || details["reasoning_tokens"] != float64(1022) {
		t.Fatalf("wire usage = %s, want completion_tokens_details.reasoning_tokens 1022", b)
	}
	if _, ok := got["prompt_tokens_details"]; ok {
		t.Fatalf("wire usage = %s, want no prompt_tokens_details without a breakdown", b)
	}

	plain, err := opts.Marshal(toProtoUsage(&gw.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), "tokens_details") {
		t.Fatalf("wire usage = %s, want no details keys", plain)
	}
}
