package providers

import (
	"testing"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestChatRequestTimeoutScalesWithRequestedOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sampling gw.SamplingParams
		want     time.Duration
	}{
		{
			name:     "no output cap falls back to the unbounded budget",
			sampling: gw.SamplingParams{},
			want:     300 * time.Second,
		},
		{
			name:     "short answer still clears the base budget",
			sampling: gw.SamplingParams{MaxTokens: 100},
			want:     66 * time.Second,
		},
		{
			name:     "long answer earns proportionally more time",
			sampling: gw.SamplingParams{MaxTokens: 4000},
			want:     300 * time.Second,
		},
		{
			name:     "max_completion_tokens supersedes max_tokens",
			sampling: gw.SamplingParams{MaxTokens: 100, MaxCompletionTokens: 4000},
			want:     300 * time.Second,
		},
		{
			name:     "an extreme output cap is clamped",
			sampling: gw.SamplingParams{MaxCompletionTokens: 1_000_000},
			want:     600 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ChatRequestTimeout(tt.sampling); got != tt.want {
				t.Fatalf("ChatRequestTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A 12s ceiling was the original defect: it bound on generation time, so any
// request whose output was long enough failed deterministically no matter how
// small its input was.
func TestChatRequestTimeoutNeverBindsOnOrdinaryGeneration(t *testing.T) {
	t.Parallel()

	for _, maxTokens := range []int{0, 1, 256, 1024, 8192} {
		got := ChatRequestTimeout(gw.SamplingParams{MaxTokens: maxTokens})
		if got <= 12*time.Second {
			t.Fatalf("max_tokens=%d produced budget %v, which cannot outlast the old 12s ceiling", maxTokens, got)
		}
	}
}

func TestChatRequestTimeoutEnvOverride(t *testing.T) {
	t.Setenv(chatTimeoutEnv, "90s")
	if got := ChatRequestTimeout(gw.SamplingParams{MaxTokens: 4000}); got != 90*time.Second {
		t.Fatalf("ChatRequestTimeout() = %v, want the override 90s", got)
	}

	t.Setenv(chatTimeoutEnv, "not-a-duration")
	if got := ChatRequestTimeout(gw.SamplingParams{}); got != unboundedOutputBudget {
		t.Fatalf("an unparseable override should fall back to policy, got %v", got)
	}
}

func TestChatRequestTimeoutReasoningFloor(t *testing.T) {
	t.Parallel()

	// Reasoning with a small output cap still gets at least 120s.
	if got := ChatRequestTimeout(gw.SamplingParams{ReasoningEffort: "high", MaxTokens: 10}); got != 120*time.Second {
		t.Fatalf("reasoning timeout = %s, want 120s floor", got)
	}
	// Reasoning with a large output cap keeps the larger token-derived budget.
	if got := ChatRequestTimeout(gw.SamplingParams{ReasoningEffort: "high", MaxTokens: 4000}); got != 300*time.Second {
		t.Fatalf("reasoning with large output = %s, want 300s", got)
	}
}
