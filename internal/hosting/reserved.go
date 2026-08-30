// Package hosting contains shared domain logic for the evs.run instant
// static hosting product (see docs/design/evs-run-hosting.md).
package hosting

import "regexp"

// SlugPattern is the allowed shape for site slugs: lowercase alphanumeric
// plus interior hyphens, 2-63 chars (DNS label constraints).
var SlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)

// reservedSlugs are hostnames on evs.run that must never be claimable as
// site slugs. Two categories:
//
//   - infra names that exist (or will exist) as real records on the zone;
//     keep in sync with infra/cloudflare/dns (var.regions and the static
//     record blocks)
//   - names whose squatting invites phishing or user confusion
var reservedSlugs = map[string]struct{}{
	// Region ids and env-prefixed variants (sync with infra/cloudflare/dns var.regions).
	"eu-gra-1": {},
	"eu-fra-1": {},
	"dev":      {},
	"staging":  {},

	// Infra / product hostnames.
	"ssh":     {},
	"api":     {},
	"get":     {},
	"docs":    {},
	"www":     {},
	"app":     {},
	"admin":   {},
	"assets":  {},
	"cdn":     {},
	"status":  {},
	"mail":    {},
	"smtp":    {},
	"blog":    {},
	"ops":     {},
	"grafana": {},
	"auth":    {},
	"billing": {},
	"license": {},
	"gateway": {},
	"cloud":   {},
	"console": {},
	"session": {},

	// Brand / phishing magnets.
	"everstack": {},
	"login":     {},
	"signin":    {},
	"signup":    {},
	"account":   {},
	"secure":    {},
	"support":   {},
	"help":      {},
	"payment":   {},
	"payments":  {},
	"wallet":    {},
	"verify":    {},
}

// IsReservedSlug reports whether slug may not be used for a hosted site.
// Callers must lowercase the slug first (SlugPattern enforces this anyway).
func IsReservedSlug(slug string) bool {
	_, ok := reservedSlugs[slug]
	return ok
}

// ValidSlug reports whether slug is well-formed and not reserved.
func ValidSlug(slug string) bool {
	return SlugPattern.MatchString(slug) && !IsReservedSlug(slug)
}
