package sandbox

import "testing"

func TestPreferredSandboxName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		inst *Instance
		want string
	}{
		{
			name: "nil instance",
			inst: nil,
			want: "",
		},
		{
			name: "instance name wins",
			inst: &Instance{
				Name: "red-rook",
				Config: InstanceConfig{
					Name: "blue-hawk",
				},
			},
			want: "red-rook",
		},
		{
			name: "fallback to config name",
			inst: &Instance{
				Name: "",
				Config: InstanceConfig{
					Name: "blue-hawk",
				},
			},
			want: "blue-hawk",
		},
		{
			name: "trim whitespace",
			inst: &Instance{
				Name: "   ",
				Config: InstanceConfig{
					Name: " green-fox ",
				},
			},
			want: "green-fox",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := preferredSandboxName(tc.inst)
			if got != tc.want {
				t.Fatalf("preferredSandboxName() = %q, want %q", got, tc.want)
			}
		})
	}
}
