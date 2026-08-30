package sandbox

import "time"

// RetentionResolver resolves the idle retention duration for a given tenant.
// Implementations can look up the tenant's plan tier and return the
// corresponding retention. When no resolver is wired the manager falls back
// to GlobalSandboxConfig.DefaultIdleRetentionSecs.
type RetentionResolver interface {
	ResolveIdleRetention(tenantID string) time.Duration
}

// StopRetentionResolver resolves how long a stopped sandbox remains revivable.
// This is optional; when not provided, manager lifecycle logic falls back to
// tier defaults.
type StopRetentionResolver interface {
	ResolveStopRetention(tenantID string) time.Duration
}

// DefaultRetentionMap maps plan tiers to their idle retention durations.
// A value of 0 means no expiration (sandbox lives forever until manually destroyed).
var DefaultRetentionMap = map[string]time.Duration{
	"free":       1 * 24 * time.Hour, // 1 day
	"basic":      7 * 24 * time.Hour, // 7 days
	"pro":        0,                  // no expiration
	"enterprise": 0,                  // no expiration
}

// DefaultStopRetentionMap maps plan tiers to the duration a stopped sandbox
// remains revivable before being automatically terminated. Sourced from
// plans.json SESSION_RETENTION_DAYS per tier (enterprise -1 = no expiration).
// Applies to sandboxes at stop time only, so upgrading never retroactively
// sweeps existing history (editions-and-billing.md, grandfathering rule).
var DefaultStopRetentionMap = map[string]time.Duration{
	"free":       7 * 24 * time.Hour,  // SESSION_RETENTION_DAYS: 7
	"basic":      30 * 24 * time.Hour, // SESSION_RETENTION_DAYS: 30
	"pro":        90 * 24 * time.Hour, // SESSION_RETENTION_DAYS: 90
	"enterprise": 0,                   // SESSION_RETENTION_DAYS: -1 (no expiration)
}

// StaticRetentionResolver is a simple fallback that always returns the same
// duration regardless of tenant. Used when no license monitor is wired.
type StaticRetentionResolver struct {
	Duration time.Duration
}

func (s *StaticRetentionResolver) ResolveIdleRetention(_ string) time.Duration {
	return s.Duration
}

// ResolveStopRetention returns the revivable-until duration for a stopped sandbox.
func ResolveStopRetention(planTier string) time.Duration {
	if d, ok := DefaultStopRetentionMap[planTier]; ok {
		return d
	}
	return DefaultStopRetentionMap["free"]
}

// ============================================================================
// Persistent Trooper resolvers
// ============================================================================

// TrooperLimitResolver resolves the maximum number of persistent troopers
// a tenant is allowed to create based on their plan tier.
type TrooperLimitResolver interface {
	ResolveMaxTroopers(tenantID string) int
	// ResolvePlanTier returns the plan tier for a tenant (used for resource limits).
	ResolvePlanTier(tenantID string) string
	// IsTrooperFeatureEnabled returns true if the tenant's license includes
	// the persistent_troopers feature flag (cloud-only gate).
	IsTrooperFeatureEnabled(tenantID string) bool
	// IsBrowserHeadedEnabled returns true if the tenant's license includes
	// the browser_headed feature flag (non-headless browser with live streaming).
	IsBrowserHeadedEnabled(tenantID string) bool
}

// DefaultTrooperIdleMap maps plan tiers to the idle-to-sleep timeout for
// persistent troopers. A value of 0 means no auto-sleep (enterprise configurable).
var DefaultTrooperIdleMap = map[string]time.Duration{
	"free":       30 * time.Second,
	"basic":      2 * time.Minute,
	"pro":        5 * time.Minute,
	"enterprise": 0, // configurable, 0 = no auto-sleep
}

// DefaultPersistentAgentLimitMap maps plan tiers to the maximum number of
// persistent (always-on) agents allowed. -1 means unlimited.
var DefaultPersistentAgentLimitMap = map[string]int{
	"free":       1,
	"basic":      3,
	"pro":        10,
	"enterprise": -1,
}

// DefaultTrooperLimitMap is a backwards-compatible alias for DefaultPersistentAgentLimitMap.
// Deprecated: Use DefaultPersistentAgentLimitMap instead.
var DefaultTrooperLimitMap = DefaultPersistentAgentLimitMap

// DefaultTrooperResourceMap maps plan tiers to trooper resource limits:
// [0] = CPU cores, [1] = Memory MB, [2] = Disk MB.
// Memory follows plans.json SANDBOX_MEMORY_MB per tier (enterprise is -1 =
// unlimited there; the 8192 here is the concrete default allocation, not a
// cap). CPU and disk have no plans.json equivalent yet (Phase 1c of
// docs/design/editions-and-billing.md).
var DefaultTrooperResourceMap = map[string][3]float64{
	"free":       {0.5, 512, 20480},
	"basic":      {1, 1024, 20480},
	"pro":        {4, 4096, 20480},
	"enterprise": {8, 8192, 20480},
}

// ResolveTrooperIdleTimeout returns the idle-to-sleep timeout for persistent
// troopers on the given plan tier.
func ResolveTrooperIdleTimeout(planTier string) time.Duration {
	if d, ok := DefaultTrooperIdleMap[planTier]; ok {
		return d
	}
	return DefaultTrooperIdleMap["free"]
}

// ResolveTrooperResources returns (cpu, memMB, diskMB) limits for the tier.
func ResolveTrooperResources(planTier string) (cpu float64, memMB, diskMB int64) {
	r, ok := DefaultTrooperResourceMap[planTier]
	if !ok {
		r = DefaultTrooperResourceMap["free"]
	}
	return r[0], int64(r[1]), int64(r[2])
}
