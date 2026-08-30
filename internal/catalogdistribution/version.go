package catalogdistribution

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// IsNewerVersion prevents replay of an older, otherwise valid signed channel.
// Catalog rollbacks are released as a new higher version containing the prior
// known-good content.
func IsNewerVersion(current, candidate string) (bool, error) {
	comparison, err := CompareVersions(current, candidate)
	return comparison > 0, err
}

// CompareVersions compares a candidate release with the currently applied
// release. It returns a positive value for an upgrade, zero for the same
// semantic version, and a negative value for a downgrade.
func CompareVersions(current, candidate string) (int, error) {
	candidateVersion, err := canonicalSemanticVersion(candidate)
	if err != nil {
		return 0, fmt.Errorf("invalid candidate catalog version: %w", err)
	}
	current = strings.TrimSpace(current)
	if current == "" || current == "unknown" || current == "fallback" {
		return 1, nil
	}
	currentVersion, err := canonicalSemanticVersion(current)
	if err != nil {
		// Older gateways may have persisted non-semantic version labels. Allow
		// one migration to a valid signed release, then monotonic checks apply.
		return 1, nil
	}
	return semver.Compare(candidateVersion, currentVersion), nil
}

func validateSemanticVersion(version string) error {
	_, err := canonicalSemanticVersion(version)
	return err
}

func canonicalSemanticVersion(version string) (string, error) {
	version = strings.TrimSpace(version)
	canonical := version
	if !strings.HasPrefix(canonical, "v") {
		canonical = "v" + canonical
	}
	if !semver.IsValid(canonical) {
		return "", fmt.Errorf("catalog version %q is not semantic versioning", version)
	}
	return canonical, nil
}
