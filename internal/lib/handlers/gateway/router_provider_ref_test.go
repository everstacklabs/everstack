package gateway

import "testing"

func TestParseProviderRef(t *testing.T) {
	cases := []struct {
		in           string
		wantProvider string
		wantModel    string
		wantOK       bool
	}{
		{"@openai/gpt-5.5", "openai", "gpt-5.5", true},
		{"@cohere/command-a-03-2025", "cohere", "command-a-03-2025", true},
		{"@aws_bedrock/anthropic.claude-3", "aws_bedrock", "anthropic.claude-3", true},
		{"@openai/org/gpt-5.5", "openai", "org/gpt-5.5", true}, // only first "/" splits
		{"gpt-5.5", "", "", false},                            // bare id
		{"@gpt-5.5", "", "", false},                           // no slash
		{"@/gpt-5.5", "", "", false},                          // empty provider
		{"@openai/", "", "", false},                           // empty model
		{"", "", "", false},
		{"@", "", "", false},
	}
	for _, c := range cases {
		p, m, ok := parseProviderRef(c.in)
		if ok != c.wantOK || p != c.wantProvider || m != c.wantModel {
			t.Errorf("parseProviderRef(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, p, m, ok, c.wantProvider, c.wantModel, c.wantOK)
		}
	}
}
