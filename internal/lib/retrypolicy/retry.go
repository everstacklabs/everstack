package retrypolicy

import (
	"context"
	"errors"
	"strings"

	mf "github.com/everstacklabs/everstack/internal/lib/mferrors"
)

// IsRetryable returns true for errors that are safe to retry or fallback on.
// Heuristics: timeouts, unavailable, resource exhausted (rate limit), transient internal.
// NOTE: Auth errors are NOT retryable with the same key, but should trigger key rotation.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Auth errors should not be retried with the same key
	if IsAuthenticationError(err) {
		return false
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
	if err == nil {
		return false
	}
	// Check for explicit unauthenticated error type
	if mf.IsUnauthenticated(err) {
		return true
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

	return strings.Contains(msg, "invalid api token") ||
		strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "unauthenticated") ||
		strings.Contains(msg, "invalid_api_key") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "cohere error") // Catch Cohere API errors
}
