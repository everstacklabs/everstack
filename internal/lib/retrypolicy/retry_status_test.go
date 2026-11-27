package retrypolicy

import (
	"fmt"
	"testing"
)

type statusErr struct {
	status int
	msg    string
}

func (e statusErr) Error() string       { return e.msg }
func (e statusErr) HTTPStatusCode() int { return e.status }

func TestTypedUpstreamStatusDecidesClassification(t *testing.T) {
	cases := []struct {
		err                    error
		auth, retry, rateLimit bool
	}{
		{statusErr{401, "Incorrect API key provided"}, true, false, false},
		{statusErr{403, "forbidden"}, true, false, false},
		// A 400 whose text happens to contain "401" or "unavailable" is
		// still a plain bad request.
		{statusErr{400, "Bad Request: field 401 unavailable"}, false, false, false},
		{statusErr{429, "slow down"}, false, true, true},
		{statusErr{503, "overloaded"}, false, true, false},
		{fmt.Errorf("wrapped: %w", statusErr{401, "x"}), true, false, false},
	}
	for _, tc := range cases {
		if got := IsAuthenticationError(tc.err); got != tc.auth {
			t.Errorf("IsAuthenticationError(%v) = %v, want %v", tc.err, got, tc.auth)
		}
		if got := IsRetryable(tc.err); got != tc.retry {
			t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.retry)
		}
		if got := IsRateLimitError(tc.err); got != tc.rateLimit {
			t.Errorf("IsRateLimitError(%v) = %v, want %v", tc.err, got, tc.rateLimit)
		}
	}
}
