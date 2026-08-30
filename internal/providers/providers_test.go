package providers

import (
	"testing"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestChatRequestTimeoutExtendsReasoningRequests(t *testing.T) {
	t.Parallel()

	if got := ChatRequestTimeout(gw.SamplingParams{}, 12*time.Second); got != 12*time.Second {
		t.Fatalf("ordinary timeout = %s, want 12s", got)
	}
	if got := ChatRequestTimeout(gw.SamplingParams{ReasoningEffort: "high"}, 12*time.Second); got != 120*time.Second {
		t.Fatalf("reasoning timeout = %s, want 120s", got)
	}
}
