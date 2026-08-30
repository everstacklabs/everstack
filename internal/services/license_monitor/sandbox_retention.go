package license_monitor

import (
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// SandboxRetentionAdapter implements sandbox.RetentionResolver by looking up
// the current plan tier from the license monitor and returning the
// corresponding idle retention duration.
type SandboxRetentionAdapter struct {
	monitor *Monitor
}

// NewSandboxRetentionAdapter creates a new adapter backed by the given monitor.
func NewSandboxRetentionAdapter(monitor *Monitor) *SandboxRetentionAdapter {
	return &SandboxRetentionAdapter{monitor: monitor}
}

// ResolveIdleRetention returns the idle retention duration for the tenant's
// current plan tier. A return value of 0 means no expiration (pro/enterprise).
// Falls back to the "free" tier default if the tier is unknown or the license
// state is unavailable. Returns -1 to signal the caller should use the global
// default (only when the tier is not in the map).
func (a *SandboxRetentionAdapter) ResolveIdleRetention(_ string) time.Duration {
	state := a.monitor.GetLicenseState()
	if state == nil {
		return sandbox.DefaultRetentionMap["free"]
	}
	tier := state.Tier
	if d, ok := sandbox.DefaultRetentionMap[tier]; ok {
		return d
	}
	return sandbox.DefaultRetentionMap["free"]
}

// ResolveStopRetention returns the stopped-sandbox retention for the current
// plan tier. Falls back to "free" defaults when state/tier is unavailable.
func (a *SandboxRetentionAdapter) ResolveStopRetention(_ string) time.Duration {
	state := a.monitor.GetLicenseState()
	if state == nil {
		return sandbox.ResolveStopRetention("free")
	}
	return sandbox.ResolveStopRetention(state.Tier)
}

// ============================================================================
// Persistent Agent Limit Adapter (formerly Trooper Limit Adapter)
// ============================================================================

// TrooperLimitAdapter implements sandbox.TrooperLimitResolver by looking up
// the current plan tier from the license monitor. It resolves limits for both
// legacy troopers and the unified persistent agents.
type TrooperLimitAdapter struct {
	monitor *Monitor
}

// NewTrooperLimitAdapter creates a new adapter backed by the given monitor.
func NewTrooperLimitAdapter(monitor *Monitor) *TrooperLimitAdapter {
	return &TrooperLimitAdapter{monitor: monitor}
}

// ResolveMaxTroopers returns the maximum number of persistent agents for
// the tenant's plan tier. Returns -1 for unlimited.
func (a *TrooperLimitAdapter) ResolveMaxTroopers(_ string) int {
	tier := a.resolveTier()
	if limit, ok := sandbox.DefaultPersistentAgentLimitMap[tier]; ok {
		return limit
	}
	return sandbox.DefaultPersistentAgentLimitMap["free"]
}

// ResolvePlanTier returns the current plan tier string.
func (a *TrooperLimitAdapter) ResolvePlanTier(_ string) string {
	return a.resolveTier()
}

// IsTrooperFeatureEnabled returns true if the license includes either the
// persistent_agents or the legacy persistent_troopers feature flag.
func (a *TrooperLimitAdapter) IsTrooperFeatureEnabled(_ string) bool {
	state := a.monitor.GetLicenseState()
	if state == nil {
		// No state yet (startup, before the first monitor tick): the free
		// plan includes persistent_agents, so CE entitlements say yes.
		// Returning false here would stop stopped troopers from being
		// recreated on unlicensed instances (editions-and-billing.md).
		return true
	}
	a.monitor.mu.RLock()
	defer a.monitor.mu.RUnlock()
	// Check unified persistent_agents feature first
	if _, ok := a.monitor.availableFeatures["persistent_agents"]; ok {
		return true
	}
	if fs, ok := a.monitor.features["persistent_agents"]; ok && fs.Enabled {
		return true
	}
	// Fallback to legacy persistent_troopers
	if _, ok := a.monitor.availableFeatures["persistent_troopers"]; ok {
		return true
	}
	if fs, ok := a.monitor.features["persistent_troopers"]; ok {
		return fs.Enabled
	}
	return false
}

// IsBrowserHeadedEnabled returns true if the license includes the browser_headed
// feature flag. This is a development feature gated behind per-tenant override.
func (a *TrooperLimitAdapter) IsBrowserHeadedEnabled(_ string) bool {
	state := a.monitor.GetLicenseState()
	if state == nil {
		return false
	}
	a.monitor.mu.RLock()
	defer a.monitor.mu.RUnlock()
	if _, ok := a.monitor.availableFeatures["browser_headed"]; ok {
		return true
	}
	if fs, ok := a.monitor.features["browser_headed"]; ok {
		return fs.Enabled
	}
	return false
}

func (a *TrooperLimitAdapter) resolveTier() string {
	state := a.monitor.GetLicenseState()
	if state == nil || state.Tier == "" {
		return "free"
	}
	return state.Tier
}
