package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedSandboxSize is returned when managed Everstack compute does
// not match one of the public, priced machine profiles or exceeds the plan's
// maximum profile. Self-hosted runtimes do not enable this policy.
var ErrUnsupportedSandboxSize = errors.New("unsupported managed sandbox size")

// MachineProfile is a priced managed-compute resource tuple. Keep this list in
// lockstep with pkg/plans/plans.json sandbox_compute_pricing.
type MachineProfile struct {
	ID       string
	CPU      float64
	MemoryMB int64
	DiskMB   int64
}

var ManagedMachineProfiles = []MachineProfile{
	{ID: "nano", CPU: 0.5, MemoryMB: 512, DiskMB: 20 * 1024},
	{ID: "small", CPU: 1, MemoryMB: 1024, DiskMB: 20 * 1024},
	{ID: "medium", CPU: 2, MemoryMB: 2048, DiskMB: 20 * 1024},
	{ID: "large", CPU: 4, MemoryMB: 4096, DiskMB: 20 * 1024},
	{ID: "xlarge", CPU: 8, MemoryMB: 8192, DiskMB: 20 * 1024},
}

var maxManagedMemoryByTier = map[string]int64{
	"free":       512,
	"basic":      1024,
	"pro":        4096,
	"enterprise": 8192,
}

func MatchManagedMachineProfile(config SandboxConfig) (MachineProfile, bool) {
	for _, profile := range ManagedMachineProfiles {
		if config.CPULimit == profile.CPU &&
			config.MemoryMB == profile.MemoryMB &&
			config.DiskMB == profile.DiskMB {
			return profile, true
		}
	}
	return MachineProfile{}, false
}

func ValidateManagedMachineProfile(config SandboxConfig, tier string) error {
	profile, ok := MatchManagedMachineProfile(config)
	if !ok {
		return fmt.Errorf(
			"%w: choose nano, small, medium, large, or xlarge; custom CPU, memory, and root disk combinations are available only on self-hosted runtimes",
			ErrUnsupportedSandboxSize,
		)
	}

	normalizedTier := strings.ToLower(strings.TrimSpace(tier))
	maxMemory, ok := maxManagedMemoryByTier[normalizedTier]
	if !ok {
		maxMemory = maxManagedMemoryByTier["free"]
	}
	if profile.MemoryMB > maxMemory {
		return fmt.Errorf(
			"%w: %s is not available on the %s plan",
			ErrUnsupportedSandboxSize,
			profile.ID,
			normalizedTier,
		)
	}
	return nil
}

// validateInstanceMachineProfile applies the same managed-compute policy to a
// persisted InstanceConfig. Create requests are validated before persistence,
// but revive and internal reprovision paths load this shape directly from the
// database and must not bypass the priced fixed-size catalog.
func (m *SandboxManager) validateInstanceMachineProfile(config InstanceConfig) error {
	return m.ValidateSandboxMachineProfile(SandboxConfig{
		CPULimit: config.CPULimit,
		MemoryMB: config.MemoryMB,
		DiskMB:   config.DiskMB,
	}, config.TenantID)
}
