package retrypolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

type trialRefusal struct{}

func (trialRefusal) Error() string             { return "trial refused" }
func (trialRefusal) ModelTrialRefusal() string { return "rate" }

func TestShouldFallback(t *testing.T) {
	_, dialErr := net.Dial("tcp", "127.0.0.1:1") // nothing listens on port 1
	if dialErr == nil {
		t.Skip("port 1 accepted a connection")
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", statusErr{429, "slow down"}, true},
		{"500", statusErr{500, "boom"}, true},
		{"503", statusErr{503, "overloaded"}, true},
		{"408", statusErr{408, "timeout"}, true},
		{"401 after rotation", statusErr{401, "bad key"}, true},
		{"403 after rotation", statusErr{403, "forbidden"}, true},
		{"400", statusErr{400, "invalid request"}, false},
		{"404", statusErr{404, "no such model"}, false},
		{"422", statusErr{422, "unprocessable"}, false},
		{"context length", statusErr{400, "maximum context length is 8192 tokens (code=context_length_exceeded)"}, false},
		{"wrapped 400", fmt.Errorf("attempt: %w", statusErr{400, "bad"}), false},
		{"connection refused", dialErr, true},
		{"deadline", context.DeadlineExceeded, true},
		{"untyped upstream 503", errors.New("upstream 503"), true},
		{"terminal refusal", trialRefusal{}, false},
	}
	for _, tc := range cases {
		if got := ShouldFallback(tc.err); got != tc.want {
			t.Errorf("%s: ShouldFallback(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}
