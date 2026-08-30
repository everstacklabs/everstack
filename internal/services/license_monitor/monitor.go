package license_monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/http/middleware"
	"github.com/everstacklabs/everstack/internal/auth/m2m"
	"github.com/everstacklabs/everstack/internal/config"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	licenseconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
	"github.com/google/uuid"
)

// ErrSpendLimitExceeded is returned when a spend limit is exceeded.
// This error should be checked mid-stream to terminate ongoing requests.
var ErrSpendLimitExceeded = errors.New("spend limit exceeded")

// SpendLimitExceededError provides details about the exceeded spend limit.
type SpendLimitExceededError struct {
	CurrentSpend float64
	LimitAmount  float64
	Reason       string
}

func (e *SpendLimitExceededError) Error() string {
	return e.Reason
}

func (e *SpendLimitExceededError) Is(target error) bool {
	return target == ErrSpendLimitExceeded
}

// IsSpendLimitExceeded checks if an error is a spend limit exceeded error.
func IsSpendLimitExceeded(err error) bool {
	return errors.Is(err, ErrSpendLimitExceeded)
}

// UsageType represents different types of usage limits
type UsageType string

const (
	UsageTypeRPM                 UsageType = "RPM"                   // Requests per minute
	UsageTypeRequests            UsageType = "REQUESTS"              // Total requests (monthly/lifetime)
	UsageTypeRPS                 UsageType = "RPS"                   // Requests per second
	UsageTypeRPH                 UsageType = "RPH"                   // Requests per hour
	UsageTypeTokens              UsageType = "TOKENS"                // Total tokens (monthly)
	UsageTypeStorageBytes        UsageType = "STORAGE_BYTES"         // Storage quota in bytes
	UsageTypeHostedSites         UsageType = "HOSTED_SITES"          // Max hosted sites
	UsageTypeHostingStorageBytes UsageType = "HOSTING_STORAGE_BYTES" // Hosting storage quota in bytes
	UsageTypeConcurrentBrowsers  UsageType = "CONCURRENT_BROWSERS"   // Max concurrent browser sessions
	UsageTypeBrowserSessionMax   UsageType = "BROWSER_SESSION_MAX_SECONDS"
	UsageTypeDatasetItems        UsageType = "DATASET_ITEMS"          // Max dataset items
	UsageTypeEvalRunsMonthly     UsageType = "EVAL_RUNS_MONTHLY"      // Eval runs per month
	UsageTypeAnnotationQueues    UsageType = "ANNOTATION_QUEUES"      // Max annotation queues
	UsageTypePersistentTroopers  UsageType = "PERSISTENT_TROOPERS"    // Max persistent troopers
	UsageTypeAgents              UsageType = "AGENTS"                 // Max agent definitions
	UsageTypePersistentAgents    UsageType = "PERSISTENT_AGENTS"      // Max persistent agents
	UsageTypeConcurrentRunning   UsageType = "CONCURRENT_RUNNING"     // Max concurrently running agents
	UsageTypeConcurrentSandboxes UsageType = "CONCURRENT_SANDBOXES"   // Max concurrently allocated sandboxes
	UsageTypeSandboxMemoryMB     UsageType = "SANDBOX_MEMORY_MB"      // Sandbox memory limit in MB
	UsageTypeMessagesMonthly     UsageType = "MESSAGES_MONTHLY"       // Agent messages per month
	UsageTypeChannels            UsageType = "CHANNELS"               // Max channel configs (platform connections)
	UsageTypeChannelBindings     UsageType = "CHANNEL_BINDINGS"       // Max channel bindings
	UsageTypeSpawnDepth          UsageType = "SPAWN_DEPTH"            // Max agent spawn depth
	UsageTypeSessionRetention    UsageType = "SESSION_RETENTION_DAYS" // Session retention in days
)

// Spend sync configuration - batches updates to reduce license service load
const (
	// DefaultSpendSyncInterval is how often to sync accumulated spend to license service
	// At 10K instances with 30s interval = 333 RPS (vs 100K+ RPS with per-request updates)
	DefaultSpendSyncInterval = 30 * time.Second
	// MaxSpendRetryBackoff is the maximum backoff duration for failed syncs
	MaxSpendRetryBackoff = 5 * time.Minute
	// SpendRetryBaseBackoff is the base backoff duration for retries
	SpendRetryBaseBackoff = 5 * time.Second
	// PendingSpendRedisKeyPrefix is the Redis key prefix for pending spend deltas
	PendingSpendRedisKeyPrefix = "everstack:spend:pending:"
)

// GatewayM2MScopes are the scopes required for gateway M2M tokens.
// These allow the gateway to read license info and report usage.
var GatewayM2MScopes = []string{"license:read", "license:write", "instance:read", "instance:write"}

// UsageLimit represents a usage limit configuration
type UsageLimit struct {
	Type  UsageType
	Limit int64 // -1 means unlimited
}

// UsageStats tracks current usage statistics
type UsageStats struct {
	// Request rate metrics
	RPM            int64     // Peak requests per minute
	RPS            int64     // Peak requests per second
	RPH            int64     // Peak requests per hour
	TotalRequests  int64     // Total requests (resets monthly)
	LastReset      time.Time // Last time stats were reset
	LastMinute     time.Time // Last minute window start
	LastSecond     time.Time // Last second window start
	LastHour       time.Time // Last hour window start
	RequestsInMin  int64     // Requests in current minute
	RequestsInSec  int64     // Requests in current second
	RequestsInHour int64     // Requests in current hour

	// Token metrics
	TotalInputTokens  int64 // Total input tokens processed
	TotalOutputTokens int64 // Total output tokens generated
	TotalTokens       int64 // Total tokens (input + output)

	// Cost metrics (in USD)
	EstimatedCostUSD float64 // Estimated total cost
	CacheSavingsUSD  float64 // Cost savings from cache hits

	// Cache performance metrics
	CacheHits   int64 // Number of cache hits
	CacheMisses int64 // Number of cache misses
}

// RequestMetrics contains metrics for a single request
type RequestMetrics struct {
	InputTokens   int64   // Input tokens for this request
	OutputTokens  int64   // Output tokens for this request
	EstimatedCost float64 // Estimated cost in USD for this request
	CacheSavings  float64 // Cost savings from cache (if cache hit)
	CacheHit      bool    // Whether this request was a cache hit
}

// LicenseState represents the current license state
type LicenseState struct {
	Active                bool
	Tier                  string
	Status                string
	IsPaid                bool
	ExpiresAt             *time.Time
	TrialExpires          *time.Time
	FetchedAt             time.Time
	UsageLimits           []UsageLimit
	SpendLimits           []SpendLimit // Spend limits for the organization/instance
	SandboxBillingEnabled bool
}

// SpendLimit represents a spending limit configuration
type SpendLimit struct {
	ID             string
	OrganizationID string
	InstanceID     string // Empty = org-wide, set = instance-specific
	LimitType      SpendLimitType
	LimitAmount    float64
	Period         SpendLimitPeriod
	ActionOnExceed SpendLimitAction
	CurrentSpend   float64
	Enabled        bool
}

// SpendLimitType represents the type of spend tracking
type SpendLimitType string

const (
	SpendLimitTypeEstimatedCost SpendLimitType = "estimated_cost"
	SpendLimitTypeActualBilling SpendLimitType = "actual_billing"
)

// SpendLimitPeriod represents the time period for spend limits
type SpendLimitPeriod string

const (
	SpendLimitPeriodDaily   SpendLimitPeriod = "daily"
	SpendLimitPeriodMonthly SpendLimitPeriod = "monthly"
)

// SpendLimitAction represents what happens when a limit is exceeded
type SpendLimitAction string

const (
	SpendLimitActionBlock  SpendLimitAction = "block"
	SpendLimitActionWarn   SpendLimitAction = "warn"
	SpendLimitActionNotify SpendLimitAction = "notify"
)

// FeatureState represents whether a feature is available
type FeatureState struct {
	Enabled      bool
	RequiredTier string
	LockedReason string
	AvailableAt  *time.Time // When feature becomes available (if temporarily locked)
}

// FeatureRelease represents feature metadata from the license service
type FeatureRelease struct {
	Name        string
	Description string
	Status      string   // 'development', 'beta', 'released', 'deprecated'
	Categories  []string // ['gateway', 'dashboard', 'api']
}

// Monitor tracks license state, usage limits, and feature availability
type Monitor struct {
	enforcer          *middleware.LicenseEnforcer
	mu                sync.RWMutex
	usage             UsageStats
	licenseState      *LicenseState
	features          map[string]FeatureState
	availableFeatures map[string]*FeatureRelease // Features from license service (tier-evaluated)
	plansConfig       *config.PlansConfig        // Cached plans configuration
	licenseServiceURL string                     // URL to fetch updated plans from
	locked            bool                       // Gateway is completely locked
	lockReason        string
	checkInterval     time.Duration
	stopCh            chan struct{}
	subscribers       []func(LicenseState)
	// Spend limit tracking
	organizationID       string // Organization ID for spend limit lookups
	instanceID           string // Instance ID for spend limit lookups
	spendLimitsFetched   bool   // Whether spend limits have been fetched
	spendLimitsLastFetch time.Time
	// M2M authenticated client for spend limit updates
	spendLimitClient licenseconnect.LicenseServiceClient
	signingKey       []byte // HMAC signing key for M2M authentication
	// Spend limit blocking state (set when license service reports limit exceeded with block action)
	spendBlocked       bool   // Whether new requests should be blocked due to spend limit
	spendBlockedReason string // Human-readable reason for the block
	// Local spend tracking for async enforcement (zero-latency checks)
	localCurrentSpend float64          // Gateway's local estimate of current spend
	localLimitAmount  float64          // Cached limit amount from license service
	localLimitEnabled bool             // Whether a spend limit is active
	localLimitAction  SpendLimitAction // Action to take when limit exceeded (block, warn, notify)
	// Batched spend sync (reduces license service load from 100K+ RPS to ~333 RPS)
	pendingSpendDelta       float64            // Accumulated spend since last sync (persisted to Redis)
	spendRetryCount         int                // Current retry count for failed syncs
	spendNextRetry          time.Time          // When to next attempt sync after failure
	spendSyncInterval       time.Duration      // How often to sync spend to license service
	spendLimitConfigFromJWT bool               // True if spend config was set via JWT (disables polling)
	redisClient             *cache.RedisClient // Redis client for spend persistence
	processID               string             // Unique process ID for Redis key namespacing
	// Idempotency for spend sync
	spendIdempotencyKey string // UUID reused across retries, cleared on success
}

// Config for the license monitor
type Config struct {
	CheckInterval     time.Duration // How often to check license state
	WarnBefore        time.Duration // Warning period before expiry (e.g., 7 days)
	LicenseServiceURL string        // URL of the license service (for fetching plans)
	OrganizationID    string        // Organization ID for spend limit lookups
	InstanceID        string        // Instance ID for spend limit lookups
}

// NewMonitor creates a new license monitor
func NewMonitor(enforcer *middleware.LicenseEnforcer, cfg Config) *Monitor {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 1 * time.Hour // Check every hour by default
	}

	// Load plans configuration with cascading fallbacks
	plansConfig := loadPlansConfig(cfg)

	// Generate unique process ID for Redis key namespacing
	// This handles multiple gateway instances with the same instance ID
	processID := os.Getenv("POD_NAME") // Kubernetes pod name
	if processID == "" {
		processID = os.Getenv("HOSTNAME")
	}
	if processID == "" {
		processID = uuid.New().String()[:8] // Short random ID as fallback
	}

	return &Monitor{
		enforcer:          enforcer,
		usage:             UsageStats{LastReset: time.Now()},
		features:          make(map[string]FeatureState),
		plansConfig:       plansConfig,
		licenseServiceURL: cfg.LicenseServiceURL,
		checkInterval:     cfg.CheckInterval,
		stopCh:            make(chan struct{}),
		subscribers:       make([]func(LicenseState), 0),
		organizationID:    cfg.OrganizationID,
		instanceID:        cfg.InstanceID,
		spendSyncInterval: DefaultSpendSyncInterval,
		processID:         processID,
	}
}

// SetSpendLimitConfig updates the local spend limit configuration from JWT claims.
// This replaces polling GetSpendLimits — limit config is now pushed via the license JWT.
func (m *Monitor) SetSpendLimitConfig(amount float64, action string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.localLimitAmount = amount
	m.localLimitEnabled = enabled
	m.spendLimitConfigFromJWT = true

	switch SpendLimitAction(action) {
	case SpendLimitActionBlock, SpendLimitActionWarn, SpendLimitActionNotify:
		m.localLimitAction = SpendLimitAction(action)
	default:
		m.localLimitAction = SpendLimitActionBlock
	}

	// Re-evaluate blocked state with new config
	if !enabled {
		if m.spendBlocked {
			m.spendBlocked = false
			m.spendBlockedReason = ""
			logger.Info("license_monitor: spend limit disabled via JWT, unblocking")
		}
	} else if m.localLimitAction == SpendLimitActionBlock && m.localCurrentSpend >= m.localLimitAmount {
		if !m.spendBlocked {
			m.spendBlocked = true
			m.spendBlockedReason = fmt.Sprintf("Spend limit exceeded. Current: $%.2f, Limit: $%.2f", m.localCurrentSpend, m.localLimitAmount)
			logger.Warnf("license_monitor: spend limit exceeded after JWT config update")
		}
	} else if m.spendBlocked && m.localCurrentSpend < m.localLimitAmount {
		m.spendBlocked = false
		m.spendBlockedReason = ""
		logger.Info("license_monitor: spend limit no longer exceeded after JWT config update, unblocking")
	}

	logger.Debugf("license_monitor: spend limit config updated from JWT - enabled: %v, amount: $%.2f, action: %s",
		enabled, amount, action)
}

// SetRedisClient sets the Redis client for pending spend persistence
// This enables spend tracking to survive gateway restarts
func (m *Monitor) SetRedisClient(client *cache.RedisClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redisClient = client
}

// loadPendingSpendFromRedis loads any pending spend delta from Redis on startup
// This ensures spend is not lost if gateway restarts before syncing to license service
func (m *Monitor) loadPendingSpendFromRedis(ctx context.Context) {
	m.mu.RLock()
	instID := m.instanceID
	procID := m.processID
	redisClient := m.redisClient
	m.mu.RUnlock()

	if instID == "" || redisClient == nil || !redisClient.IsConnected() {
		return
	}

	key := PendingSpendRedisKeyPrefix + instID + ":" + procID
	pendingDelta, err := redisClient.Client().Get(ctx, key).Float64()
	if err != nil {
		// Not an error if key doesn't exist (fresh start)
		return
	}

	if pendingDelta > 0 {
		m.mu.Lock()
		m.pendingSpendDelta = pendingDelta
		m.localCurrentSpend += pendingDelta // Add to local tracking too
		m.mu.Unlock()
		logger.Infof("license_monitor: loaded pending spend delta from Redis: $%.4f", pendingDelta)
	}
}

// loadPlansConfig loads plans configuration with intelligent fallbacks:
// 1. Try license service API (if URL provided) for dynamic plan updates
// 2. Fall back to the canonical plans embedded in the binary (pkg/plans)
// 3. Hard-coded defaults in parseUsageLimits as a truly last resort
func loadPlansConfig(cfg Config) *config.PlansConfig {
	// Try license service API (for dynamic plan updates)
	if cfg.LicenseServiceURL != "" {
		plansConfig, err := fetchPlansFromAPI(cfg.LicenseServiceURL)
		if err != nil {
			logger.Warnf("failed to fetch plans from API %s: %v - using embedded plans", cfg.LicenseServiceURL, err)
		} else {
			logger.Infof("successfully loaded plans from API: %s", cfg.LicenseServiceURL)
			return plansConfig
		}
	}

	// Embedded canonical plans: air-gapped and offline-licensed instances
	// (EVS_LICENSE_FILE) must resolve the FULL per-tier limit matrix, not
	// the 3-type rate subset in parseUsageLimits, or every resource-count
	// limit on a licensed air-gapped instance silently resolves to
	// unlimited (docs/design/editions-and-billing.md sections 5-7).
	if embedded, err := config.LoadPlansConfig(""); err == nil && embedded != nil {
		logger.Info("license_monitor: using embedded canonical plans configuration")
		return embedded
	} else if err != nil {
		logger.Warnf("license_monitor: embedded plans unavailable: %v - using hard-coded defaults", err)
	}

	// Fall back to hard-coded defaults in parseUsageLimits
	logger.Warn("no plans configuration available - using hard-coded defaults")
	return nil
}

// fetchPlansFromAPI fetches plans configuration from the license service via ConnectRPC
func fetchPlansFromAPI(serviceURL string) (*config.PlansConfig, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	licenseClient := licenseconnect.NewLicenseServiceClient(httpClient, serviceURL)

	// Call License Service GetPlans RPC
	plansReq := connect.NewRequest(&licv1.GetPlansRequest{})
	plansResp, err := licenseClient.GetPlans(context.Background(), plansReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call plans API: %w", err)
	}

	// Convert proto response to PlansConfig
	plansConfig := &config.PlansConfig{
		Plans: make(map[string]config.PlanConfig),
	}

	for _, p := range plansResp.Msg.GetPlans() {
		plan := config.PlanConfig{
			Tier:        p.GetTier(),
			Name:        p.GetName(),
			Description: p.GetDescription(),
			Highlight:   p.GetHighlight(),
		}

		// Convert pricing
		if p.GetPricing() != nil {
			plan.Pricing = config.PlanPricing{
				Monthly: p.GetPricing().GetMonthly(),
				Yearly:  p.GetPricing().GetYearly(),
			}
		}

		// Convert features
		plan.Features = make([]config.PlanFeature, 0, len(p.GetFeatures()))
		for _, f := range p.GetFeatures() {
			plan.Features = append(plan.Features, config.PlanFeature{
				Name:    f.GetName(),
				Enabled: f.GetEnabled(),
			})
		}

		// Convert usage limits
		plan.UsageLimits = make([]config.PlanUsageLimit, 0, len(p.GetUsageLimits()))
		for _, ul := range p.GetUsageLimits() {
			plan.UsageLimits = append(plan.UsageLimits, config.PlanUsageLimit{
				Type:    ul.GetType(),
				Value:   ul.GetValue(),
				SubText: ul.GetSubText(),
			})
		}

		plansConfig.Plans[p.GetTier()] = plan
	}

	return plansConfig, nil
}

// refreshPlansConfig attempts to refresh plans configuration from the license service API
func (m *Monitor) refreshPlansConfig() {
	if m.licenseServiceURL == "" {
		return // No API configured, skip refresh
	}

	newPlansConfig, err := fetchPlansFromAPI(m.licenseServiceURL)
	if err != nil {
		logger.Debugf("failed to refresh plans from API %s: %v", m.licenseServiceURL, err)
		return
	}

	m.mu.Lock()
	oldConfig := m.plansConfig
	m.plansConfig = newPlansConfig
	m.mu.Unlock()

	logger.Infof("successfully refreshed plans configuration from API: %s", m.licenseServiceURL)

	// If license state exists, update its usage limits with new plans
	if oldConfig != nil {
		m.mu.Lock()
		state := m.licenseState
		if state != nil {
			state.UsageLimits = m.parseUsageLimits(state.Tier)
		}
		// Copy subscribers and state under lock for safe iteration
		var subs []func(LicenseState)
		var stateCopy LicenseState
		if state != nil {
			subs = make([]func(LicenseState), len(m.subscribers))
			copy(subs, m.subscribers)
			stateCopy = *state
		}
		m.mu.Unlock()

		// Notify subscribers (outside lock)
		for _, sub := range subs {
			sub(stateCopy)
		}
	}
}

// Start begins monitoring license state and usage
func (m *Monitor) Start(ctx context.Context) {
	// Load any pending spend from Redis (survives gateway restarts)
	m.loadPendingSpendFromRedis(ctx)

	// Initial check
	m.checkLicenseState()

	// Start background monitoring
	go m.monitorLoop(ctx)

	// Start usage reset ticker
	go m.usageResetLoop(ctx)

	// Start batched spend sync loop (syncs accumulated spend to license service)
	go m.spendLimitSyncLoop(ctx)
}

// Stop stops the monitor
func (m *Monitor) Stop() {
	close(m.stopCh)
}

// Refresh forces an immediate check of the license state
// Call this after license activation/upgrade to update the monitor immediately
func (m *Monitor) Refresh() {
	m.checkLicenseState()
}

// monitorLoop periodically checks license state
func (m *Monitor) monitorLoop(ctx context.Context) {
	licenseTicker := time.NewTicker(m.checkInterval)
	defer licenseTicker.Stop()

	// Refresh plans less frequently - every 6 hours
	plansTicker := time.NewTicker(6 * time.Hour)
	defer plansTicker.Stop()

	for {
		select {
		case <-licenseTicker.C:
			m.checkLicenseState()
		case <-plansTicker.C:
			m.refreshPlansConfig()
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// usageResetLoop resets usage counters periodically
func (m *Monitor) usageResetLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second) // Check every second for precision
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.resetUsageCounters()
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// spendLimitSyncLoop periodically sends accumulated pending spend to the license service.
// Spend limit *config* is now pushed via the license JWT (see SetSpendLimitConfig).
// Only falls back to polling GetSpendLimits if JWT has no spend limit fields (old license service).
// Uses adaptive interval based on spend proximity to limit.
func (m *Monitor) spendLimitSyncLoop(ctx context.Context) {
	// Wait a bit before starting to allow activation to complete
	time.Sleep(2 * time.Second)

	interval := m.calculateSpendSyncInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Fallback limit polling uses a slower 5-minute interval
	fallbackTicker := time.NewTicker(5 * time.Minute)
	defer fallbackTicker.Stop()

	for {
		select {
		case <-ticker.C:
			// Send any accumulated spend delta to license service
			m.syncPendingSpendBatch(ctx)

			// Recalculate and adjust interval based on current spend ratio
			newInterval := m.calculateSpendSyncInterval()
			if newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
				logger.Debugf("license_monitor: spend sync interval adjusted to %v", interval)
			}
		case <-fallbackTicker.C:
			// Fallback: poll GetSpendLimits only if JWT didn't provide spend config
			m.mu.RLock()
			hasJWTConfig := m.spendLimitConfigFromJWT
			m.mu.RUnlock()
			if !hasJWTConfig {
				m.syncSpendLimits(ctx)
			}
		case <-m.stopCh:
			// Final sync attempt before stopping
			m.syncPendingSpendBatch(ctx)
			return
		case <-ctx.Done():
			// Final sync attempt on context cancellation
			m.syncPendingSpendBatch(ctx)
			return
		}
	}
}

// syncPendingSpendBatch sends accumulated pending spend to the license service.
// Uses exponential backoff retry on failure. Only clears pending on success.
func (m *Monitor) syncPendingSpendBatch(ctx context.Context) {
	m.mu.RLock()
	pendingDelta := m.pendingSpendDelta
	orgID := m.organizationID
	instID := m.instanceID
	procID := m.processID
	client := m.spendLimitClient
	licenseURL := m.licenseServiceURL
	retryCount := m.spendRetryCount
	nextRetry := m.spendNextRetry
	redisClient := m.redisClient
	m.mu.RUnlock()

	// Nothing to sync
	if pendingDelta <= 0 {
		return
	}

	// Not activated yet
	if orgID == "" {
		return
	}

	// Check if we're in backoff period
	if retryCount > 0 && time.Now().Before(nextRetry) {
		return
	}

	// Create client if needed
	if client == nil {
		if licenseURL == "" {
			return
		}
		// Use JWT-based M2M auth if signing key is available
		m.mu.RLock()
		signingKey := m.signingKey
		m.mu.RUnlock()

		var httpClient *http.Client
		if len(signingKey) >= 32 && instID != "" {
			// Create JWT-based M2M provider using the activation-derived signing key
			simpleConfig := &m2m.SimpleConfig{
				SigningKey: signingKey,
				Issuer:     "everstack",
				Audience:   "everstack-services",
				TokenTTL:   5 * time.Minute,
			}
			// Include gateway scopes so the license service allows the request
			provider, err := m2m.NewSimpleTokenProviderWithScopes(simpleConfig, instID, GatewayM2MScopes)
			if err != nil {
				logger.WithError(err).Warn("license_monitor: failed to create M2M provider for spend sync")
				httpClient = &http.Client{Timeout: 10 * time.Second}
			} else {
				httpClient = m2m.NewHTTPClient(provider, 10*time.Second)
			}
		} else {
			httpClient = &http.Client{Timeout: 10 * time.Second}
		}
		client = licenseconnect.NewLicenseServiceClient(httpClient, licenseURL)

		m.mu.Lock()
		m.spendLimitClient = client
		m.mu.Unlock()
	}

	// Get or create idempotency key (reused across retries for same delta)
	m.mu.Lock()
	if m.spendIdempotencyKey == "" {
		m.spendIdempotencyKey = uuid.New().String()
	}
	idempotencyKey := m.spendIdempotencyKey
	m.mu.Unlock()

	// Send the accumulated spend delta
	syncCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req := connect.NewRequest(&licv1.UpdateSpendUsageRequest{
		OrganizationId: orgID,
		InstanceId:     instID,
		EstimatedCost:  pendingDelta,
		IdempotencyKey: idempotencyKey,
	})

	resp, err := client.UpdateSpendUsage(syncCtx, req)
	if err != nil {
		// Increment retry count and calculate next retry time (exponential backoff)
		m.mu.Lock()
		m.spendRetryCount++
		backoff := calculateBackoff(m.spendRetryCount)
		m.spendNextRetry = time.Now().Add(backoff)
		m.mu.Unlock()

		logger.Warnf("license_monitor: failed to sync pending spend ($%.4f), retry %d in %v: %v",
			pendingDelta, m.spendRetryCount, backoff, err)
		return
	}

	// SUCCESS - clear pending delta and idempotency key
	m.mu.Lock()
	m.pendingSpendDelta = 0
	m.spendRetryCount = 0
	m.spendNextRetry = time.Time{}
	m.spendIdempotencyKey = "" // New key will be generated for next batch

	// Update local spend from server's authoritative value
	m.localCurrentSpend = resp.Msg.GetNewEstimatedSpend()
	// Keep EstimatedCostUSD in sync with the authoritative server value
	m.usage.EstimatedCostUSD = resp.Msg.GetNewEstimatedSpend()

	// Check if spend limit is now exceeded
	if resp.Msg.GetLimitExceeded() && resp.Msg.GetAction() == licv1.SpendLimitAction_SPEND_LIMIT_ACTION_BLOCK {
		if !m.spendBlocked {
			m.spendBlocked = true
			m.spendBlockedReason = fmt.Sprintf("Spend limit exceeded. Current: $%.2f", m.localCurrentSpend)
			logger.Error("license_monitor: SPEND BLOCKED - server confirmed limit exceeded")
		}
	} else if m.spendBlocked {
		m.spendBlocked = false
		m.spendBlockedReason = ""
		logger.Info("license_monitor: spend limit no longer exceeded, unblocking")
	}
	m.mu.Unlock()

	// Clear Redis pending (best effort)
	if redisClient != nil && redisClient.IsConnected() && instID != "" {
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer clearCancel()
		key := PendingSpendRedisKeyPrefix + instID + ":" + procID
		if err := redisClient.Client().Del(clearCtx, key).Err(); err != nil {
			logger.WithError(err).Warn("license_monitor: failed to clear Redis pending spend")
		}
	}

	logger.Debugf("license_monitor: synced pending spend ($%.4f) to license service, new total: $%.4f",
		pendingDelta, resp.Msg.GetNewEstimatedSpend())
}

// calculateSpendSyncInterval returns an adaptive sync interval based on proximity to the spend limit.
// Far from limit (<50%): 5 minutes. Approaching (50-80%): 1 minute. Near limit (>80%): 15 seconds.
func (m *Monitor) calculateSpendSyncInterval() time.Duration {
	m.mu.RLock()
	enabled := m.localLimitEnabled
	limitAmount := m.localLimitAmount
	currentSpend := m.localCurrentSpend
	m.mu.RUnlock()

	if !enabled || limitAmount <= 0 {
		return 5 * time.Minute
	}
	ratio := currentSpend / limitAmount
	switch {
	case ratio >= 0.8:
		return 15 * time.Second
	case ratio >= 0.5:
		return 1 * time.Minute
	default:
		return 5 * time.Minute
	}
}

// calculateBackoff returns the backoff duration for the given retry count
// Uses exponential backoff: 5s, 10s, 20s, 40s, ... up to MaxSpendRetryBackoff
func calculateBackoff(retryCount int) time.Duration {
	backoff := SpendRetryBaseBackoff * time.Duration(1<<uint(retryCount-1))
	if backoff > MaxSpendRetryBackoff {
		backoff = MaxSpendRetryBackoff
	}
	return backoff
}

// syncSpendLimits fetches current spend limits from the license service
// and updates local state for accurate enforcement.
func (m *Monitor) syncSpendLimits(ctx context.Context) {
	m.mu.RLock()
	orgID := m.organizationID
	instID := m.instanceID
	client := m.spendLimitClient
	licenseURL := m.licenseServiceURL
	m.mu.RUnlock()

	// Not activated yet - skip sync
	if orgID == "" {
		return
	}

	// Create client if needed
	if client == nil {
		if licenseURL == "" {
			return
		}
		httpClient := &http.Client{Timeout: 5 * time.Second}
		client = licenseconnect.NewLicenseServiceClient(httpClient, licenseURL)
	}

	// Fetch spend limits from license service
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := connect.NewRequest(&licv1.GetSpendLimitsRequest{
		OrganizationId: orgID,
		InstanceId:     instID,
	})

	resp, err := client.GetSpendLimits(syncCtx, req)
	if err != nil {
		logger.WithError(err).Debug("license_monitor: spend limit sync failed (non-fatal)")
		return
	}

	// Update local state from server response
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the active spend limit (prefer instance-specific, then org-wide)
	var activeLimit *licv1.SpendLimit
	for _, limit := range resp.Msg.GetSpendLimits() {
		if !limit.GetEnabled() {
			continue
		}
		// Only track estimated cost limits (not actual billing)
		if limit.GetLimitType() != licv1.SpendLimitType_SPEND_LIMIT_TYPE_ESTIMATED_COST {
			continue
		}
		// Prefer instance-specific limits
		if limit.GetInstanceId() != "" && limit.GetInstanceId() == instID {
			activeLimit = limit
			break
		}
		// Fall back to org-wide limit
		if limit.GetInstanceId() == "" && activeLimit == nil {
			activeLimit = limit
		}
	}

	if activeLimit != nil {
		m.localLimitEnabled = true
		m.localLimitAmount = activeLimit.GetLimitAmount()
		m.localCurrentSpend = activeLimit.GetCurrentSpend()
		// Keep EstimatedCostUSD in sync with the authoritative server value
		m.usage.EstimatedCostUSD = activeLimit.GetCurrentSpend()
		m.localLimitAction = protoSpendLimitActionToLocal(activeLimit.GetActionOnExceed())

		// Update blocked state based on server data
		if m.localLimitAction == SpendLimitActionBlock && m.localCurrentSpend >= m.localLimitAmount {
			if !m.spendBlocked {
				m.spendBlocked = true
				m.spendBlockedReason = fmt.Sprintf("Spend limit exceeded. Current: $%.2f, Limit: $%.2f", m.localCurrentSpend, m.localLimitAmount)
				logger.Warnf("license_monitor: sync detected spend limit exceeded - blocking requests")
			}
		} else if m.spendBlocked && m.localCurrentSpend < m.localLimitAmount {
			// Limit was increased or spend was reset - unblock
			m.spendBlocked = false
			m.spendBlockedReason = ""
			logger.Info("license_monitor: sync detected spend limit no longer exceeded - unblocking requests")
		}

		logger.Debugf("license_monitor: spend limit sync complete - current: $%.2f, limit: $%.2f, blocked: %v",
			m.localCurrentSpend, m.localLimitAmount, m.spendBlocked)
	} else {
		// No active limit - clear local state
		if m.localLimitEnabled {
			logger.Debug("license_monitor: no active spend limit found, clearing local limit state")
		}
		m.localLimitEnabled = false
		m.localLimitAmount = 0
		// Don't reset localCurrentSpend - keep tracking for when limit is re-enabled
		if m.spendBlocked && m.spendBlockedReason != "" {
			// Only unblock if it was blocked due to spend limit
			m.spendBlocked = false
			m.spendBlockedReason = ""
		}
	}
}

// checkLicenseState fetches and evaluates current license state
func (m *Monitor) checkLicenseState() {
	if m.enforcer == nil {
		return
	}

	// Get cached license state from enforcer
	cachedState := m.enforcer.GetCached()

	// DEBUG: Log what the monitor is reading from the enforcer cache
	if cachedState != nil {
		logger.Infof("license_monitor.checkLicenseState: got cached state - tier=%s status=%s active=%v",
			cachedState.Tier, cachedState.Status, cachedState.Active)
	} else {
		logger.Info("license_monitor.checkLicenseState: cached state is nil, will check trial mode")
	}
	if cachedState == nil {
		// No license state available - check if trial mode is active
		if m.enforcer.IsInTrialMode() {
			// Trial mode is active, don't lock the gateway
			m.setLocked(false, "")
			// Set free plan limits for trial mode (since trial effectively gives free plan access)
			trialManager := m.enforcer.GetTrialManager()
			var trialExpires *time.Time
			if trialManager != nil {
				expiresAt := trialManager.GetStatus()["expires_at"]
				if expiresAtStr, ok := expiresAt.(string); ok {
					if parsed, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
						trialExpires = &parsed
					}
				}
			}

			m.mu.Lock()
			m.licenseState = &LicenseState{
				Active:                true,
				Tier:                  "free",
				Status:                "trial",
				IsPaid:                false,
				TrialExpires:          trialExpires,
				UsageLimits:           m.parseUsageLimits("free"),
				SandboxBillingEnabled: false,
			}
			m.mu.Unlock()
			m.updateFeatureStates()
			return
		}

		// No license and no trial: the instance runs as Community Edition.
		// This is a terminal, non-blocking state (editions-and-billing.md
		// D9) — free-plan limits apply, the gateway is NOT locked.
		m.setLocked(false, "")
		m.mu.Lock()
		m.licenseState = &LicenseState{
			Active:      false,
			Tier:        "free",
			Status:      "unlicensed",
			IsPaid:      false,
			UsageLimits: m.parseUsageLimits("free"),
		}
		m.mu.Unlock()
		m.updateFeatureStates()
		return
	}

	// Grace state machine (editions-and-billing.md D6): an expired license
	// keeps full plan entitlements for the grace window, then degrades to CE.
	// Grace anchors on the license's own expiry so it survives restarts.
	effectiveActive := cachedState.Active
	effectiveStatus := cachedState.Status
	effectiveTier := cachedState.Tier
	if effectiveActive && cachedState.ExpiresAt != nil && cachedState.ExpiresAt.Before(time.Now().UTC()) {
		if time.Now().UTC().Before(cachedState.ExpiresAt.Add(middleware.LicenseGraceDuration)) {
			effectiveStatus = "grace" // full entitlements retained
		} else {
			effectiveActive = false
			effectiveStatus = "degraded"
			effectiveTier = "free" // CE limits from here on
		}
	}

	// Update internal state
	m.mu.Lock()
	m.licenseState = &LicenseState{
		Active:                effectiveActive,
		Tier:                  effectiveTier,
		Status:                effectiveStatus,
		IsPaid:                cachedState.IsPaid,
		ExpiresAt:             cachedState.ExpiresAt,
		TrialExpires:          cachedState.TrialExpires,
		FetchedAt:             cachedState.FetchedAt,
		UsageLimits:           m.parseUsageLimits(effectiveTier),
		SandboxBillingEnabled: cachedState.SandboxBillingEnabled,
	}
	// Also update the organization and instance IDs from the cached state
	if cachedState.TenantId != "" {
		m.organizationID = cachedState.TenantId
	}
	if cachedState.InstanceId != "" {
		m.instanceID = cachedState.InstanceId
	}
	// Copy subscribers and state under lock for safe iteration
	subs := make([]func(LicenseState), len(m.subscribers))
	copy(subs, m.subscribers)
	stateCopy := *m.licenseState
	m.mu.Unlock()

	// Notify subscribers (outside lock)
	for _, sub := range subs {
		sub(stateCopy)
	}

	// No license state is lock-worthy anymore: inactive/expired/degraded
	// all resolve to CE entitlements at the enforcement callsites, and reads
	// are never blocked (editions-and-billing.md D6/D9). Log the situation
	// instead of locking the gateway.
	if err := m.validateLicenseState(); err != nil {
		logger.Warnf("license_monitor: %v — operating with CE entitlements", err)
	}

	// Check expiry warnings
	m.checkExpiryWarnings()

	// The gateway is never locked by license state under the new model.
	m.setLocked(false, "")

	// Update feature availability
	m.updateFeatureStates()
}

// validateLicenseState reports (as an error for logging purposes) any
// non-nominal license situation. It never locks the gateway.
func (m *Monitor) validateLicenseState() error {
	m.mu.RLock()
	state := m.licenseState
	m.mu.RUnlock()

	if state == nil {
		return fmt.Errorf("license state not initialized")
	}

	// Suspended licenses (usage limits exceeded) are NOT lock-worthy.
	// The admin dashboard should remain fully accessible; only AI gateway
	// requests are blocked by the SpendLimitInterceptor.
	if state.Status == "suspended" {
		return nil
	}

	switch state.Status {
	case "grace":
		return fmt.Errorf("license expired, in grace window until %s",
			state.ExpiresAt.Add(middleware.LicenseGraceDuration).Format(time.RFC3339))
	case "degraded":
		return fmt.Errorf("license expired beyond grace, degraded to CE limits")
	case "unlicensed":
		return nil // normal CE operation, nothing to log
	}

	if !state.Active {
		return fmt.Errorf("license is not active (status: %s)", state.Status)
	}

	return nil
}

// checkExpiryWarnings logs warnings for upcoming expiry
func (m *Monitor) checkExpiryWarnings() {
	m.mu.RLock()
	state := m.licenseState
	m.mu.RUnlock()

	if state == nil {
		return
	}

	now := time.Now().UTC()
	warnPeriod := 7 * 24 * time.Hour // Warn 7 days before expiry

	if state.ExpiresAt != nil {
		timeUntilExpiry := state.ExpiresAt.Sub(now)
		if timeUntilExpiry > 0 && timeUntilExpiry <= warnPeriod {
			daysLeft := int(timeUntilExpiry.Hours() / 24)
			logger.Debugf("license_monitor: license expires in %d days (%s)", daysLeft, state.ExpiresAt.Format("2006-01-02"))
		}
	}

	if !state.IsPaid && state.TrialExpires != nil {
		timeUntilExpiry := state.TrialExpires.Sub(now)
		if timeUntilExpiry > 0 && timeUntilExpiry <= warnPeriod {
			daysLeft := int(timeUntilExpiry.Hours() / 24)
			logger.Debugf("license_monitor: free trial expires in %d days (%s)", daysLeft, state.TrialExpires.Format("2006-01-02"))
		}
	}
}

// parseUsageLimits extracts usage limits based on tier
func (m *Monitor) parseUsageLimits(tier string) []UsageLimit {
	// If we have loaded plans config, use it
	if m.plansConfig != nil {
		plan, ok := m.plansConfig.GetPlan(tier)
		if ok {
			limits := make([]UsageLimit, 0, len(plan.UsageLimits))
			for _, limit := range plan.UsageLimits {
				usageType := m.convertUsageType(limit.Type)
				if usageType != "" {
					limits = append(limits, UsageLimit{
						Type:  usageType,
						Limit: limit.Value,
					})
				}
			}
			return limits
		}
	}

	// Fallback to hard-coded values if config not available.
	//
	// The free tier fallback carries the FULL free-plan limit set (mirroring
	// plans.json, pinned by internal/enterprise/ce_defaults_test.go): a
	// license-service outage must not silently unlock resource-count limits
	// (AGENTS, DATASET_ITEMS, ...) for unlicensed instances. Paid tiers keep
	// the rate-limit subset: for a verified paid license, missing plan data
	// should not clamp resource counts to wrong values (fail open on counts,
	// fail closed on rates) until a refresh delivers the real plan.
	logger.Debugf("license_monitor: using hard-coded usage limits for tier %s", tier)
	limits := make([]UsageLimit, 0)

	switch tier {
	case "free":
		limits = append(limits,
			UsageLimit{Type: UsageTypeRPM, Limit: 60},
			UsageLimit{Type: UsageTypeTokens, Limit: 1000000},
			UsageLimit{Type: UsageTypeRequests, Limit: 10000},
			UsageLimit{Type: UsageTypeStorageBytes, Limit: 524288000},
			UsageLimit{Type: UsageTypeDatasetItems, Limit: 1000},
			UsageLimit{Type: UsageTypeEvalRunsMonthly, Limit: 5},
			UsageLimit{Type: UsageTypeAnnotationQueues, Limit: 1},
			UsageLimit{Type: UsageTypeAgents, Limit: 3},
			UsageLimit{Type: UsageTypePersistentAgents, Limit: 1},
			UsageLimit{Type: UsageTypeConcurrentRunning, Limit: 1},
			UsageLimit{Type: UsageTypeSandboxMemoryMB, Limit: 512},
			UsageLimit{Type: UsageTypeMessagesMonthly, Limit: 1000},
			UsageLimit{Type: UsageTypeChannels, Limit: 3},
			UsageLimit{Type: UsageTypeChannelBindings, Limit: 3},
			UsageLimit{Type: UsageTypeSpawnDepth, Limit: 1},
			UsageLimit{Type: UsageTypeSessionRetention, Limit: 7},
		)
	case "basic":
		limits = append(limits,
			UsageLimit{Type: UsageTypeRPM, Limit: 600},
			UsageLimit{Type: UsageTypeTokens, Limit: 10000000},
			UsageLimit{Type: UsageTypeRequests, Limit: 100000},
		)
	case "pro":
		limits = append(limits,
			UsageLimit{Type: UsageTypeRPM, Limit: 6000},
			UsageLimit{Type: UsageTypeTokens, Limit: 100000000},
			UsageLimit{Type: UsageTypeRequests, Limit: 1000000},
		)
	case "enterprise":
		limits = append(limits,
			UsageLimit{Type: UsageTypeRPM, Limit: -1},      // Unlimited
			UsageLimit{Type: UsageTypeTokens, Limit: -1},   // Unlimited
			UsageLimit{Type: UsageTypeRequests, Limit: -1}, // Unlimited
		)
	}

	return limits
}

// convertUsageType converts plans.json usage type strings to UsageType constants
func (m *Monitor) convertUsageType(planType string) UsageType {
	switch planType {
	case "RPM":
		return UsageTypeRPM
	case "TOKENS":
		return UsageTypeTokens
	case "REQUESTS":
		return UsageTypeRequests
	case "STORAGE_BYTES":
		return UsageTypeStorageBytes
	case "HOSTED_SITES":
		return UsageTypeHostedSites
	case "HOSTING_STORAGE_BYTES":
		return UsageTypeHostingStorageBytes
	case "CONCURRENT_BROWSERS":
		return UsageTypeConcurrentBrowsers
	case "BROWSER_SESSION_MAX_SECONDS":
		return UsageTypeBrowserSessionMax
	case "DATASET_ITEMS":
		return UsageTypeDatasetItems
	case "EVAL_RUNS_MONTHLY":
		return UsageTypeEvalRunsMonthly
	case "ANNOTATION_QUEUES":
		return UsageTypeAnnotationQueues
	case "PERSISTENT_TROOPERS":
		return UsageTypePersistentTroopers
	case "AGENTS":
		return UsageTypeAgents
	case "PERSISTENT_AGENTS":
		return UsageTypePersistentAgents
	case "CONCURRENT_RUNNING":
		return UsageTypeConcurrentRunning
	case "CONCURRENT_SANDBOXES":
		return UsageTypeConcurrentSandboxes
	case "SANDBOX_MEMORY_MB":
		return UsageTypeSandboxMemoryMB
	case "MESSAGES_MONTHLY":
		return UsageTypeMessagesMonthly
	case "CHANNELS":
		return UsageTypeChannels
	case "CHANNEL_BINDINGS":
		return UsageTypeChannelBindings
	case "SPAWN_DEPTH":
		return UsageTypeSpawnDepth
	case "SESSION_RETENTION_DAYS":
		return UsageTypeSessionRetention
	default:
		logger.Warnf("license_monitor: unknown usage type in plans config: %s", planType)
		return ""
	}
}

// updateFeatureStates updates feature availability based on availableFeatures
// received from the license service (which already resolves tier + per-tenant overrides).
// Falls back to a minimal set if no features have been received yet.
func (m *Monitor) updateFeatureStates() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.licenseState == nil {
		return
	}

	// If we have features from the license service, use those directly.
	// The license service already evaluates tier requirements and per-tenant overrides.
	if len(m.availableFeatures) > 0 {
		for key, release := range m.availableFeatures {
			m.features[key] = FeatureState{
				Enabled:      true,
				RequiredTier: "", // Tier already resolved by license service
			}
			_ = release // metadata available via GetAvailableFeatures()
		}
		return
	}

	// Fallback: no features received from the license service yet (unlicensed
	// instance, or first startup before license sync). Grant the free plan's
	// feature set — Community Edition entitlements — instead of just core_api:
	// blocking features the free plan includes (datasets/evals under
	// "evaluations", persistent agents) would contradict plans.json. Mirrors
	// the free tier; pinned by internal/enterprise/ce_defaults_test.go.
	for _, key := range []string{"core_api", "persistent_agents", "persistent_troopers", "evaluations"} {
		m.features[key] = FeatureState{
			Enabled:      true,
			RequiredTier: "free",
		}
	}
}

// incrementCounters updates all request rate counters using clock-aligned windows.
// Must be called with m.mu held.
func (m *Monitor) incrementCounters(now time.Time) {
	// Clock-aligned second window: truncate to second boundary
	currentSecond := now.Truncate(time.Second)
	if currentSecond != m.usage.LastSecond.Truncate(time.Second) {
		m.usage.LastSecond = now
		m.usage.RequestsInSec = 1
	} else {
		m.usage.RequestsInSec++
	}

	// Clock-aligned minute window: truncate to minute boundary
	currentMinute := now.Truncate(time.Minute)
	if currentMinute != m.usage.LastMinute.Truncate(time.Minute) {
		m.usage.LastMinute = now
		m.usage.RequestsInMin = 1
	} else {
		m.usage.RequestsInMin++
	}

	// Clock-aligned hour window: truncate to hour boundary
	currentHour := now.Truncate(time.Hour)
	if currentHour != m.usage.LastHour.Truncate(time.Hour) {
		m.usage.LastHour = now
		m.usage.RequestsInHour = 1
	} else {
		m.usage.RequestsInHour++
	}

	// Update total counter
	m.usage.TotalRequests++

	// Update peak values
	if m.usage.RequestsInSec > m.usage.RPS {
		m.usage.RPS = m.usage.RequestsInSec
	}
	if m.usage.RequestsInMin > m.usage.RPM {
		m.usage.RPM = m.usage.RequestsInMin
	}
	if m.usage.RequestsInHour > m.usage.RPH {
		m.usage.RPH = m.usage.RequestsInHour
	}
}

// RecordRequest increments usage counters
func (m *Monitor) RecordRequest() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.incrementCounters(time.Now())

	// Check if we've exceeded limits
	return m.checkUsageLimits()
}

// RecordRequestWithMetrics records a request with detailed metrics (tokens, cost, cache)
// and updates spend limits in the license service synchronously for accurate tracking
func (m *Monitor) RecordRequestWithMetrics(metrics RequestMetrics) error {
	m.mu.Lock()

	// Use shared counter logic (clock-aligned windows)
	m.incrementCounters(time.Now())

	// Update token metrics
	m.usage.TotalInputTokens += metrics.InputTokens
	m.usage.TotalOutputTokens += metrics.OutputTokens
	m.usage.TotalTokens += metrics.InputTokens + metrics.OutputTokens

	// Update cost metrics
	m.usage.EstimatedCostUSD += metrics.EstimatedCost
	m.usage.CacheSavingsUSD += metrics.CacheSavings

	// Update cache metrics
	if metrics.CacheHit {
		m.usage.CacheHits++
	} else {
		m.usage.CacheMisses++
	}

	// Update local spend tracking for async enforcement (zero-latency checks)
	// This is the primary mechanism for blocking - no network calls required
	if metrics.EstimatedCost > 0 {
		m.localCurrentSpend += metrics.EstimatedCost
		// Accumulate for batched sync (instead of per-request UpdateSpendUsage calls)
		m.pendingSpendDelta += metrics.EstimatedCost

		// Check if we've exceeded the local limit and set blocked flag
		if m.localLimitEnabled && m.localLimitAction == SpendLimitActionBlock {
			if m.localCurrentSpend >= m.localLimitAmount && !m.spendBlocked {
				m.spendBlocked = true
				m.spendBlockedReason = fmt.Sprintf("Spend limit exceeded. Current: $%.2f, Limit: $%.2f", m.localCurrentSpend, m.localLimitAmount)
				logger.Errorf("license_monitor: LOCAL SPEND BLOCKED - current: $%.2f, limit: $%.2f", m.localCurrentSpend, m.localLimitAmount)
			}
		}
	}

	// Check local usage limits
	usageErr := m.checkUsageLimits()

	// Capture what we need for Redis persistence before releasing lock
	estimatedCost := metrics.EstimatedCost
	instID := m.instanceID
	procID := m.processID
	redisClient := m.redisClient

	m.mu.Unlock()

	// Persist pending spend to Redis atomically (best-effort, non-blocking)
	// This survives gateway restarts - the batched sync loop will send it to license service
	if estimatedCost > 0 && instID != "" && redisClient != nil && redisClient.IsConnected() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			key := PendingSpendRedisKeyPrefix + instID + ":" + procID
			if _, err := redisClient.Client().IncrByFloat(ctx, key, estimatedCost).Result(); err != nil {
				logger.WithError(err).Warn("license_monitor: failed to persist pending spend to Redis")
			} else {
				// Set TTL to ensure cleanup (48 hours)
				_ = redisClient.Client().Expire(ctx, key, 48*time.Hour).Err()
			}
		}()
	}

	return usageErr
}

// checkUsageLimits verifies current usage against license limits
func (m *Monitor) checkUsageLimits() error {
	if m.licenseState == nil {
		return nil
	}

	// Check usage limits (RPM, tokens, etc.)
	for _, limit := range m.licenseState.UsageLimits {
		if limit.Limit == -1 {
			continue // Unlimited
		}

		var current int64
		switch limit.Type {
		case UsageTypeRPM:
			current = m.usage.RequestsInMin
		case UsageTypeRPS:
			current = m.usage.RequestsInSec
		case UsageTypeRPH:
			current = m.usage.RequestsInHour
		case UsageTypeRequests:
			current = m.usage.TotalRequests
		case UsageTypeTokens:
			current = m.usage.TotalTokens
		}

		if current >= limit.Limit {
			return fmt.Errorf("usage limit exceeded: %s limit is %d, current usage is %d", limit.Type, limit.Limit, current)
		}
	}

	// Check spend limits
	if err := m.checkSpendLimits(); err != nil {
		return err
	}

	return nil
}

// checkSpendLimits verifies current spend against configured spend limits.
// Uses localCurrentSpend as the single source of truth for spend enforcement.
func (m *Monitor) checkSpendLimits() error {
	if m.licenseState == nil {
		return nil
	}

	for _, limit := range m.licenseState.SpendLimits {
		if !limit.Enabled {
			continue
		}

		// Only check estimated cost limits locally (actual billing is checked server-side)
		if limit.LimitType != SpendLimitTypeEstimatedCost {
			continue
		}

		// Use localCurrentSpend as canonical value (synced with server + local increments)
		currentSpend := m.localCurrentSpend

		if currentSpend >= limit.LimitAmount {
			switch limit.ActionOnExceed {
			case SpendLimitActionBlock:
				return &SpendLimitExceededError{
					CurrentSpend: currentSpend,
					LimitAmount:  limit.LimitAmount,
					Reason: fmt.Sprintf("spend limit exceeded: %s limit is $%.2f, current spend is $%.2f",
						limit.Period, limit.LimitAmount, currentSpend),
				}
			case SpendLimitActionWarn:
				logger.Warnf("license_monitor: spend limit warning - %s limit is $%.2f, current spend is $%.2f",
					limit.Period, limit.LimitAmount, currentSpend)
			case SpendLimitActionNotify:
				// Notification is handled server-side
				logger.Debugf("license_monitor: spend limit notification - %s limit is $%.2f, current spend is $%.2f",
					limit.Period, limit.LimitAmount, currentSpend)
			}
		}
	}

	return nil
}

// CheckSpendLimitBeforeRequest checks if a request with the given estimated cost should be allowed
// This is called before processing a request to pre-check spend limits
func (m *Monitor) CheckSpendLimitBeforeRequest(estimatedCost float64) (allowed bool, message string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.licenseState == nil {
		return true, ""
	}

	for _, limit := range m.licenseState.SpendLimits {
		if !limit.Enabled || limit.LimitType != SpendLimitTypeEstimatedCost {
			continue
		}

		projectedSpend := m.localCurrentSpend + estimatedCost
		remaining := limit.LimitAmount - m.localCurrentSpend

		if projectedSpend > limit.LimitAmount {
			if limit.ActionOnExceed == SpendLimitActionBlock {
				return false, fmt.Sprintf("Request would exceed %s spend limit of $%.2f (remaining: $%.2f, request cost: $%.2f)",
					limit.Period, limit.LimitAmount, remaining, estimatedCost)
			}
		}
	}

	return true, ""
}

// GetSpendLimitStatus returns the current spend limit status
func (m *Monitor) GetSpendLimitStatus() (currentSpend float64, limitAmount float64, remaining float64, hasLimit bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.licenseState == nil {
		return 0, 0, 0, false
	}

	for _, limit := range m.licenseState.SpendLimits {
		if !limit.Enabled || limit.LimitType != SpendLimitTypeEstimatedCost {
			continue
		}

		currentSpend = m.localCurrentSpend
		limitAmount = limit.LimitAmount
		remaining = limit.LimitAmount - currentSpend
		if remaining < 0 {
			remaining = 0
		}
		return currentSpend, limitAmount, remaining, true
	}

	return 0, 0, 0, false
}

// FetchSpendLimits fetches spend limits from the license service
func (m *Monitor) FetchSpendLimits(ctx context.Context) error {
	if m.licenseServiceURL == "" || m.organizationID == "" {
		return nil
	}

	// Rate limit fetches to avoid hammering the service
	m.mu.RLock()
	if m.spendLimitsFetched && time.Since(m.spendLimitsLastFetch) < 5*time.Minute {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	httpClient := &http.Client{Timeout: 10 * time.Second}
	licenseClient := licenseconnect.NewLicenseServiceClient(httpClient, m.licenseServiceURL)

	req := connect.NewRequest(&licv1.GetSpendLimitsRequest{
		OrganizationId: m.organizationID,
		InstanceId:     m.instanceID,
	})

	resp, err := licenseClient.GetSpendLimits(ctx, req)
	if err != nil {
		logger.WithError(err).Warn("license_monitor: failed to fetch spend limits")
		return err
	}

	// Convert proto to local types
	spendLimits := make([]SpendLimit, len(resp.Msg.GetSpendLimits()))
	for i, protoLimit := range resp.Msg.GetSpendLimits() {
		spendLimits[i] = SpendLimit{
			ID:             protoLimit.GetId(),
			OrganizationID: protoLimit.GetOrganizationId(),
			InstanceID:     protoLimit.GetInstanceId(),
			LimitType:      protoSpendLimitTypeToLocal(protoLimit.GetLimitType()),
			LimitAmount:    protoLimit.GetLimitAmount(),
			Period:         protoSpendLimitPeriodToLocal(protoLimit.GetPeriod()),
			ActionOnExceed: protoSpendLimitActionToLocal(protoLimit.GetActionOnExceed()),
			CurrentSpend:   protoLimit.GetCurrentSpend(),
			Enabled:        protoLimit.GetEnabled(),
		}
	}

	m.mu.Lock()
	if m.licenseState == nil {
		m.licenseState = &LicenseState{}
	}
	m.licenseState.SpendLimits = spendLimits
	m.spendLimitsFetched = true
	m.spendLimitsLastFetch = time.Now()
	m.mu.Unlock()

	logger.Debugf("license_monitor: fetched %d spend limits for org %s", len(spendLimits), m.organizationID)
	return nil
}

// Helper functions to convert proto types to local types
func protoSpendLimitTypeToLocal(t licv1.SpendLimitType) SpendLimitType {
	switch t {
	case licv1.SpendLimitType_SPEND_LIMIT_TYPE_ESTIMATED_COST:
		return SpendLimitTypeEstimatedCost
	case licv1.SpendLimitType_SPEND_LIMIT_TYPE_ACTUAL_BILLING:
		return SpendLimitTypeActualBilling
	default:
		return SpendLimitTypeEstimatedCost
	}
}

func protoSpendLimitPeriodToLocal(p licv1.SpendLimitPeriod) SpendLimitPeriod {
	switch p {
	case licv1.SpendLimitPeriod_SPEND_LIMIT_PERIOD_DAILY:
		return SpendLimitPeriodDaily
	case licv1.SpendLimitPeriod_SPEND_LIMIT_PERIOD_MONTHLY:
		return SpendLimitPeriodMonthly
	default:
		return SpendLimitPeriodMonthly
	}
}

func protoSpendLimitActionToLocal(a licv1.SpendLimitAction) SpendLimitAction {
	switch a {
	case licv1.SpendLimitAction_SPEND_LIMIT_ACTION_BLOCK:
		return SpendLimitActionBlock
	case licv1.SpendLimitAction_SPEND_LIMIT_ACTION_WARN:
		return SpendLimitActionWarn
	case licv1.SpendLimitAction_SPEND_LIMIT_ACTION_NOTIFY:
		return SpendLimitActionNotify
	default:
		return SpendLimitActionBlock
	}
}

// resetUsageCounters resets time-based counters when the month changes.
// Compares current month to last-reset month instead of checking exact time,
// so resets are not missed if the gateway is down at midnight on the 1st.
func (m *Monitor) resetUsageCounters() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// Reset monthly counter when the month changes
	lastResetYear, lastResetMonth, _ := m.usage.LastReset.Date()
	currentYear, currentMonth, _ := now.Date()
	if currentYear != lastResetYear || currentMonth != lastResetMonth {
		logger.Infof("license_monitor: resetting monthly usage counter (previous: %d requests)", m.usage.TotalRequests)
		m.usage.TotalRequests = 0
		m.usage.TotalTokens = 0
		m.usage.TotalInputTokens = 0
		m.usage.TotalOutputTokens = 0
		// Cost/cache are period metrics too — reset them with the token
		// counters so "this period" cost reflects the current month rather
		// than a lifetime total (this also feeds the cloud usage tab).
		m.usage.EstimatedCostUSD = 0
		m.usage.CacheSavingsUSD = 0
		m.usage.CacheHits = 0
		m.usage.CacheMisses = 0
		m.usage.LastReset = now
	}
}

// IsLocked returns whether the gateway is currently locked
func (m *Monitor) IsLocked() (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.locked, m.lockReason
}

// IsSpendBlocked returns whether requests are blocked due to spend limit exceeded.
// This is a zero-latency check using local cached state - no network calls.
// It checks both the explicit spendBlocked flag AND the local spend vs limit.
func (m *Monitor) IsSpendBlocked() (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check explicit blocked flag (set by previous UpdateSpendUsage response or background sync)
	if m.spendBlocked {
		logger.Debugf("license_monitor: IsSpendBlocked check returning true (flag): %s", m.spendBlockedReason)
		return true, m.spendBlockedReason
	}

	// Check local spend tracking for real-time enforcement
	if m.localLimitEnabled && m.localLimitAction == SpendLimitActionBlock {
		if m.localCurrentSpend >= m.localLimitAmount {
			reason := fmt.Sprintf("Spend limit exceeded. Current: $%.2f, Limit: $%.2f", m.localCurrentSpend, m.localLimitAmount)
			logger.Debugf("license_monitor: IsSpendBlocked check returning true (local): %s", reason)
			return true, reason
		}
	}

	return false, ""
}

// setLocked sets the gateway lock state
func (m *Monitor) setLocked(locked bool, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Only log if state changed
	if m.locked != locked {
		if locked {
			logger.Errorf("license_monitor: GATEWAY LOCKED - %s", reason)
		} else {
			logger.Info("license_monitor: gateway unlocked")
		}
	}

	m.locked = locked
	m.lockReason = reason
}

// IsFeatureEnabled checks if a feature is available in the current license.
// It checks the features map which is populated from the license service's
// tier-evaluated and per-tenant-overridden feature set.
func (m *Monitor) IsFeatureEnabled(feature string) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Primary: check the features map (populated from availableFeatures)
	state, ok := m.features[feature]
	if !ok {
		// If features map doesn't have it, check availableFeatures directly
		// (covers the case where features map hasn't been synced yet)
		if _, available := m.availableFeatures[feature]; available {
			return true, ""
		}
		return false, fmt.Sprintf("feature not available: %s", feature)
	}

	if !state.Enabled {
		return false, state.LockedReason
	}

	return true, ""
}

// GetLicenseState returns the current license state
func (m *Monitor) GetLicenseState() *LicenseState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.licenseState == nil {
		return nil
	}

	// Return a copy to prevent mutation
	stateCopy := *m.licenseState
	return &stateCopy
}

// GetUsageStats returns current usage statistics
func (m *Monitor) GetUsageStats() UsageStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usage
}

// GetOrganizationID returns the organization ID associated with this monitor
func (m *Monitor) GetOrganizationID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.organizationID
}

// GetInstanceID returns the instance ID associated with this monitor
func (m *Monitor) GetInstanceID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instanceID
}

// SetOrganizationAndInstanceID updates the organization and instance IDs
// This should be called after activation when the IDs become available
func (m *Monitor) SetOrganizationAndInstanceID(orgID, instID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.organizationID = orgID
	m.instanceID = instID
	logger.WithFields(map[string]interface{}{
		"organization_id": orgID,
		"instance_id":     instID,
	}).Debug("license_monitor: updated organization and instance IDs")
}

// SetM2MCredentials sets the signing key for M2M authenticated spend limit updates.
// Uses JWT-based M2M authentication where instance_id is the client_id in the token,
// allowing the License Service to look up the correct signing key for verification.
func (m *Monitor) SetM2MCredentials(signingKey []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signingKey = signingKey

	// Create M2M authenticated client if we have all required info
	if len(signingKey) >= 32 && m.instanceID != "" && m.licenseServiceURL != "" {
		// Create JWT-based M2M provider using the activation-derived signing key
		// The instance_id becomes the client_id in the JWT, allowing the License Service
		// to look up the correct key using InstanceLookup(instance_id)
		simpleConfig := &m2m.SimpleConfig{
			SigningKey: signingKey,
			Issuer:     "everstack",
			Audience:   "everstack-services",
			TokenTTL:   5 * time.Minute,
		}
		// Include gateway scopes so the license service allows the request
		provider, err := m2m.NewSimpleTokenProviderWithScopes(simpleConfig, m.instanceID, GatewayM2MScopes)
		if err != nil {
			logger.WithError(err).Warn("license_monitor: failed to create M2M provider for spend tracking")
			return
		}

		httpClient := m2m.NewHTTPClient(provider, 10*time.Second)
		m.spendLimitClient = licenseconnect.NewLicenseServiceClient(httpClient, m.licenseServiceURL)
		logger.Debug("license_monitor: created JWT-based M2M authenticated spend limit client")
	}
}

// Subscribe adds a callback that will be called when license state changes
func (m *Monitor) Subscribe(callback func(LicenseState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribers = append(m.subscribers, callback)
}

// SetAvailableFeatures updates the available features from the license service
func (m *Monitor) SetAvailableFeatures(features map[string]*FeatureRelease) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.availableFeatures = features
	logger.Debugf("license_monitor: updated available features (count=%d)", len(features))
}

// GetAvailableFeatures returns the available features from the license service
func (m *Monitor) GetAvailableFeatures() map[string]*FeatureRelease {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.availableFeatures == nil {
		return make(map[string]*FeatureRelease)
	}
	// Return a copy to prevent mutation
	result := make(map[string]*FeatureRelease, len(m.availableFeatures))
	for k, v := range m.availableFeatures {
		result[k] = v
	}
	return result
}
