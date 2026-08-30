package attributes

import (
	"strings"
	"testing"
)

func TestMaybeRedactToggle(t *testing.T) {
	t.Cleanup(func() { RedactPII = false })

	// Default off: content passes through unchanged.
	RedactPII = false
	const pii = "contact 123-45-6789 or a@b.com"
	if got := maybeRedact(pii); got != pii {
		t.Errorf("redaction should be off by default, got %q", got)
	}

	// Enabled: PII is stripped before storage.
	RedactPII = true
	got := maybeRedact(pii)
	if strings.Contains(got, "123-45-6789") || strings.Contains(got, "a@b.com") {
		t.Errorf("PII not redacted: %q", got)
	}
	if maybeRedact("") != "" {
		t.Errorf("empty input should stay empty")
	}
}
