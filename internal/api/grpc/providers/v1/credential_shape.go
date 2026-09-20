package v1

import (
	"fmt"
	"strings"
)

// An invalid provider credential is accepted, stored, and reported as Active.
// On one dev instance the stored OpenAI key was literally a date string, and
// had been failing every request upstream since 2026-05-24 while the platform
// showed isConfigured/isActive true. Nothing surfaced it on either side; the
// only symptom was a 500 on the customer's first request.
//
// The real fix is a live authenticated call on save plus a provider-health
// signal (EVS-245). This is the cheap half: catch the credentials that are
// obviously wrong before they are stored, which covers the mistakes that
// actually happen -- a trailing newline from a paste, a truncated copy, a value
// pasted into the wrong field.
//
// It is deliberately conservative. Rejecting a VALID key is worse than
// accepting an invalid one, because the user cannot work around us, so this
// only refuses credentials that cannot be right.

// knownKeyPrefixes lists providers whose key format is stable and publicly
// documented. A provider absent from this map is never shape-checked.
//
// Providers are omitted on purpose where the format varies or is undocumented
// (cohere, mistral, together), or where authentication is not a bearer key at
// all (ollama, and Google via Vertex service accounts). Guessing a format for
// those would reject working credentials.
var knownKeyPrefixes = map[string][]string{
	"openai":     {"sk-"},
	"anthropic":  {"sk-ant-"},
	"groq":       {"gsk_"},
	"xai":        {"xai-"},
	"openrouter": {"sk-or-"},
	"perplexity": {"pplx-"},
	"fireworks":  {"fw_"},
	"deepseek":   {"sk-"},
}

// NormalizeAPIKey trims surrounding whitespace from a pasted credential.
//
// This alone fixes the single most common failure: a trailing newline picked up
// when copying from a terminal or a provider dashboard. Such a key is stored
// verbatim today, is rejected upstream, and looks identical to a good one in
// the UI.
func NormalizeAPIKey(key string) string {
	return strings.TrimSpace(key)
}

// ValidateAPIKeyShape reports whether a credential is obviously not a key for
// this provider. It returns nil whenever it cannot be sure.
//
// hasCustomBaseURL disables the check entirely: a custom base URL means a
// proxy, a gateway, a self-hosted deployment or an enterprise endpoint, and
// those legitimately use credential formats that differ from the vendor's.
// Enforcing a vendor prefix there would break exactly the advanced setups least
// able to tolerate it.
func ValidateAPIKeyShape(providerName, key string, hasCustomBaseURL bool) error {
	key = NormalizeAPIKey(key)
	if key == "" {
		return nil // absent credentials are a separate concern, handled by the caller
	}
	if hasCustomBaseURL {
		return nil
	}

	prefixes, known := knownKeyPrefixes[strings.ToLower(strings.TrimSpace(providerName))]
	if !known {
		return nil
	}

	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return nil
		}
	}

	// Name the expected shape but never echo the supplied value: this error
	// reaches the caller, and provider errors have already been observed
	// echoing key fragments back to tenants.
	return fmt.Errorf(
		"the %s API key does not look right: expected it to start with %q. "+
			"Check for a truncated copy or a stray newline, or set a custom base URL if you are using a proxy",
		providerName, strings.Join(prefixes, "\" or \""),
	)
}
