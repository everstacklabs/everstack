package sandbox

import (
	"errors"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
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

	normalizedTier, maxMemory := managedMemoryCap(tier)
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

// NearestManagedMachineProfile returns the largest managed profile that uses
// no more CPU and no more memory than config, falling back to the smallest
// profile when config is below every one. It rounds down wherever a profile
// fits. Below nano it has to size up, so normalizeStoredAgentSize records a
// billing ceiling (InstanceConfig.Metered) that keeps each dimension billed
// at no more than the stored configuration. Disk is always the profile's
// disk, because managed profiles fix it.
func NearestManagedMachineProfile(config SandboxConfig) MachineProfile {
	nearest := ManagedMachineProfiles[0]
	for _, profile := range ManagedMachineProfiles[1:] {
		if profile.CPU <= config.CPULimit && profile.MemoryMB <= config.MemoryMB {
			nearest = profile
		}
	}
	return nearest
}

// NormalizeStoredMachineProfile maps a persisted sandbox size that predates
// managed machine profiles onto the nearest profile, so agents created with
// legacy sizes (for example 1 vCPU / 512 MB / 1 GB) can still be provisioned
// on managed runtimes instead of failing with ErrUnsupportedSandboxSize on
// every attempt. It is for sizes read back from storage only; explicit API
// requests must keep going through ValidateSandboxMachineProfile so callers
// learn their size is unsupported. Self-hosted runtimes accept custom sizes,
// so the config is returned unchanged there, as it is when unset (all zero)
// or already a profile. The tier check still runs afterwards, so a stored
// size above the plan's limit is reported rather than silently shrunk.
func (m *SandboxManager) NormalizeStoredMachineProfile(config SandboxConfig) (SandboxConfig, bool) {
	if m == nil || !m.managedMachineProfiles.Load() {
		return config, false
	}
	if config.CPULimit == 0 && config.MemoryMB == 0 && config.DiskMB == 0 {
		return config, false
	}
	if _, ok := MatchManagedMachineProfile(config); ok {
		return config, false
	}
	profile := NearestManagedMachineProfile(config)
	config.CPULimit = profile.CPU
	config.MemoryMB = profile.MemoryMB
	config.DiskMB = profile.DiskMB
	return config, true
}

// normalizeStoredAgentSize maps a stored agent size onto a managed profile
// (see NormalizeStoredMachineProfile) and returns the billing ceiling to
// record with it. Normalization rounds down where it can, but a size below
// nano is provisioned as nano; the ceiling then keeps each dimension billed
// at no more than what was configured. It is nil when nothing was raised.
func (m *SandboxManager) normalizeStoredAgentSize(config SandboxConfig) (SandboxConfig, *MeteredResources, bool) {
	normalized, changed := m.NormalizeStoredMachineProfile(config)
	if !changed {
		return config, nil, false
	}
	var metered MeteredResources
	raised := false
	if config.CPULimit > 0 && config.CPULimit < normalized.CPULimit {
		metered.CPULimit = config.CPULimit
		raised = true
	}
	if config.MemoryMB > 0 && config.MemoryMB < normalized.MemoryMB {
		metered.MemoryMB = config.MemoryMB
		raised = true
	}
	if config.DiskMB > 0 && config.DiskMB < normalized.DiskMB {
		metered.DiskMB = config.DiskMB
		raised = true
	}
	if !raised {
		return normalized, nil, true
	}
	return normalized, &metered, true
}

func managedMemoryCap(tier string) (string, int64) {
	normalizedTier := strings.ToLower(strings.TrimSpace(tier))
	maxMemory, ok := maxManagedMemoryByTier[normalizedTier]
	if !ok {
		maxMemory = maxManagedMemoryByTier["free"]
	}
	return normalizedTier, maxMemory
}

// maxManagedProfileForTier is the largest profile the plan allows.
func maxManagedProfileForTier(tier string) (string, MachineProfile) {
	normalizedTier, maxMemory := managedMemoryCap(tier)
	best := ManagedMachineProfiles[0]
	for _, profile := range ManagedMachineProfiles[1:] {
		if profile.MemoryMB <= maxMemory {
			best = profile
		}
	}
	return normalizedTier, best
}

// FitExistingManagedMachineProfile is the policy for bringing back a sandbox
// that already exists (revive, restore, repo re-prepare) rather than
// allocating a new one. The saved size is kept only when it is within the
// org's current plan on every dimension. Otherwise CPU and memory are each
// clamped to the plan's largest profile (never raised, so the result is never
// above the plan or above what was saved). Disk is never shrunk, because
// resizing a snapshotted root disk breaks restore; a saved disk above the
// plan is refused instead. The result may be off-catalog (sandboxes created
// before managed profiles), which is fine: usage is metered per resource.
func FitExistingManagedMachineProfile(config SandboxConfig, tier string) (SandboxConfig, error) {
	normalizedTier, maxProfile := maxManagedProfileForTier(tier)
	if config.DiskMB > maxProfile.DiskMB {
		return config, fmt.Errorf(
			"%w: this sandbox's %d MB root disk is above the %s plan limit of %d MB and cannot be shrunk without losing its snapshot",
			ErrUnsupportedSandboxSize,
			config.DiskMB,
			normalizedTier,
			maxProfile.DiskMB,
		)
	}
	if config.CPULimit <= 0 || config.CPULimit > maxProfile.CPU {
		config.CPULimit = maxProfile.CPU
	}
	if config.MemoryMB <= 0 || config.MemoryMB > maxProfile.MemoryMB {
		config.MemoryMB = maxProfile.MemoryMB
	}
	return config, nil
}

// fitExistingInstanceMachineProfile applies FitExistingManagedMachineProfile
// to a persisted InstanceConfig and returns the config to provision with.
func (m *SandboxManager) fitExistingInstanceMachineProfile(config InstanceConfig) (InstanceConfig, error) {
	if m == nil || !m.managedMachineProfiles.Load() {
		return config, nil
	}
	fitted, err := FitExistingManagedMachineProfile(SandboxConfig{
		CPULimit: config.CPULimit,
		MemoryMB: config.MemoryMB,
		DiskMB:   config.DiskMB,
	}, m.resolveTenantTier(config.TenantID))
	if err != nil {
		return config, err
	}
	if fitted.CPULimit != config.CPULimit || fitted.MemoryMB != config.MemoryMB {
		logger.WithFields(
			"tenant_id", config.TenantID,
			"session_id", config.SessionID,
			"saved_cpu", config.CPULimit,
			"saved_memory_mb", config.MemoryMB,
			"cpu", fitted.CPULimit,
			"memory_mb", fitted.MemoryMB,
		).Warn("sandbox: saved size is above the current plan; reviving at the plan's limit")
		config.CPULimit = fitted.CPULimit
		config.MemoryMB = fitted.MemoryMB
	}
	return config, nil
}

// validateInstanceMachineProfile applies the strict managed-compute policy to
// a persisted InstanceConfig that is being allocated for the first time.
func (m *SandboxManager) validateInstanceMachineProfile(config InstanceConfig) error {
	return m.ValidateSandboxMachineProfile(SandboxConfig{
		CPULimit: config.CPULimit,
		MemoryMB: config.MemoryMB,
		DiskMB:   config.DiskMB,
	}, config.TenantID)
}
