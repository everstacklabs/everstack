package retrypolicy

import (
	"context"
	"errors"
	"regexp"
	"strings"

	mf "github.com/everstacklabs/everstack/internal/lib/mferrors"
)

// httpStatusError is implemented by errors that carry an upstream HTTP status
// (gateway.UpstreamError). When the status is known it decides the
// classification; the string heuristics below are only for untyped errors.
// Without this, a 400 body that happened to contain "401" or "unavailable"
// was misclassified.
type httpStatusError interface {
	error
	HTTPStatusCode() int
}

// terminalRefusal is implemented by refusals that must never be retried or
// failed over, whatever their status (billingmeter.ModelTrialRefusedError:
// its rate-limited form is a 429, which would otherwise be retryable).
type terminalRefusal interface {
	error
	ModelTrialRefusal() string
}

// IsTerminalRefusal reports whether err carries a refusal that no retry,
// key rotation or fallback may answer.
func IsTerminalRefusal(err error) bool {
	var tr terminalRefusal
	return errors.As(err, &tr)
}

func upstreamStatus(err error) (int, bool) {
	var se httpStatusError
	if errors.As(err, &se) && se.HTTPStatusCode() > 0 {
		return se.HTTPStatusCode(), true
	}
	return 0, false
}

// IsRetryable returns true for errors that are safe to retry or fallback on.
// Heuristics: timeouts, unavailable, resource exhausted (rate limit), transient internal.
// NOTE: Auth errors are NOT retryable with the same key, but should trigger key rotation.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if IsTerminalRefusal(err) {
		return false
	}
	// Auth errors should not be retried with the same key
	if IsAuthenticationError(err) {
		return false
	}
	if status, ok := upstreamStatus(err); ok {
		return status == 408 || status == 409 || status == 429 || status >= 500
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if mf.IsUnavailable(err) || mf.IsDeadlineExceeded(err) || mf.IsResourceExhausted(err) {
		return true
	}
	// Consider generic internal errors retryable unless caller specifies otherwise
	if mf.IsInternal(err) {
		return true
	}
	// Fuzzy checks for common transient strings
	msg := err.Error()
	if strings.Contains(strings.ToLower(msg), "timeout") ||
		strings.Contains(strings.ToLower(msg), "temporar") ||
		strings.Contains(strings.ToLower(msg), "rate limit") ||
		strings.Contains(strings.ToLower(msg), "too many requests") ||
		strings.Contains(strings.ToLower(msg), "unavailable") ||
		strings.Contains(strings.ToLower(msg), "overloaded") ||
		strings.Contains(strings.ToLower(msg), "529") {
		return true
	}
	return false
}

// IsRateLimitError returns true if the error is specifically a rate-limit error.
// This is a narrower check than IsRetryable, used by the agent loop to decide
// whether to wait and retry rather than immediately terminating the session.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	if mf.IsResourceExhausted(err) {
		return true
	}
	if status, ok := upstreamStatus(err); ok {
		return status == 429 || status == 529
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "overloaded") ||
		strings.Contains(msg, "529")
}

// IsAuthenticationError returns true if the error is an authentication/authorization failure.
// These errors indicate invalid API key and should trigger key rotation instead of retry.
func IsAuthenticationError(err error) bool {
	if err == nil || IsTerminalRefusal(err) {
		return false
	}
	// Check for explicit unauthenticated error type
	if mf.IsUnauthenticated(err) {
		return true
	}
	if status, ok := upstreamStatus(err); ok {
		return status == 401 || status == 403
	}
	// Fuzzy checks for common auth error patterns
	msg := strings.ToLower(err.Error())

	// Exclude model availability errors that look like auth errors
	// HuggingFace returns "invalid_request_error" with "model_not_supported" code
	if strings.Contains(msg, "model_not_supported") ||
		strings.Contains(msg, "model not found") ||
		strings.Contains(msg, "model") && strings.Contains(msg, "not supported") {
		return false
	}

	// No provider-prefix match ("cohere error" used to be here: it made
	// every Cohere 400 and 429 an auth failure, which sent it through up to
	// 10 key rotations and marked good keys failed) and no bare "401"/"403"
	// substring (a token count of 14013 matched). A typed upstream status is
	// handled above; untyped text needs an explicit marker.
	return strings.Contains(msg, "invalid api token") ||
		strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "unauthenticated") ||
		strings.Contains(msg, "invalid_api_key") ||
		// Provider error bodies the untyped adapters pass through:
		// Anthropic and DeepSeek error types, Gemini's invalid-key reason
		// (sent as HTTP 400), Anthropic's header name, Qwen's code.
		strings.Contains(msg, "authentication_error") ||
		strings.Contains(msg, "permission_error") ||
		strings.Contains(msg, "api_key_invalid") ||
		strings.Contains(msg, "api key not valid") ||
		strings.Contains(msg, "invalid x-api-key") ||
		strings.Contains(msg, "invalid api-key") ||
		strings.Contains(msg, "invalidapikey") ||
		explicitAuthStatus.MatchString(msg)
}

// explicitAuthStatus matches a 401/403 written as an HTTP status: "status
// 401", "status code: 403", "http 401", "returned 401", "error 403",
// "(401)", a JSON code field ("code":401 or "code":"401", Azure's body),
// or "401 unauthorized" / "403 forbidden". Applied to lower-cased text.
var explicitAuthStatus = regexp.MustCompile(`(?:\b(?:status(?:\s*code)?|http(?:/\d(?:\.\d)?)?|returned|error|code)["'\s:=]*\(?|\()40[13]\b|\b401\s+unauthorized\b|\b403\s+forbidden\b`)
