package gateway

import "testing"

// Cost calculation reads cache hits through this accessor. A caller that gets
// zero back bills every cached token at the full input rate, so the fallback
// order matters as much as the happy path.
func TestUsageCacheReadCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		usage *Usage
		want  int
	}{
		{
			name:  "nil usage",
			usage: nil,
			want:  0,
		},
		{
			name:  "provider reports no token details",
			usage: &Usage{PromptTokens: 100},
			want:  0,
		},
		{
			name: "explicit cache read count is preferred",
			usage: &Usage{
				PromptTokens:  1000,
				PromptDetails: &TokenDetails{CacheReadTokens: 640, CachedTokens: 900},
			},
			want: 640,
		},
		{
			name: "legacy aggregate is used when no explicit read count",
			usage: &Usage{
				PromptTokens:  1000,
				PromptDetails: &TokenDetails{CachedTokens: 512},
			},
			want: 512,
		},
		{
			name: "details present but nothing cached",
			usage: &Usage{
				PromptTokens:  1000,
				PromptDetails: &TokenDetails{ReasoningTokens: 40},
			},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.usage.CacheReadCount(); got != tc.want {
				t.Fatalf("CacheReadCount() = %d, want %d", got, tc.want)
			}
		})
	}
}
