package memory

import "testing"

func TestInferScope(t *testing.T) {
	tests := []struct {
		input  string
		expect MemoryScope
	}{
		{"user", MemoryScopeUser},
		{"agent", MemoryScopeAgent},
		{"global", MemoryScopeGlobal},
		{"", MemoryScopeAgent},       // default
		{"unknown", MemoryScopeAgent}, // default
		{"USER", MemoryScopeAgent},    // case sensitive, defaults to agent
	}

	for _, tt := range tests {
		got := inferScope(tt.input)
		if got != tt.expect {
			t.Errorf("inferScope(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}
