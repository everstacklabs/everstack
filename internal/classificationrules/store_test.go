package classificationrules

import "testing"

func TestValidateKind(t *testing.T) {
	for _, k := range []string{"retrieval", "RAG step", "tool-call", "kind_1"} {
		if err := ValidateKind(k); err != nil {
			t.Errorf("ValidateKind(%q) = %v, want nil", k, err)
		}
	}
	// A kind is inlined into SQL, so quotes/semicolons must be rejected.
	for _, k := range []string{"", "x'); drop", "a'b", "kind;", "way-too-long-kind-label-that-exceeds-the-forty-char-limit"} {
		if err := ValidateKind(k); err == nil {
			t.Errorf("ValidateKind(%q) = nil, want error", k)
		}
	}
}

func TestValidatePattern(t *testing.T) {
	for _, p := range []string{"retriever.%", "agent.turn.%", "tool_%", "mcp.call"} {
		if err := ValidatePattern(p); err != nil {
			t.Errorf("ValidatePattern(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{"", "has space", "a'b", "x;y"} {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("ValidatePattern(%q) = nil, want error", p)
		}
	}
}
