package issues

import "testing"

func TestNormalizeSignature_StableAcrossVolatileBits(t *testing.T) {
	a := NormalizeSignature(`request 550e8400-e29b-41d4-a716-446655440000 failed at 0x1f after 1234 retries reading /var/run/foo.sock`)
	b := NormalizeSignature(`request 6ba7b810-9dad-11d1-80b4-00c04fd430c8 failed at 0x2a after 9 retries reading /tmp/bar.sock`)
	if a != b {
		t.Fatalf("signatures should match after normalization:\n a=%q\n b=%q", a, b)
	}
	for _, token := range []string{"<uuid>", "<hex>", "<n>", "<path>"} {
		if !contains(a, token) {
			t.Errorf("expected %q in normalized signature %q", token, a)
		}
	}
}

func TestNormalizeSignature_LowercaseTrimCollapse(t *testing.T) {
	got := NormalizeSignature("  TIMEOUT   While    Waiting ")
	if got != "timeout while waiting" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeSignature_QuotedStringsCollapse(t *testing.T) {
	a := NormalizeSignature(`tool "get_weather" returned error`)
	b := NormalizeSignature(`tool "search_docs" returned error`)
	if a != b {
		t.Fatalf("quoted-arg signatures should match: a=%q b=%q", a, b)
	}
}

func TestNormalizeSignature_CapsLength(t *testing.T) {
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	if got := NormalizeSignature(string(long)); len(got) != 200 {
		t.Fatalf("want length 200, got %d", len(got))
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"Rate limit exceeded, retry after 1s":             "rate_limit",
		"This model's maximum context length is 8192":     "context_length",
		"Request blocked by content filter (moderation)":  "guardrail_block",
		"context deadline exceeded":                       "timeout",
		"401 Unauthorized: invalid api key":               "auth",
		`anthropic chat error: {"type":"authentication_error","message":"invalid x-api-key"}`: "auth",
		"tool execution failed: get_weather":              "tool_error",
		"failed to parse JSON response":                   "parse_error",
		"502 Bad Gateway: provider overloaded":            "provider_5xx",
		"something entirely unexpected happened":          "other",
	}
	for msg, want := range cases {
		if got := Classify(msg); got != want {
			t.Errorf("Classify(%q) = %q, want %q", msg, got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
