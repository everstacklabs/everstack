// Package orgident separates the two ways a request can name an
// organization: by id or by slug.
//
// A slug is chosen by the org's owner, so a slug that reads as another org's
// id would let "id OR slug" lookups resolve to the wrong org. Two rules close
// that:
//
//   - new slugs never look like a UUID in any form Go or Postgres accepts
//     (ValidateSlug);
//   - a lookup decides up front which kind of identifier it holds and
//     matches on that column only (Parse).
package orgident

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// slugPattern is the allowed shape for organization slugs: lowercase
// alphanumerics with interior hyphens, 2-63 characters. It matches
// internal/hosting.SlugPattern (not imported here to keep this package
// dependency-free).
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)

// ErrInvalidSlug rejects a slug on create or update.
var ErrInvalidSlug = errors.New("invalid organization slug")

// ValidateSlug checks a slug about to be written. Existing rows are not
// re-validated: reads keep working for slugs created before this rule.
func ValidateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("%w: use 2-63 lowercase letters, digits and hyphens, not starting or ending with a hyphen", ErrInvalidSlug)
	}
	if looksLikeUUID(slug) {
		return fmt.Errorf("%w: a slug cannot look like an id", ErrInvalidSlug)
	}
	return nil
}

// looksLikeUUID reports whether s is a UUID in any form uuid.Parse accepts
// (canonical, hyphenless, braced, urn:uuid:, any case) or in any form
// Postgres casts to uuid (32 hex digits with hyphens anywhere between
// groups).
func looksLikeUUID(s string) bool {
	if _, err := uuid.Parse(s); err == nil {
		return true
	}
	hex := strings.Trim(strings.ReplaceAll(s, "-", ""), "{}")
	if len(hex) != 32 {
		return false
	}
	for _, c := range strings.ToLower(hex) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Parse classifies an identifier. When it parses as a UUID it returns the
// canonical id and true: the caller must look it up by id only. Otherwise
// it returns the trimmed input and false: look it up by slug only, as exact
// text. There is no third case and never a combined match.
func Parse(identifier string) (string, bool) {
	s := strings.TrimSpace(identifier)
	if u, err := uuid.Parse(s); err == nil {
		return u.String(), true
	}
	return s, false
}

// FitSlug adapts a slug derived from an org name so that it, and the
// "-N" suffixes added for uniqueness, pass ValidateSlug: it caps the length
// and prefixes "org-" to a base that is too short or reads as an id.
func FitSlug(base string) string {
	base = strings.Trim(base, "-")
	if len(base) > 50 {
		base = strings.TrimRight(base[:50], "-")
	}
	if len(base) < 2 || looksLikeUUID(base) {
		base = strings.TrimRight("org-"+base, "-")
	}
	return base
}
