package traces

import (
	"strings"
	"testing"
)

// The gateway wrapper span repeats the token and cost attributes of the
// provider call it wraps. Summing every span therefore reported exactly twice
// the real usage for an ordinary single-provider completion (verified live:
// a 30/59/89-token request was rolled up as 60/118/178 at double the cost).
// The rollup must restrict the sum to generation spans.
func TestSumGenerationRestrictsToGenerationSpans(t *testing.T) {
	got := sumGenerationSQL(inputTokensSQL())

	if !strings.Contains(got, "sumIf(") {
		t.Fatalf("rollup must use a conditional sum, got %q", got)
	}
	if strings.Contains(got, "sumIf(") && !strings.Contains(got, isGenerationSpanSQL) {
		t.Fatalf("conditional sum is not scoped to generation spans: %q", got)
	}
}

// A trace whose usage lives only on non-generation spans (coding agents
// ingested over OTLP never name a span provider.* nor set observation.type)
// must still report its tokens, so the fragment keeps a whole-trace fallback.
func TestSumGenerationKeepsWholeTraceFallback(t *testing.T) {
	got := sumGenerationSQL(inputTokensSQL())

	condIdx := strings.Index(got, "countIf(")
	if condIdx == -1 {
		t.Fatalf("no guard deciding whether a generation span carries usage: %q", got)
	}
	// The unconditional sum is the else-branch, so it must appear after the guard.
	plainIdx := strings.LastIndex(got, "sum(")
	if plainIdx == -1 || plainIdx < condIdx {
		t.Fatalf("missing whole-trace fallback after the guard: %q", got)
	}
}

// The fragment is inlined into the query text, so a bound placeholder would
// shift every later parameter. Every usage fragment it wraps must be
// placeholder-free, including when widened by tenant semantic mappings.
func TestSumGenerationProducesPlaceholderFreeSQL(t *testing.T) {
	for name, frag := range map[string]string{
		"cost":      costSQL(),
		"input":     inputTokensSQL(),
		"output":    outputTokensSQL(),
		"total":     totalTokensSQL(),
		"cacheRead": cacheReadTokensSQL(),
		"reasoning": reasoningTokensSQL(),
		"mapped":    inputTokensSQL("my_tenant.prompt_tokens"),
	} {
		if got := sumGenerationSQL(frag); strings.Contains(got, "?") {
			t.Fatalf("%s rollup contains a bound placeholder and is unsafe to inline: %q", name, got)
		}
	}
}

// Reasoning tokens are billed as output by providers and were previously read
// from a single hard-coded attribute. Keep the aliases registered so a provider
// using the gen_ai semconv name is not silently counted as zero.
func TestReasoningTokensCoversKnownAliases(t *testing.T) {
	got := reasoningTokensSQL()

	for _, attr := range []string{"llm.tokens.reasoning", "gen_ai.usage.reasoning_tokens"} {
		if !strings.Contains(got, attr) {
			t.Fatalf("reasoning fragment does not read %s: %q", attr, got)
		}
	}
}
