package sandbox

import "strings"

const (
	FreeConcurrentSandboxLimit  = 10
	BasicConcurrentSandboxLimit = 50
	ProConcurrentSandboxLimit   = 50
	UnlimitedSandboxLimit       = -1
)

// ResolveConcurrentSandboxLimit returns the customer-facing number of
// concurrently allocated sandboxes for a single Everstack instance. This is
// deliberately separate from GlobalSandboxConfig.MaxSandboxes, which is a
// hidden process/host safety ceiling and must never be presented as a plan
// entitlement.
func ResolveConcurrentSandboxLimit(tier string) int {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "basic":
		return BasicConcurrentSandboxLimit
	case "pro":
		return ProConcurrentSandboxLimit
	case "enterprise":
		return UnlimitedSandboxLimit
	default:
		return FreeConcurrentSandboxLimit
	}
}
