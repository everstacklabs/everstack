// Package region centralises the region-aware host composition used by
// the gateway, SSH proxy, and tenant routing. Every gateway pod reads
// EVS_REGION (e.g. "eu-fra-1") + EVS_ENV (e.g. "dev"; empty for prod)
// at boot and composes effective hostnames so customers get URLs like:
//
//	*.eu-fra-1.evs.run            (prod sandbox previews)
//	*.dev.eu-fra-1.evs.run        (dev sandbox previews)
//	ssh.eu-fra-1.evs.run          (prod SSH proxy)
//	*.eu-fra-1.everstack.ai       (prod tenant control plane)
//
// DNS / k8s split routing per region happens upstream — each pod only
// needs to know its OWN region to print the right URLs.
package region

import (
	"os"
	"strings"
)

// Resolved holds the region/env pair for the current pod.
type Resolved struct {
	// Region is the region slug, e.g. "eu-fra-1". Empty means
	// region-unaware (single-region or local dev) — composition then
	// falls back to the bare base domain.
	Region string

	// Env is the environment slug. Empty/"prod" means production and
	// is omitted from composed hostnames; any other value (typically
	// "dev", "staging") is included as a leading label.
	Env string
}

// FromEnv reads EVS_REGION + EVS_ENV. Both are optional.
func FromEnv() Resolved {
	return Resolved{
		Region: strings.TrimSpace(os.Getenv("EVS_REGION")),
		Env:    normaliseEnv(os.Getenv("EVS_ENV")),
	}
}

// Compose returns the composed FQDN for the given base domain, e.g.
//
//	Compose("evs.run")           = "eu-fra-1.evs.run"             (prod)
//	Compose("evs.run")           = "dev.eu-fra-1.evs.run"         (dev)
//	Compose("evs.run")           = "evs.run"                      (no region)
//	ComposeHost("ssh", "evs.run") = "ssh.eu-fra-1.evs.run"        (prod)
//	ComposeHost("ssh", "evs.run") = "ssh.dev.eu-fra-1.evs.run"    (dev)
//
// Pass an empty subdomain prefix when composing the wildcard base used
// for *.<base>; pass a fixed prefix (e.g. "ssh") for fixed hostnames.
func (r Resolved) Compose(base string) string {
	return r.ComposeHost("", base)
}

// ComposeHost prepends `prefix` to the region/env-composed base domain.
// Use Compose for the bare wildcard base.
func (r Resolved) ComposeHost(prefix, base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	parts := []string{}
	if prefix = strings.TrimSpace(prefix); prefix != "" {
		parts = append(parts, prefix)
	}
	if r.Env != "" {
		parts = append(parts, r.Env)
	}
	if r.Region != "" {
		parts = append(parts, r.Region)
	}
	parts = append(parts, base)
	return strings.Join(parts, ".")
}

// normaliseEnv collapses the "prod" / "production" / "" trio into "" so
// downstream callers don't have to remember to strip them.
func normaliseEnv(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" || v == "prod" || v == "production" {
		return ""
	}
	return v
}
