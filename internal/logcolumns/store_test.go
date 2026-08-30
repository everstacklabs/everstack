package logcolumns

import "testing"

func TestValidateKey(t *testing.T) {
	valid := []string{"environment", "Region", "x", "a1_b2", "ABC123"}
	for _, k := range valid {
		if err := ValidateKey(k); err != nil {
			t.Errorf("ValidateKey(%q) = %v, want nil", k, err)
		}
	}
	invalid := []string{"", "has space", "has-dash", "dotted.path", "quote'", "drop;table", "a/b"}
	for _, k := range invalid {
		if err := ValidateKey(k); err == nil {
			t.Errorf("ValidateKey(%q) = nil, want error", k)
		}
	}
}

func TestValidateAttrKey(t *testing.T) {
	// Attribute keys are bound as parameters, so the charset is broader than a
	// column key: dotted/colon/slash/dash paths are allowed (OTEL semconv).
	valid := []string{"deployment.environment", "gen_ai:request", "http/route", "a-b", "x"}
	for _, k := range valid {
		if err := ValidateAttrKey(k); err != nil {
			t.Errorf("ValidateAttrKey(%q) = %v, want nil", k, err)
		}
	}
	invalid := []string{"", "has space", "quote'", "semi;colon", "brace{x}"}
	for _, k := range invalid {
		if err := ValidateAttrKey(k); err == nil {
			t.Errorf("ValidateAttrKey(%q) = nil, want error", k)
		}
	}
}
