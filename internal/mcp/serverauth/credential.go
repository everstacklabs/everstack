package serverauth

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/everstacklabs/everstack/internal/api/common"
)

// ExtractPresentedKey parses all key transports in one place, so presence
// detection and authentication can never disagree about a request.
//
// Precedence matches what clients already rely on: an "Authorization: Bearer"
// token first, then the canonical API-key header, then the legacy aliases. An
// Authorization header with another scheme (a proxy's Basic auth, for example)
// is not an Everstack credential and is skipped. A Bearer header with no token,
// or a key header that is blank, falls through to the next header as before.
//
// What is new is the failure case: when no usable key is found but a Bearer
// scheme, a blank Authorization header or a key header was presented, or a
// credential header is repeated with different values, the request is reported
// as presenting a malformed credential. Callers must refuse it; it is never "no credential",
// so it can never reach an anonymous tenant fallback. Bearer is matched case
// insensitively and Unicode whitespace around it is ignored. A key itself may
// not contain whitespace, control characters or commas.
func ExtractPresentedKey(r *http.Request) (key string, presented, malformed bool) {
	if r == nil {
		return "", false, false
	}
	bad := false
	if auth, found, ambiguous := headerValue(r.Header, common.Authorization); ambiguous {
		return "", true, true
	} else if found {
		parts := strings.Fields(auth)
		switch {
		case len(parts) == 0:
			bad = true // a blank Authorization header is not a credential
		case strings.EqualFold(parts[0], "Bearer"):
			if len(parts) == 2 && validKey(parts[1]) {
				return parts[1], true, false
			}
			bad = true
		}
	}
	for _, name := range []string{common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey} {
		value, found, ambiguous := headerValue(r.Header, name)
		if ambiguous {
			return "", true, true
		}
		if !found {
			continue
		}
		if value = strings.TrimSpace(value); validKey(value) {
			return value, true, false
		}
		bad = true
	}
	if bad {
		return "", true, true
	}
	return "", false, false
}

// headerValue finds a header whatever its spelling. A header repeated with
// different values is ambiguous: there is no safe way to choose which
// credential the caller meant.
func headerValue(h http.Header, name string) (value string, found, ambiguous bool) {
	for header, entries := range h {
		if !strings.EqualFold(header, name) {
			continue
		}
		for _, entry := range entries {
			if !found {
				value, found = entry, true
			} else if entry != value {
				return "", true, true
			}
		}
	}
	return value, found, false
}

func validKey(key string) bool {
	if key == "" || strings.Contains(key, ",") {
		return false
	}
	return strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
