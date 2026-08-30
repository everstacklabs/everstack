package enterprise

// CE (Community Edition) entitlement defaults.
//
// These mirror the FREE tier in pkg/plans/plans.json, except for the keys
// listed in ceDivergesFromFree, and apply whenever no plan data is available:
// CE builds, EE builds without an active license, and (fail-closed) licensed
// instances whose limit data is missing. A unit test (ce_defaults_test.go)
// pins both halves of that statement so neither the mirror nor the divergence
// set can drift; Phase 1 of docs/design/editions-and-billing.md replaces the
// mirror with a single generated source.

// CEUsageLimits is the effective CE entitlement set. -1 means unlimited,
// 0 means the resource is unavailable.
var CEUsageLimits = map[UsageType]int64{
	UsageTypeRPM:                 60,
	UsageTypeTokens:              1_000_000,
	UsageTypeRequests:            10_000,
	UsageTypeStorageBytes:        524_288_000,
	UsageTypeHostedSites:         3,
	UsageTypeHostingStorageBytes: 524_288_000,
	UsageTypeDatasetItems:        1_000,
	UsageTypeEvalRunsMonthly:     5,
	UsageTypeAnnotationQueues:    1,
	UsageTypeAgents:              3,
	UsageTypePersistentAgents:    1,
	UsageTypeConcurrentRunning:   1,
	UsageTypeConcurrentSandboxes: 10,
	UsageTypeConcurrentBrowsers:  2,
	UsageTypeBrowserSessionMax:   900,
	UsageTypeSandboxMemoryMB:     512,
	UsageTypeMessagesMonthly:     -1,
	UsageTypeChannels:            -1,
	UsageTypeChannelBindings:     -1,
	UsageTypeSpawnDepth:          1,
	UsageTypeSessionRetention:    7,
}

// ceDivergesFromFree records every CE limit that deliberately differs from the
// free cloud plan, and is the ONLY sanctioned way to differ: every key absent
// from this map must match plans.json exactly.
//
// CHANNELS / CHANNEL_BINDINGS: the free plan's channel ceiling exists because
// each live connector costs Everstack a goroutine and an open socket for as
// long as it is connected. That cost lands on whoever runs the gateway, so on
// a self-hosted instance it is the operator's to spend. Capping channels there
// would be a licensing gate wearing a capacity gate's clothes, and it is the
// most visible line against self-hostable competitors that cap governance
// features rather than the work itself.
// MESSAGES_MONTHLY: the hosted allowance meters traffic Everstack carries and
// bills for. A self-hosted channel message costs Everstack nothing, so a
// monthly ceiling there would only be a licence tripwire, and it would make
// the unlimited-channels grant above hollow: unlimited channels that fall
// silent after 1,000 messages are not unlimited.
var ceDivergesFromFree = map[UsageType]int64{
	UsageTypeChannels:        -1,
	UsageTypeChannelBindings: -1,
	UsageTypeMessagesMonthly: -1,
}

// ceFeatures is the free tier's feature flags. Keys absent from this registry
// are treated as ENABLED under CE: plans.json has not modeled them yet, and
// blocking unmodeled keys would take core product surface away from free
// users (see CEFeatureEnabled).
var ceFeatures = map[string]bool{
	"core_api":            true,
	"persistent_agents":   true,
	"persistent_troopers": true, // legacy alias of persistent_agents
	"channel_bindings":    true, // every plan connects channels; free caps the count
	"browser_headed":      false,
	"evaluations":         true, // free plan grants DATASET_ITEMS/EVAL_RUNS limits
	"alerts":              false,
	"sandbox_firecracker": true,
	"sandbox_kubernetes":  true, // backend choice is the operator's, not a paywall
	"memory_external":     false,
}

// CEFeatureEnabled reports whether a feature key is available under CE
// entitlements (no license). Registered keys follow the free plan's flags;
// unregistered keys default to enabled so an incomplete feature registry
// cannot brick free instances.
func CEFeatureEnabled(key string) bool {
	if enabled, ok := ceFeatures[key]; ok {
		return enabled
	}
	return true
}
