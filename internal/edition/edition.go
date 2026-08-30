// Package edition exposes the build-time product edition. It is a leaf
// package (no imports) so that any layer — including packages that
// internal/enterprise itself depends on, like internal/sandbox — can consult
// the edition without creating an import cycle.
//
// The edition is fixed at compile time by build tags:
//   - "ce"  — Community Edition (DEFAULT, no tags): CE limits + feature gates
//   - "ee"  — Enterprise Edition (-tags enterprise): license enforcement
//   - "dev" — development (-tags dev): everything unlocked, never shipped
//
// There is deliberately no runtime override (env var or config): an override
// that unlocks a shipped binary is a licensing backdoor. See
// docs/design/editions-and-billing.md, decisions D3 and D8.
package edition

var current = "ce"

// Current returns the build edition: "ce", "ee", or "dev".
func Current() string { return current }

// IsDev returns true only when the binary was built with -tags dev.
func IsDev() bool { return current == "dev" }
