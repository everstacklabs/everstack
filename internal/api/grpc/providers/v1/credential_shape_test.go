package v1

import (
	"strings"
	"testing"
)

// The observed failure: a dev instance stored a date string as its OpenAI key
// on 2026-05-24, every request failed upstream for months, and the platform
// reported isConfigured/isActive true throughout.
func TestRejectsTheCredentialThatWasActuallyStored(t *testing.T) {
	err := ValidateAPIKeyShape("openai", "2026-05-24", false)
	if err == nil {
		t.Fatal("a date string was accepted as an OpenAI API key")
	}
}

// Rejecting a valid key is worse than accepting an invalid one, because the
// user cannot work around us. Each case here is a credential that must survive.
func TestAcceptsCredentialsItCannotBeSureAbout(t *testing.T) {
	cases := []struct {
		name          string
		provider, key string
		customBaseURL bool
	}{
		{"openai standard", "openai", "sk-abc123def456", false},
		{"openai project key", "openai", "sk-proj-abc123def456", false},
		{"anthropic", "anthropic", "sk-ant-api03-abc123", false},
		{"groq", "groq", "gsk_abc123", false},
		{"openrouter", "openrouter", "sk-or-v1-abc123", false},
		{"provider with no documented format", "cohere", "anything-at-all", false},
		{"provider with no documented format 2", "mistral", "xyz789", false},
		{"together", "together", "whatever", false},
		{"ollama needs no key", "ollama", "n/a", false},
		{"google via vertex", "google", "service-account-ish", false},
		{"unknown provider entirely", "some-new-vendor", "zzz", false},
		{"custom base url disables the check", "openai", "proxy-issued-token", true},
		{"custom base url, anthropic proxy", "anthropic", "internal-token-42", true},
		{"empty key is someone else's problem", "openai", "", false},
		{"whitespace-only key", "openai", "   ", false},
		{"case-insensitive provider name", "OpenAI", "sk-abc123", false},
	}

	for _, c := range cases {
		if err := ValidateAPIKeyShape(c.provider, c.key, c.customBaseURL); err != nil {
			t.Errorf("%s: rejected a credential it should have allowed: %v", c.name, err)
		}
	}
}

func TestRejectsObviouslyWrongCredentials(t *testing.T) {
	cases := []struct{ name, provider, key string }{
		{"date pasted as openai key", "openai", "2026-05-24"},
		{"anthropic key in the openai field", "openai", "not-a-key-at-all"},
		{"openai key in the anthropic field", "anthropic", "sk-abc123"},
		{"groq missing its prefix", "groq", "abc123"},
		{"a URL pasted by mistake", "openai", "https://api.openai.com/v1"},
		{"an email pasted by mistake", "openai", "someone@example.com"},
	}

	for _, c := range cases {
		if err := ValidateAPIKeyShape(c.provider, c.key, false); err == nil {
			t.Errorf("%s: accepted %q", c.name, c.key)
		}
	}
}

// The ticket notes the gateway already echoes provider-key fragments back to
// callers. This error reaches the caller too, so it must not add another one.
func TestErrorNeverEchoesTheCredential(t *testing.T) {
	secret := "totally-wrong-but-secret-value-9f3a"

	err := ValidateAPIKeyShape("openai", secret, false)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed the supplied credential: %s", err)
	}
	// It should still be actionable.
	if !strings.Contains(err.Error(), "sk-") {
		t.Errorf("error does not tell the user the expected shape: %s", err)
	}
}

// The most common real-world mistake: a newline picked up when copying from a
// terminal or a provider dashboard. Stored verbatim today, rejected upstream,
// indistinguishable from a good key in the UI.
func TestNormalizeStripsPasteArtefacts(t *testing.T) {
	for _, raw := range []string{
		"sk-abc123\n",
		"  sk-abc123",
		"sk-abc123\r\n",
		"\tsk-abc123\t",
		"\n sk-abc123 \n",
	} {
		if got := NormalizeAPIKey(raw); got != "sk-abc123" {
			t.Errorf("NormalizeAPIKey(%q) = %q, want %q", raw, got, "sk-abc123")
		}
	}
}

// A key that is valid apart from a trailing newline must pass validation once
// normalized, since that is the whole point of normalizing first.
func TestNormalizedKeyPassesValidation(t *testing.T) {
	raw := "sk-abc123\n"
	if err := ValidateAPIKeyShape("openai", raw, false); err != nil {
		t.Fatalf("a valid key with a trailing newline was rejected: %v", err)
	}
}
