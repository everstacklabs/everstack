package customcolumns

import "testing"

func TestValidateKey(t *testing.T) {
	valid := []string{"customer_tier", "Region", "x", "a1_b2", "ABC123"}
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

func TestStoredColumn_Definition(t *testing.T) {
	s := StoredColumn{Key: "tier", Label: "Tier", ValueType: TypeString, Source: SourceMetadata, SourceRef: "customer.tier", Position: 2}
	d := s.Definition()
	if d.Key != "tier" || d.Source != SourceMetadata || d.SourceRef != "customer.tier" || d.ValueType != TypeString {
		t.Fatalf("Definition() did not carry fields through: %+v", d)
	}
}
