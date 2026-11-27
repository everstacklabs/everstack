package apikey

import "strings"

// Fixed prefixes of the API keys this platform issues. Validation never
// depends on them (a key is checked by its full-string hash), but the
// OpenAI-compatible /v1 surface matches on them to tell an issued key sent as
// `Authorization: Bearer` apart from a device-auth JWT or a session token
// arriving on the same header.
//
// Every issuer must build its keys from one of these constants, so a new key
// shape cannot silently stop working as a Bearer.
const (
	// PlatformKeyPrefix starts every key minted by ApiKeyService.CreateApiKey
	// (sk-mf-svcacct-api01-..., sk-mf-usracct-api01-...). "mf" is the
	// pre-rebrand name; it stays because every key already issued carries it.
	PlatformKeyPrefix = "sk-mf-"

	// HostingKeyPrefix starts every key minted for evs.run hosting
	// (evs_live_...).
	HostingKeyPrefix = "evs_live_"
)

var issuedKeyPrefixes = [...]string{PlatformKeyPrefix, HostingKeyPrefix}

// HasIssuedKeyPrefix reports whether token has the shape of a key this
// platform issued. It is a shape check only: the key still has to be
// validated against the key store.
func HasIssuedKeyPrefix(token string) bool {
	for _, p := range issuedKeyPrefixes {
		if strings.HasPrefix(token, p) && len(token) > len(p) {
			return true
		}
	}
	return false
}
