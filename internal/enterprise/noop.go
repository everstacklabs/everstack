package enterprise

import (
	"context"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/m2m"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	licpkg "github.com/everstacklabs/everstack/internal/license"
)

// Package-level singletons used by the context helpers as safe fallbacks.
var (
	noopEnforcer LicenseEnforcer = noopLicenseEnforcer{}
	noopMonitor  LicenseMonitor  = noopLicenseMonitor{}
	noopInstMgr  InstanceManager = noopInstanceManager{}
)

// ---------------------------------------------------------------------------
// noopLicenseEnforcer
// ---------------------------------------------------------------------------

type noopLicenseEnforcer struct{}

var _ LicenseEnforcer = noopLicenseEnforcer{}

func (noopLicenseEnforcer) IsEnabled() bool          { return false }
func (noopLicenseEnforcer) GetCached() *LicenseState { return nil }
func (noopLicenseEnforcer) IsInTrialMode() bool      { return false }
func (noopLicenseEnforcer) GetTrialManager() any     { return nil }
func (noopLicenseEnforcer) IsKeyPinned() bool        { return false }

func (noopLicenseEnforcer) RecordTrialTokens(context.Context, int64) error { return nil }

func (noopLicenseEnforcer) Start(<-chan struct{})                                 {}
func (noopLicenseEnforcer) RefreshNow()                                           {}
func (noopLicenseEnforcer) WithLicenseEnforcement(next http.Handler) http.Handler { return next }

func (noopLicenseEnforcer) SetCachedState(*LicenseState)        {}
func (noopLicenseEnforcer) SetCachedJWT(string, *licpkg.Claims) {}

func (noopLicenseEnforcer) SetLicenseServiceURL(string)                          {}
func (noopLicenseEnforcer) SetDeviceFingerprint(string)                          {}
func (noopLicenseEnforcer) SetM2MProvider(m2m.TokenProvider)                     {}
func (noopLicenseEnforcer) SetVerifier(*licpkg.Verifier)                         {}
func (noopLicenseEnforcer) SetDB(*sqlx.DB)                                       {}
func (noopLicenseEnforcer) SetLicenseFile(string)                                {}
func (noopLicenseEnforcer) SetEnabled(bool)                                      {}
func (noopLicenseEnforcer) SetDryRun(bool)                                       {}
func (noopLicenseEnforcer) SetCacheTTL(time.Duration)                            {}
func (noopLicenseEnforcer) SetTrialManager(any)                                  {}
func (noopLicenseEnforcer) SetFeaturesCallback(FeaturesCallback)                 {}
func (noopLicenseEnforcer) SetSpendLimitConfigCallback(SpendLimitConfigCallback) {}

// ---------------------------------------------------------------------------
// noopLicenseMonitor — implements PersistentMonitor (superset of LicenseMonitor)
// ---------------------------------------------------------------------------

type noopLicenseMonitor struct{}

var _ PersistentMonitor = noopLicenseMonitor{}

func (noopLicenseMonitor) Start(context.Context) {}
func (noopLicenseMonitor) Stop()                 {}
func (noopLicenseMonitor) Refresh()              {}

func (noopLicenseMonitor) SetOrganizationAndInstanceID(string, string) {}
func (noopLicenseMonitor) GetOrganizationID() string                   { return "" }
func (noopLicenseMonitor) GetInstanceID() string                       { return "" }

func (noopLicenseMonitor) SetM2MCredentials([]byte)                        {}
func (noopLicenseMonitor) SetRedisClient(*cache.RedisClient)               {}
func (noopLicenseMonitor) SetAvailableFeatures(map[string]*FeatureRelease) {}
func (noopLicenseMonitor) SetSpendLimitConfig(float64, string, bool)       {}

func (noopLicenseMonitor) RecordRequest() error                          { return nil }
func (noopLicenseMonitor) RecordRequestWithMetrics(RequestMetrics) error { return nil }

func (noopLicenseMonitor) IsLocked() (bool, string)                         { return false, "" }
func (noopLicenseMonitor) IsSpendBlocked() (bool, string)                   { return false, "" }
func (noopLicenseMonitor) IsFeatureEnabled(string) (bool, string)           { return true, "" }
func (noopLicenseMonitor) GetUsageStats() UsageStats                        { return UsageStats{} }
func (noopLicenseMonitor) GetLicenseState() *LicenseState                   { return nil }
func (noopLicenseMonitor) GetAvailableFeatures() map[string]*FeatureRelease { return nil }
func (noopLicenseMonitor) CheckSpendLimitBeforeRequest(float64) (bool, string) {
	return true, ""
}
func (noopLicenseMonitor) GetSpendLimitStatus() (float64, float64, float64, bool) {
	return 0, 0, 0, false
}

func (noopLicenseMonitor) Subscribe(func(LicenseState)) {}

// PersistentMonitor extensions
func (noopLicenseMonitor) InitUsageSyncer(string)                         {}
func (noopLicenseMonitor) SetSyncerCredentials(string, string, []byte)    {}
func (noopLicenseMonitor) SetSyncerM2MConfig(*m2m.Config)                 {}
func (noopLicenseMonitor) SetSyncerCountsProvider(ResourceCountsProvider) {}
func (noopLicenseMonitor) SetLimitsUpdateCallback(LimitsUpdateCallback)   {}

// ---------------------------------------------------------------------------
// noopInstanceManager
// ---------------------------------------------------------------------------

type noopInstanceManager struct{}

var _ InstanceManager = noopInstanceManager{}

func (noopInstanceManager) Start(context.Context) {}
func (noopInstanceManager) Stop()                 {}

func (noopInstanceManager) IsActivated(context.Context) (bool, error) { return false, nil }
func (noopInstanceManager) GetActiveInstance(context.Context) (*InstanceInfo, error) {
	return nil, nil
}
func (noopInstanceManager) GetInstanceId(context.Context) (string, bool, error) {
	return "", false, nil
}
func (noopInstanceManager) GetLocalInstanceId(context.Context) (string, error) { return "", nil }
func (noopInstanceManager) EnsureLocalInstanceId(context.Context) (string, error) {
	return "", nil
}
func (noopInstanceManager) EnsureM2MSigningKey(context.Context) ([]byte, error) { return nil, nil }

func (noopInstanceManager) StoreActivation(context.Context, string, string, string, string, any, ...StoreActivationOpts) error {
	return nil
}

func (noopInstanceManager) CheckInstanceDataOnStartup(context.Context) (*DeviceFingerprintStatus, error) {
	return nil, nil
}
func (noopInstanceManager) GetConfig() InstanceConfig         { return InstanceConfig{} }
func (noopInstanceManager) SetCommandBus(commands.CommandBus) {}
