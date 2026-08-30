package semanticmappings

import "testing"

func TestValidateField(t *testing.T) {
	for _, f := range []string{"model", "provider", "session", "user", "cost", "input", "output", "input_tokens", "output_tokens", "total_tokens"} {
		if err := ValidateField(f); err != nil {
			t.Errorf("ValidateField(%q) = %v, want nil", f, err)
		}
	}
	for _, f := range []string{"", "Model", "latency", "drop table", "span"} {
		if err := ValidateField(f); err == nil {
			t.Errorf("ValidateField(%q) = nil, want error", f)
		}
	}
}

func TestValidateAttrKey(t *testing.T) {
	for _, k := range []string{"my_app.model_id", "gen_ai.request.model", "x", "a-b/c:d", "service.name"} {
		if err := ValidateAttrKey(k); err != nil {
			t.Errorf("ValidateAttrKey(%q) = %v, want nil", k, err)
		}
	}
	// Injection-shaped and otherwise invalid keys must be rejected.
	for _, k := range []string{"", "has space", "x'] != '' OR '1'='1", "a;b", "a,b", "quote'"} {
		if err := ValidateAttrKey(k); err == nil {
			t.Errorf("ValidateAttrKey(%q) = nil, want error", k)
		}
	}
}

func TestMappings_For(t *testing.T) {
	m := Mappings{"model": {"a", "b"}}
	if got := m.For("model"); len(got) != 2 {
		t.Fatalf("For(model) = %v, want 2 keys", got)
	}
	if got := m.For("session"); got != nil {
		t.Fatalf("For(session) = %v, want nil", got)
	}
}
