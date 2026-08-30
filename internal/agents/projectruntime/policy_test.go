package projectruntime

import (
	"strings"
	"testing"
)

func TestValidateFunctionSandboxPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config map[string]interface{}
		want   string
	}{
		"missing sandbox": {
			config: map[string]interface{}{},
			want:   "explicit sandbox configuration",
		},
		"disabled sandbox": {
			config: map[string]interface{}{"sandbox": map[string]interface{}{"enabled": false, "network_mode": "deny"}},
			want:   "sandbox.enabled must be true",
		},
		"missing network mode": {
			config: map[string]interface{}{"sandbox": map[string]interface{}{"enabled": true}},
			want:   "sandbox.network_mode must be deny, whitelist or allow",
		},
		"unknown network mode": {
			config: map[string]interface{}{"sandbox": map[string]interface{}{"enabled": true, "network_mode": "denyy"}},
			want:   "sandbox.network_mode must be deny, whitelist or allow",
		},
		"explicit deny": {
			config: map[string]interface{}{"sandbox": map[string]interface{}{"enabled": true, "network_mode": "deny"}},
		},
		"explicit allow": {
			config: map[string]interface{}{"sandbox": map[string]interface{}{"enabled": true, "network_mode": "allow"}},
		},
	}

	for name, tt := range tests {
		name, tt := name, tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := ValidateFunctionSandboxPolicy(tt.config)
			if tt.want == "" && err != nil {
				t.Fatalf("ValidateFunctionSandboxPolicy() error = %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("ValidateFunctionSandboxPolicy() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
