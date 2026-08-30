// Package enterprise defines the interfaces and value types for enterprise
// features (license enforcement, usage monitoring, instance management).
//
// CE (Community Edition) builds use no-op implementations; EE (Enterprise
// Edition) builds provide adapters over the real concrete types.  The build
// tag "enterprise" selects the variant — see wire_ce.go / wire_ee.go.
package enterprise

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/m2m"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/edition"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	licpkg "github.com/everstacklabs/everstack/internal/license"
)

// ---------------------------------------------------------------------------
// LicenseEnforcer — HTTP middleware + license state cache
// ---------------------------------------------------------------------------

// LicenseEnforcer gates HTTP requests based on the current license and
// provides cached license state to the rest of the gateway.
type LicenseEnforcer interface {
	// Query
	IsEnabled() bool
	GetCached() *LicenseState
	IsInTrialMode() bool
	GetTrialManager() any // Returns *trial.Manager in EE builds; nil in CE.
	// IsKeyPinned reports whether the license public key is pinned from config
	// (EVS_LICENSE_PUBLIC_KEY). When true, callers must not trust any
	// activation/DB-supplied public key (audit finding H2).
	IsKeyPinned() bool

	// Trial convenience — avoids consumers needing to import trial package.
	RecordTrialTokens(ctx context.Context, tokens int64) error

	// Lifecycle
	Start(done <-chan struct{})
	RefreshNow()
	WithLicenseEnforcement(next http.Handler) http.Handler

	// State mutation
	SetCachedState(state *LicenseState)
	SetCachedJWT(jwtString string, claims *licpkg.Claims)

	// Configuration (called during gateway startup)
	SetLicenseServiceURL(url string)
	SetDeviceFingerprint(fingerprint string)
	SetM2MProvider(provider m2m.TokenProvider)
	SetVerifier(v *licpkg.Verifier)
	SetDB(db *sqlx.DB)
	// SetLicenseFile configures an offline license file (air-gapped installs);
	// verified against the compiled-in vendor keyring only.
	SetLicenseFile(path string)
	SetEnabled(enabled bool)
	SetDryRun(dry bool)
	SetCacheTTL(ttl time.Duration)
	SetTrialManager(tm any) // Accepts *trial.Manager in EE builds.
	SetFeaturesCallback(callback FeaturesCallback)
	SetSpendLimitConfigCallback(callback SpendLimitConfigCallback)
}

// ---------------------------------------------------------------------------
// LicenseMonitor — usage tracking + feature gates
// ---------------------------------------------------------------------------

// LicenseMonitor tracks request usage, enforces feature gates, and manages
// spend limits.
type LicenseMonitor interface {
	// Lifecycle
	Start(ctx context.Context)
	Stop()
	Refresh()

	// Identity
	SetOrganizationAndInstanceID(orgID, instID string)
	GetOrganizationID() string
	GetInstanceID() string

	// Configuration
	SetM2MCredentials(signingKey []byte)
	SetRedisClient(client *cache.RedisClient)
	SetAvailableFeatures(features map[string]*FeatureRelease)
	SetSpendLimitConfig(amount float64, action string, enabled bool)

	// Recording
	RecordRequest() error
	RecordRequestWithMetrics(metrics RequestMetrics) error

	// Queries
	IsLocked() (locked bool, reason string)
	IsSpendBlocked() (blocked bool, reason string)
	IsFeatureEnabled(feature string) (enabled bool, reason string)
	GetUsageStats() UsageStats
	GetLicenseState() *LicenseState
	GetAvailableFeatures() map[string]*FeatureRelease
	CheckSpendLimitBeforeRequest(estimatedCost float64) (allowed bool, message string)
	GetSpendLimitStatus() (currentSpend, limitAmount, remaining float64, hasLimit bool)

	// Subscriptions
	Subscribe(callback func(LicenseState))
}

// ResourceCountsProvider returns current per-instance resource counts that
// the syncer can attach to each usage report.
type ResourceCountsProvider func(context.Context) ResourceCounts

// ResourceCounts mirrors the licensemonitor.ResourceCounts shape so callers
// of the enterprise interface don't need to import the license_monitor package.
type ResourceCounts struct {
	Agents                   int64
	PersistentAgents         int64
	ConcurrentRunning        int64
	DatasetItems             int64
	EvalRunsMonthly          int64
	AnnotationQueues         int64
	ChannelBindings          int64
	MessagesMonthly          int64
	StorageBytes             int64
	NetworkRxBytes           int64
	NetworkTxBytes           int64
	SandboxComputeSeconds    int64
	SandboxComputeCostMicros int64
}

// PersistentMonitor extends LicenseMonitor with storage-backed persistence
// and remote usage syncing.  Consumers that need these extra methods should
// type-assert: if pm, ok := monitor.(enterprise.PersistentMonitor); ok { ... }
type PersistentMonitor interface {
	LicenseMonitor

	InitUsageSyncer(licenseServiceURL string)
	SetSyncerCredentials(instanceID, refreshToken string, signingKey []byte)
	SetSyncerM2MConfig(cfg *m2m.Config)
	SetSyncerCountsProvider(fn ResourceCountsProvider)
	SetLimitsUpdateCallback(cb LimitsUpdateCallback)
}

// ---------------------------------------------------------------------------
// InstanceManager — activation + identity
// ---------------------------------------------------------------------------

// InstanceManager handles gateway instance identity, activation with the
// license service, and M2M key management.
type InstanceManager interface {
	// Lifecycle
	Start(ctx context.Context)
	Stop()

	// Identity
	IsActivated(ctx context.Context) (bool, error)
	GetActiveInstance(ctx context.Context) (*InstanceInfo, error)
	GetInstanceId(ctx context.Context) (id string, isActivated bool, err error)
	GetLocalInstanceId(ctx context.Context) (string, error)
	EnsureLocalInstanceId(ctx context.Context) (string, error)
	EnsureM2MSigningKey(ctx context.Context) ([]byte, error)

	// Activation
	StoreActivation(ctx context.Context, activationToken, instanceID, refreshToken, signingKey string, licenseState any, opts ...StoreActivationOpts) error

	// Startup checks
	CheckInstanceDataOnStartup(ctx context.Context) (*DeviceFingerprintStatus, error)
	GetConfig() InstanceConfig

	// Wiring
	SetCommandBus(bus commands.CommandBus)
}

// ---------------------------------------------------------------------------
// Edition
// ---------------------------------------------------------------------------

// Edition returns the current build edition ("ce", "ee", or "dev").
//   - "ce"   — Community Edition (default, no build tags): CE limits + feature gates
//   - "ee"   — Enterprise Edition (-tags=enterprise): cloud license enforcement
//   - "dev"  — development (-tags=dev): everything unlocked, never shipped
//
// The edition state lives in the leaf package internal/edition so that
// packages below enterprise in the import graph (e.g. internal/sandbox) can
// consult it without a cycle.
func Edition() string { return edition.Current() }

// IsDev returns true only when the binary was built with -tags dev.
// There is deliberately no runtime override: an env var that unlocks a shipped
// binary is a licensing backdoor (see docs/design/editions-and-billing.md, D8).
func IsDev() bool { return edition.IsDev() }

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrSpendLimitExceeded is returned when a spend limit is exceeded.
var ErrSpendLimitExceeded = errors.New("spend limit exceeded")

// IsSpendLimitExceeded checks if an error is a spend limit exceeded error.
func IsSpendLimitExceeded(err error) bool {
	return errors.Is(err, ErrSpendLimitExceeded)
}

// ---------------------------------------------------------------------------
// Callback types
// ---------------------------------------------------------------------------

// FeaturesCallback is invoked when the set of available features changes.
type FeaturesCallback func(features map[string]*FeatureRelease)

// SpendLimitConfigCallback is invoked when spend limit configuration is
// extracted from a license JWT.
type SpendLimitConfigCallback func(amount float64, action string, enabled bool)

// LimitsUpdateCallback is invoked when enforcement limits are updated from
// the license service.  The parameters are intentionally typed as any to
// avoid pulling proto dependencies into CE builds; EE adapters type-assert
// to *licv1.EnforcementLimits and *licv1.CumulativeUsageStats.
type LimitsUpdateCallback func(limits, cumulative any, exceeded bool)

// ---------------------------------------------------------------------------
// Value types — shared between CE and EE
// ---------------------------------------------------------------------------

// LicenseState is the superset of fields used by both the enforcer and the
// monitor.  Adapters populate the subset relevant to their source.
type LicenseState struct {
	Active                bool
	Status                string // "active", "inactive", "expired", "suspended", "cancelled", "disabled"
	Tier                  string // "free", "basic", "pro", "enterprise"
	IsPaid                bool
	ExpiresAt             *time.Time
	TrialExpires          *time.Time
	FetchedAt             time.Time
	SandboxBillingEnabled bool

	// Set by the enforcer adapter.
	TenantId   string
	InstanceId string

	// Set by the monitor adapter.
	UsageLimits []UsageLimit
	SpendLimits []SpendLimit
}

// IsSuspended returns true when the license status is "suspended" (e.g.
// usage limits exceeded).  Suspended licenses allow admin access but block
// AI gateway requests.
func (s *LicenseState) IsSuspended() bool {
	return s != nil && s.Status == "suspended"
}

// FeatureRelease represents feature metadata from the license service.
type FeatureRelease struct {
	Name        string
	Description string
	Status      string   // "development", "beta", "released", "deprecated"
	Categories  []string // e.g. ["gateway", "dashboard", "api"]
}

// FeatureState represents whether a feature is available and why.
type FeatureState struct {
	Enabled      bool
	RequiredTier string
	LockedReason string
	AvailableAt  *time.Time
}

// UsageType represents different categories of metered usage.
type UsageType string

const (
	UsageTypeRPM                 UsageType = "RPM"
	UsageTypeRequests            UsageType = "REQUESTS"
	UsageTypeRPS                 UsageType = "RPS"
	UsageTypeRPH                 UsageType = "RPH"
	UsageTypeTokens              UsageType = "TOKENS"
	UsageTypeStorageBytes        UsageType = "STORAGE_BYTES"
	UsageTypeHostedSites         UsageType = "HOSTED_SITES"
	UsageTypeHostingStorageBytes UsageType = "HOSTING_STORAGE_BYTES"
	UsageTypeDatasetItems        UsageType = "DATASET_ITEMS"
	UsageTypeEvalRunsMonthly     UsageType = "EVAL_RUNS_MONTHLY"
	UsageTypeAnnotationQueues    UsageType = "ANNOTATION_QUEUES"
	UsageTypePersistentTroopers  UsageType = "PERSISTENT_TROOPERS"
	UsageTypeAgents              UsageType = "AGENTS"
	UsageTypePersistentAgents    UsageType = "PERSISTENT_AGENTS"
	UsageTypeConcurrentRunning   UsageType = "CONCURRENT_RUNNING"
	UsageTypeConcurrentSandboxes UsageType = "CONCURRENT_SANDBOXES"
	UsageTypeConcurrentBrowsers  UsageType = "CONCURRENT_BROWSERS"
	UsageTypeBrowserSessionMax   UsageType = "BROWSER_SESSION_MAX_SECONDS"
	UsageTypeSandboxMemoryMB     UsageType = "SANDBOX_MEMORY_MB"
	UsageTypeMessagesMonthly     UsageType = "MESSAGES_MONTHLY"
	UsageTypeChannels            UsageType = "CHANNELS"
	UsageTypeChannelBindings     UsageType = "CHANNEL_BINDINGS"
	UsageTypeSpawnDepth          UsageType = "SPAWN_DEPTH"
	UsageTypeSessionRetention    UsageType = "SESSION_RETENTION_DAYS"
)

// UsageLimit represents a single usage limit configuration.
type UsageLimit struct {
	Type  UsageType
	Limit int64 // -1 means unlimited
}

// UsageStats tracks current usage statistics.
type UsageStats struct {
	RPM            int64
	RPS            int64
	RPH            int64
	TotalRequests  int64
	LastReset      time.Time
	LastMinute     time.Time
	LastSecond     time.Time
	LastHour       time.Time
	RequestsInMin  int64
	RequestsInSec  int64
	RequestsInHour int64

	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalTokens       int64

	EstimatedCostUSD float64
	CacheSavingsUSD  float64
	CacheHits        int64
	CacheMisses      int64
}

// RequestMetrics contains metrics for a single request.
type RequestMetrics struct {
	InputTokens   int64
	OutputTokens  int64
	EstimatedCost float64
	CacheSavings  float64
	CacheHit      bool
}

// SpendLimit represents a spending limit configuration.
type SpendLimit struct {
	ID             string
	OrganizationID string
	InstanceID     string
	LimitType      SpendLimitType
	LimitAmount    float64
	Period         SpendLimitPeriod
	ActionOnExceed SpendLimitAction
	CurrentSpend   float64
	Enabled        bool
}

// SpendLimitType represents the type of spend tracking.
type SpendLimitType string

const (
	SpendLimitTypeEstimatedCost SpendLimitType = "estimated_cost"
	SpendLimitTypeActualBilling SpendLimitType = "actual_billing"
)

// SpendLimitPeriod represents the time period for spend limits.
type SpendLimitPeriod string

const (
	SpendLimitPeriodDaily   SpendLimitPeriod = "daily"
	SpendLimitPeriodMonthly SpendLimitPeriod = "monthly"
)

// SpendLimitAction represents what happens when a limit is exceeded.
type SpendLimitAction string

const (
	SpendLimitActionBlock  SpendLimitAction = "block"
	SpendLimitActionWarn   SpendLimitAction = "warn"
	SpendLimitActionNotify SpendLimitAction = "notify"
)

// InstanceInfo represents cached instance activation data.
type InstanceInfo struct {
	InstanceID      string
	ActivationToken string
	RefreshToken    string
	LicenseState    json.RawMessage
	Status          string
	ActivatedAt     time.Time
}

// MonitorConfig holds configuration for creating a LicenseMonitor.
type MonitorConfig struct {
	CheckInterval     time.Duration
	WarnBefore        time.Duration
	LicenseServiceURL string
	OrganizationID    string
	InstanceID        string
}

// StorageConfig holds configuration for creating a PersistentMonitor.
type StorageConfig struct {
	InstanceID     string
	GatewayVersion string
	SyncInterval   time.Duration
}

// InstanceConfig holds configuration for the instance manager.
type InstanceConfig struct {
	PlatformURL       string
	InstanceSalt      string
	ActivationToken   string
	ActivateInterval  time.Duration
	ActivateTimeout   time.Duration
	DeviceFingerprint string
}

// StoreActivationOpts holds optional JWT fields for StoreActivation.
type StoreActivationOpts struct {
	LicenseJWT       string
	LicensePublicKey string
}

// DeviceFingerprintStatus represents the result of checking a device
// fingerprint against the license service.
type DeviceFingerprintStatus struct {
	HasBoundInstance bool
	BoundInstanceID  string
	PlanTier         string
	IsRevoked        bool
}

// IsBound returns true if the device has a bound (non-revoked) instance.
func (ds *DeviceFingerprintStatus) IsBound() bool {
	return ds != nil && ds.HasBoundInstance && !ds.IsRevoked
}
