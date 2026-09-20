package providers

import (
	"os"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Intentionally left as a placeholder for provider-specific packages to import
// a common namespace if needed in the future. The registry and interfaces live
// under internal/lib/handlers/gateway to avoid import cycles.

// Wall-clock budget bounds for a single unary provider call. These are runaway
// guards, not latency guards: connect, TLS handshake and time-to-first-byte are
// already bounded by the transport, so what remains for this budget to cover is
// generation itself.
const (
	// baseChatBudget covers request setup plus the upstream's own queueing and
	// prefill before the first output token is produced.
	baseChatBudget = 60 * time.Second

	// perOutputToken is the marginal budget granted per requested output token.
	// Sized well above observed decode rates so that a slow-but-working stream
	// is never mistaken for a hung one.
	perOutputToken = 60 * time.Millisecond

	// unboundedOutputBudget applies when the caller sets no output cap. Without
	// max_tokens the response length is bounded only by the model, so we cannot
	// derive a budget and fall back to a generous fixed one.
	unboundedOutputBudget = 300 * time.Second

	// maxChatBudget caps the computed budget so a caller cannot pin a provider
	// goroutine indefinitely by asking for a very large max_tokens.
	maxChatBudget = 600 * time.Second

	// reasoningFloor guarantees reasoning requests enough time even when the
	// token-derived budget would be smaller (e.g. short max_tokens with
	// reasoning enabled).
	reasoningFloor = 120 * time.Second

	// chatTimeoutEnv overrides the computed budget entirely, as a Go duration
	// string (e.g. "90s", "10m"). Intended for operators who need a tighter or
	// looser ceiling than the default policy without a config migration.
	chatTimeoutEnv = "EVERSTACK_PROVIDER_UNARY_TIMEOUT"
)

// ChatRequestTimeout returns the wall-clock budget for a single unary (non
// streaming) provider call.
//
// A unary upstream returns nothing at all until the final token is generated,
// so this budget has to cover generation time. That makes it a function of how
// much output the caller asked for rather than a constant: a fixed low ceiling
// does not make long completions slow, it makes them impossible, and it fails
// deterministically on exactly the requests that need the most time.
//
// Streaming calls must not use this budget. They are bounded by an idle timeout
// instead, because a healthy long stream is indistinguishable from a slow one
// by total elapsed time.
func ChatRequestTimeout(sampling gw.SamplingParams) time.Duration {
	if raw := os.Getenv(chatTimeoutEnv); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}

	budget := unboundedOutputBudget
	if maxOutput := requestedOutputTokens(sampling); maxOutput > 0 {
		budget = baseChatBudget + time.Duration(maxOutput)*perOutputToken
	}

	if isReasoning(sampling) && budget < reasoningFloor {
		budget = reasoningFloor
	}

	if budget > maxChatBudget {
		budget = maxChatBudget
	}
	return budget
}

func isReasoning(sampling gw.SamplingParams) bool {
	return sampling.ReasoningEffort != "" ||
		sampling.ReasoningBudget != nil ||
		sampling.ReasoningEnabled != nil
}

// requestedOutputTokens reports the caller's output cap. max_completion_tokens
// supersedes max_tokens where both are present, matching the OpenAI wire
// contract that deprecated the latter.
func requestedOutputTokens(sampling gw.SamplingParams) int {
	if sampling.MaxCompletionTokens > 0 {
		return sampling.MaxCompletionTokens
	}
	return sampling.MaxTokens
}
