package emailaddr

import (
	"strings"
	"testing"
)

func TestValidateAcceptsOrdinaryAddresses(t *testing.T) {
	for _, address := range []string{
		"person@example.com",
		"first.last@example.co.uk",
		"person+tag@example.com",
		"p@e.io",
		"UPPER@Example.COM",
		"  person@example.com  ", // surrounding whitespace is trimmed
	} {
		if err := Validate(address); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", address, err)
		}
	}
}

func TestValidateRejectsUnusableAddresses(t *testing.T) {
	for name, address := range map[string]string{
		"empty":            "",
		"whitespace only":  "   ",
		"no at sign":       "person.example.com",
		"no local part":    "@example.com",
		"no domain":        "person@",
		"bare hostname":    "person@localhost",
		"trailing dot":     "person@example.",
		"leading dot":      "person@.com",
		"internal space":   "person name@example.com",
		"two at signs":     "a@b@example.com",
		"display name":     "Someone <person@example.com>",
		"angle brackets":   "<person@example.com>",
		"comma separated":  "a@example.com, b@example.com",
		"newline injected": "person@example.com\nBcc: victim@example.com",
		"too long":         strings.Repeat("a", 250) + "@example.com",
	} {
		if err := Validate(address); err == nil {
			t.Errorf("%s: Validate(%q) = nil, want an error", name, address)
		}
	}
}

// The message reaches the person who typed the address, so it should read as
// instruction rather than as a parser complaint.
func TestErrInvalidReadsAsUserFacingText(t *testing.T) {
	if got := ErrInvalid.Error(); got != "please enter a valid email address" {
		t.Errorf("ErrInvalid = %q", got)
	}
}
