package toolnames

import "testing"

func TestIsReserved(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"web_fetch":                 true,
		"spawn_agent":               true,
		"publish_site":              true,
		"sandbox_future_capability": true,
		"browser_future_action":     true,
		"mcp__github__create_issue": true,
		"lookup_customer":           false,
	}
	for name, want := range tests {
		if got := IsReserved(name); got != want {
			t.Errorf("IsReserved(%q) = %t, want %t", name, got, want)
		}
	}
}
