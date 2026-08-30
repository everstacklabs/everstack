package sandbox

import "testing"

func TestIsLoopbackSSHHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want bool
	}{
		{name: "localhost", host: "localhost", want: true},
		{name: "localhost mixed case", host: "LocalHost", want: true},
		{name: "ipv4 loopback", host: "127.0.0.1", want: true},
		{name: "ipv6 loopback", host: "::1", want: true},
		{name: "bracketed ipv6 loopback", host: "[::1]", want: true},
		{name: "remote ipv4", host: "10.1.2.3", want: false},
		{name: "hostname", host: "sandbox.everstack.run", want: false},
		{name: "empty", host: "", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isLoopbackSSHHost(tc.host)
			if got != tc.want {
				t.Fatalf("isLoopbackSSHHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestParseSSHConnectionString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{
			name: "valid",
			in:   "ssh sandbox@localhost -p 2222 -o StrictHostKeyChecking=no",
			want: []string{"sandbox@localhost", "-p", "2222", "-o", "StrictHostKeyChecking=no"},
		},
		{
			name:    "empty",
			in:      "  ",
			wantErr: true,
		},
		{
			name:    "wrong binary",
			in:      "scp sandbox@localhost:/tmp/file .",
			wantErr: true,
		},
		{
			name:    "no args",
			in:      "ssh",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSSHConnectionString(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got args=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("arg count mismatch: got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("arg[%d] mismatch: got %q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
