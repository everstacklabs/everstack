package v1

import "testing"

func TestLinkedSessionIDFromAgentConfig(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "empty input",
			raw:  "",
			want: "",
		},
		{
			name: "missing sandbox key",
			raw:  `{"memory": {"enabled": true}}`,
			want: "",
		},
		{
			name: "sandbox without linked_session_id",
			raw:  `{"sandbox": {"enabled": true, "cpu_limit": 1}}`,
			want: "",
		},
		{
			name: "linked_session_id present",
			raw:  `{"sandbox": {"enabled": true, "linked_session_id": "ses_abc123"}}`,
			want: "ses_abc123",
		},
		{
			name: "linked_session_id empty string",
			raw:  `{"sandbox": {"linked_session_id": ""}}`,
			want: "",
		},
		{
			name: "invalid json",
			raw:  `{not json}`,
			want: "",
		},
		{
			name: "sandbox not an object",
			raw:  `{"sandbox": "string-value"}`,
			want: "",
		},
		{
			name: "linked_session_id non-string",
			raw:  `{"sandbox": {"linked_session_id": 42}}`,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := linkedSessionIDFromAgentConfig([]byte(tc.raw))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
