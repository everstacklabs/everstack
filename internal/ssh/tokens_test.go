package ssh

import "testing"

func TestIsSandboxSSHTokenValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "valid token", in: "ssht_abc123", want: true},
		{name: "valid token with whitespace", in: "  ssht_abc123\n", want: true},
		{name: "empty", in: "", want: false},
		{name: "ssh token id is not raw token", in: "sshtok_abc123", want: false},
		{name: "short code", in: "abc12345", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsSandboxSSHTokenValue(tt.in); got != tt.want {
				t.Fatalf("IsSandboxSSHTokenValue(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeSSHTokenMinutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int32
		want int32
	}{
		{name: "default for zero", in: 0, want: defaultSSHTokenMinutes},
		{name: "default for negative", in: -5, want: defaultSSHTokenMinutes},
		{name: "keeps explicit value", in: 15, want: 15},
		{name: "keeps max", in: maxSSHTokenMinutes, want: maxSSHTokenMinutes},
		{name: "clamps above max", in: maxSSHTokenMinutes + 1, want: maxSSHTokenMinutes},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeSSHTokenMinutes(tt.in); got != tt.want {
				t.Fatalf("NormalizeSSHTokenMinutes(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
