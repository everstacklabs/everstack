package tools

import "testing"

func TestSameGitHubRepo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a    string
		b    string
		same bool
	}{
		{a: "everstacklabs/model-catalog", b: "everstacklabs/model-catalog", same: true},
		{a: "https://github.com/everstacklabs/model-catalog", b: "everstacklabs/model-catalog", same: true},
		{a: "https://github.com/everstacklabs/model-catalog.git", b: "git@github.com:everstacklabs/model-catalog.git", same: true},
		{a: "EverstackLabs/Model-Catalog", b: "everstacklabs/model-catalog", same: true},
		{a: "modelcontextprotocol/model-catalog", b: "everstacklabs/model-catalog", same: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.a+"__"+tc.b, func(t *testing.T) {
			t.Parallel()
			got := sameGitHubRepo(tc.a, tc.b)
			if got != tc.same {
				t.Fatalf("sameGitHubRepo(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
			}
		})
	}
}

