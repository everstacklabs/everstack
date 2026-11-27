package retrypolicy

import (
	mf "github.com/everstacklabs/everstack/internal/lib/mferrors"
)

// ShouldFallback reports whether a failed attempt may be answered by the next
// target of a fallback plan (another model, usually another provider). It is
// broader than IsRetryable, which decides whether the SAME target may be tried
// again:
//
//   - 429, 408, 409, 5xx, timeouts and connection failures fall back.
//   - 401/403 fall back: the handler rotates keys first, and a different
//     provider has different keys.
//   - Request errors (any other typed 4xx: 400 invalid request, 404, 413,
//     422, context length exceeded) do not. The next model gets the same
//     request and fails the same way, and every fallback attempt is charged.
//
// Context-length errors deliberately do not fall back. A larger-context
// fallback model is a legitimate configuration, but nothing in the plan says
// which targets have a larger window, and providers report the condition
// inconsistently (OpenAI "context_length_exceeded", Anthropic a plain 400
// "prompt is too long"), so it cannot be told apart reliably from other 400s.
//
// Untyped errors still fall back. Most adapters do not yet return
// gateway.UpstreamError, so an untyped error carries no reliable status and
// refusing to fall back on it would remove fallback for an upstream 5xx on
// every such adapter. Only errors that are known request errors stop it.
func ShouldFallback(err error) bool {
	if err == nil || IsTerminalRefusal(err) {
		return false
	}
	if status, ok := upstreamStatus(err); ok {
		switch {
		case status == 401 || status == 403:
			return true
		case status == 408 || status == 409 || status == 429 || status >= 500:
			return true
		case status >= 400:
			return false
		}
	}
	if mf.IsInvalidArgument(err) {
		return false
	}
	return true
}
