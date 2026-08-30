// Package license_monitor provides usage tracking and license state monitoring.
package license_monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/auth/m2m"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	licenseconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1/licenseconnect"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// Default usage sync interval to license service.
	// Usage reports are telemetry (not enforcement) — 5-minute granularity is sufficient.
	defaultUsageSyncInterval = 5 * time.Minute
	// Redis key for pending usage reports
	pendingReportsKey = "everstack:usage:pending"
	// Max retry attempts before dropping
	maxRetryAttempts = 5
	// Base backoff duration for retries
	baseBackoff = 5 * time.Second
	// Redis key for archived (dead-letter) usage reports
	archivedReportsKey = "everstack:usage:archived"
	// TTL for archived reports
	archivedReportsTTL = 7 * 24 * time.Hour
)

// SyncerConfig holds configuration for the usage syncer
type SyncerConfig struct {
	// SyncInterval is how often to sync usage to the license service
	SyncInterval time.Duration
	// LicenseServiceURL is the URL of the license service
	LicenseServiceURL string
	// InstanceID is the gateway instance ID (set after activation)
	InstanceID string
	// RefreshToken is the token for authenticating with the license service (set after activation)
	RefreshToken string
	// SigningKey is the HMAC signing key for M2M authentication (set after activation)
	// DEPRECATED: Use M2MConfig instead
	SigningKey []byte
	// Fingerprint is the device fingerprint for pre-activation tracking
	// Used to report usage for free/trial users before they have an instance ID
	Fingerprint string
	// GatewayVersion identifies the running build in the private Operations
	// inventory. It is sent only with the existing aggregate usage report.
	GatewayVersion string
	// M2MConfig is the new provider-agnostic M2M configuration
	// If set, this takes precedence over SigningKey
	M2MConfig *m2m.Config
}

// LimitsUpdateCallback is called when the License Service returns updated limits
type LimitsUpdateCallback func(limits *licv1.EnforcementLimits, cumulative *licv1.CumulativeUsageStats, exceeded bool)

// UsageSyncer syncs usage metrics from the gateway to the license service
// ResourceCounts is a snapshot of per-instance resource counts that gets
// piggybacked on usage reports for historical chart data in the cloud.
type ResourceCounts struct {
	Agents              int64
	PersistentAgents    int64
	ConcurrentRunning   int64
	ConcurrentSandboxes int64
	DatasetItems        int64
	EvalRunsMonthly     int64
	AnnotationQueues    int64
	ChannelBindings     int64
	MessagesMonthly     int64
	StorageBytes        int64
	// Cumulative sandbox network data transfer this period (bytes).
	// These are flow metrics (period totals), not point-in-time gauges.
	NetworkRxBytes int64
	NetworkTxBytes int64
	// Lifetime-monotonic sandbox compute meters. The central billing service
	// converts these to billing-period deltas using a durable watermark.
	SandboxComputeSeconds    int64
	SandboxComputeCostMicros int64
	// Lifetime-monotonic hosted-browser lease meters. Idle pool capacity is
	// excluded; a lease starts when a session binds and ends on release.
	BrowserRuntimeSeconds    int64
	BrowserRuntimeCostMicros int64
}

type UsageSyncer struct {
	config      SyncerConfig
	client      licenseconnect.InstanceServiceClient
	httpClient  *http.Client
	redis       *cache.RedisClient
	m2mProvider m2m.TokenProvider // New M2M token provider (if configured)

	mu              sync.RWMutex
	lastSyncedStats UsageStats
	lastSyncTime    time.Time
	pendingReports  []pendingReport
	retryCount      int
	started         bool
	stopCh          chan struct{}

	// Callback to get current stats
	getStats func() UsageStats
	// Callback to get current resource counts (may be nil for CE builds)
	getCounts func(context.Context) ResourceCounts
	// Callback when limits are updated from License Service
	onLimitsUpdate LimitsUpdateCallback
}

// SetCountsProvider wires a callback that returns the current per-instance
// resource counts. Called on every sync to include counts in the report.
func (s *UsageSyncer) SetCountsProvider(fn func(context.Context) ResourceCounts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCounts = fn
}

// pendingReport holds a usage report waiting to be sent
type pendingReport struct {
	Report     *licv1.UsageReport `json:"report"`
	CreatedAt  time.Time          `json:"created_at"`
	RetryCount int                `json:"retry_count"`
}

// NewUsageSyncer creates a new usage syncer
func NewUsageSyncer(config SyncerConfig, redisClient *cache.RedisClient, getStats func() UsageStats) *UsageSyncer {
	if config.SyncInterval == 0 {
		config.SyncInterval = defaultUsageSyncInterval
	}

	syncer := &UsageSyncer{
		config:         config,
		redis:          redisClient,
		pendingReports: make([]pendingReport, 0),
		stopCh:         make(chan struct{}),
		getStats:       getStats,
	}

	// Create HTTP client with appropriate M2M authentication
	syncer.httpClient = syncer.createHTTPClient()

	// Create license service client
	if config.LicenseServiceURL != "" {
		syncer.client = licenseconnect.NewInstanceServiceClient(syncer.httpClient, config.LicenseServiceURL)
	}

	return syncer
}

// createHTTPClient creates an HTTP client.
// If M2M provider is configured (after key sync), it uses M2M authentication.
// Otherwise, it falls back to the public endpoint pattern with in-payload credentials.
func (s *UsageSyncer) createHTTPClient() *http.Client {
	s.mu.RLock()
	provider := s.m2mProvider
	s.mu.RUnlock()

	if provider != nil {
		// Use M2M-authenticated HTTP client
		return m2m.NewHTTPClient(provider, 30*time.Second)
	}

	// Fall back to plain HTTP - the License Service validates via in-payload credentials
	return &http.Client{Timeout: 30 * time.Second}
}

// SetCredentials updates the instance credentials (called after activation)
// This creates an M2M provider using the activation-derived signing key.
// The instance_id is used as the client_id in M2M tokens, allowing the
// License Service to look up the correct signing key for verification.
func (s *UsageSyncer) SetCredentials(instanceID, refreshToken string, signingKey []byte) {
	s.mu.Lock()
	s.config.InstanceID = instanceID
	s.config.RefreshToken = refreshToken
	s.config.SigningKey = signingKey
	licenseURL := s.config.LicenseServiceURL
	s.mu.Unlock()

	// Create M2M provider using the activation-derived signing key
	// The instance_id becomes the client_id in the JWT, allowing the License Service
	// to look up the correct key using InstanceLookup(instance_id)
	if len(signingKey) >= 32 && instanceID != "" {
		simpleConfig := &m2m.SimpleConfig{
			SigningKey: signingKey,
			Issuer:     "everstack",
			Audience:   "everstack-services",
			TokenTTL:   5 * time.Minute,
		}
		// Use instance_id as the client name - this becomes client_id in the JWT
		// Include gateway scopes so the license service allows the request
		provider, err := m2m.NewSimpleTokenProviderWithScopes(simpleConfig, instanceID, GatewayM2MScopes)
		if err != nil {
			logger.WithError(err).Warn("usage_syncer: failed to create instance M2M provider")
		} else {
			s.mu.Lock()
			s.m2mProvider = provider
			s.mu.Unlock()

			// Recreate HTTP client with M2M
			httpClient := s.createHTTPClient()

			s.mu.Lock()
			s.httpClient = httpClient
			if licenseURL != "" {
				s.client = licenseconnect.NewInstanceServiceClient(s.httpClient, licenseURL)
			}
			s.mu.Unlock()

			logger.WithFields("instance_id", instanceID[:8]+"...").Info("usage_syncer: M2M configured with instance credentials")
		}
	} else {
		logger.Debug("usage_syncer: credentials updated (no M2M - missing key or instance_id)")
	}
}

// SetM2MConfig configures M2M authentication for the usage syncer.
// After the gateway syncs its signing key with the License Service,
// M2M authentication is used instead of public endpoint fallback.
func (s *UsageSyncer) SetM2MConfig(cfg *m2m.Config) {
	if cfg == nil || !cfg.Enabled {
		return
	}

	// Create M2M provider using the "gateway" client
	provider, err := m2m.NewTokenProvider(cfg, "gateway")
	if err != nil {
		logger.WithError(err).Warn("usage_syncer: failed to create M2M provider")
		return
	}

	s.mu.Lock()
	s.m2mProvider = provider
	s.config.M2MConfig = cfg
	licenseURL := s.config.LicenseServiceURL
	s.mu.Unlock()

	// Recreate HTTP client with M2M (outside of lock since createHTTPClient acquires RLock)
	httpClient := s.createHTTPClient()

	s.mu.Lock()
	s.httpClient = httpClient
	if licenseURL != "" {
		s.client = licenseconnect.NewInstanceServiceClient(s.httpClient, licenseURL)
	}
	s.mu.Unlock()

	logger.Info("usage_syncer: M2M authentication configured")
}

// SetFingerprint sets the device fingerprint for pre-activation tracking
func (s *UsageSyncer) SetFingerprint(fingerprint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Fingerprint = fingerprint
}

// IsActivated returns true if the syncer has instance credentials
func (s *UsageSyncer) IsActivated() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.InstanceID != "" && s.config.RefreshToken != ""
}

// SetLicenseServiceURL updates the license service URL
func (s *UsageSyncer) SetLicenseServiceURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.LicenseServiceURL = url
	if url != "" {
		s.client = licenseconnect.NewInstanceServiceClient(s.httpClient, url)
	}
}

// SetLimitsUpdateCallback sets the callback for when limits are updated from License Service
func (s *UsageSyncer) SetLimitsUpdateCallback(cb LimitsUpdateCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLimitsUpdate = cb
}

// Start begins the background sync loop
func (s *UsageSyncer) Start(done <-chan struct{}) {
	if s.started {
		return
	}
	s.started = true

	// Load any pending reports from Redis
	s.loadPendingReports()

	go func() {
		ticker := time.NewTicker(s.config.SyncInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.sync()
			case <-done:
				// Final sync before shutdown
				s.sync()
				close(s.stopCh)
				return
			}
		}
	}()

	logger.Infof("usage_syncer: started with sync interval %v", s.config.SyncInterval)
}

// sync sends accumulated usage metrics to the license service
func (s *UsageSyncer) sync() {
	s.mu.Lock()
	instanceID := s.config.InstanceID
	refreshToken := s.config.RefreshToken
	fingerprint := s.config.Fingerprint
	s.mu.Unlock()

	// Skip if no client configured
	if s.client == nil {
		logger.Debug("usage_syncer: skipping sync - no license service client")
		return
	}

	// Need either (instance_id + refresh_token) for activated instances
	// or fingerprint for free/trial users
	isActivated := instanceID != "" && refreshToken != ""
	hasFingerprint := fingerprint != ""

	if !isActivated && !hasFingerprint {
		logger.Debug("usage_syncer: skipping sync - no credentials or fingerprint")
		return
	}

	// Get current stats
	if s.getStats == nil {
		return
	}
	currentStats := s.getStats()

	// Calculate delta since last sync
	report := s.calculateDelta(currentStats)
	if report == nil {
		logger.Debug("usage_syncer: no usage changes since last sync")
		// Still try to send pending reports
		s.sendPendingReports()
		return
	}

	// Try to send the report
	if err := s.sendReport(instanceID, refreshToken, fingerprint, report); err != nil {
		// Check if this is a permanent error (e.g., license released)
		if isPermanentError(err) {
			logger.WithError(err).Warn("usage_syncer: permanent error, stopping sync (license may have been released)")
			// Clear credentials to stop future sync attempts
			s.mu.Lock()
			s.config.InstanceID = ""
			s.config.RefreshToken = ""
			s.mu.Unlock()
			// Archive pending reports to dead-letter queue instead of dropping them
			s.archivePendingReports(report)
			return
		}
		logger.WithError(err).Warn("usage_syncer: failed to send usage report, queuing for retry")
		s.queueReport(report)
	} else {
		// Update last synced stats on success
		s.mu.Lock()
		s.lastSyncedStats = currentStats
		s.lastSyncTime = time.Now()
		s.retryCount = 0
		s.mu.Unlock()
		if isActivated {
			logger.Debug("usage_syncer: successfully sent usage report (activated)")
		} else {
			logger.Debug("usage_syncer: successfully sent usage report (fingerprint-based)")
		}
	}

	// Try to send any pending reports
	s.sendPendingReports()
}

// calculateDelta computes the usage delta since last sync
func (s *UsageSyncer) calculateDelta(current UsageStats) *licv1.UsageReport {
	s.mu.RLock()
	last := s.lastSyncedStats
	lastTime := s.lastSyncTime
	s.mu.RUnlock()

	// If this is the first sync, use current stats as the delta
	if lastTime.IsZero() {
		lastTime = current.LastReset
	}

	// Calculate deltas
	deltaRequests := current.TotalRequests - last.TotalRequests
	deltaInputTokens := current.TotalInputTokens - last.TotalInputTokens
	deltaOutputTokens := current.TotalOutputTokens - last.TotalOutputTokens
	deltaTokens := current.TotalTokens - last.TotalTokens
	deltaCost := current.EstimatedCostUSD - last.EstimatedCostUSD
	deltaSavings := current.CacheSavingsUSD - last.CacheSavingsUSD
	deltaCacheHits := current.CacheHits - last.CacheHits
	deltaCacheMisses := current.CacheMisses - last.CacheMisses

	s.mu.RLock()
	getCounts := s.getCounts
	s.mu.RUnlock()

	// Resource meters change independently from LLM traffic. When a resource
	// provider is installed, send the regular heartbeat even if there were no
	// model requests; otherwise an idle-but-allocated sandbox would never be
	// reported or billed.
	if deltaRequests == 0 && deltaInputTokens == 0 && deltaOutputTokens == 0 && getCounts == nil {
		return nil
	}

	report := &licv1.UsageReport{
		TotalRequests:    deltaRequests,
		InputTokens:      deltaInputTokens,
		OutputTokens:     deltaOutputTokens,
		TotalTokens:      deltaTokens,
		EstimatedCostUsd: deltaCost,
		CacheSavingsUsd:  deltaSavings,
		CacheHits:        deltaCacheHits,
		CacheMisses:      deltaCacheMisses,
		PeakRpm:          current.RPM,
		PeakRps:          current.RPS,
		PeakRph:          current.RPH,
		PeriodStart:      timestamppb.New(lastTime),
		PeriodEnd:        timestamppb.New(time.Now()),
		// Cumulative period totals (since last reset), sourced from the
		// monitor's running stats. These feed usage_totals for the cloud
		// billing headline numbers; the delta fields above feed Stripe.
		CumulativeTotalTokens:        current.TotalTokens,
		CumulativeTotalRequests:      current.TotalRequests,
		CumulativeInputTokens:        current.TotalInputTokens,
		CumulativeOutputTokens:       current.TotalOutputTokens,
		CumulativeCostMicros:         int64(current.EstimatedCostUSD * 1_000_000),
		CumulativeCacheSavingsMicros: int64(current.CacheSavingsUSD * 1_000_000),
		CumulativeCacheHits:          current.CacheHits,
		CumulativeCacheMisses:        current.CacheMisses,
	}

	if getCounts != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		counts := getCounts(ctx)
		cancel()
		report.AgentsCount = counts.Agents
		report.PersistentAgentsCount = counts.PersistentAgents
		report.ConcurrentRunningCount = counts.ConcurrentRunning
		report.ConcurrentSandboxesCount = counts.ConcurrentSandboxes
		report.DatasetItemsCount = counts.DatasetItems
		report.EvalRunsMonthlyCount = counts.EvalRunsMonthly
		report.AnnotationQueuesCount = counts.AnnotationQueues
		report.ChannelBindingsCount = counts.ChannelBindings
		report.MessagesMonthlyCount = counts.MessagesMonthly
		report.StorageBytes = counts.StorageBytes
		// Sandbox network bytes are period-cumulative flows, not gauges —
		// route them to the cumulative fields so billing stores them in
		// usage_totals (summed across instances), not usage_snapshots.
		report.CumulativeNetworkRxBytes = counts.NetworkRxBytes
		report.CumulativeNetworkTxBytes = counts.NetworkTxBytes
		report.CumulativeSandboxComputeSeconds = counts.SandboxComputeSeconds
		report.CumulativeSandboxComputeCostMicros = counts.SandboxComputeCostMicros
		report.CumulativeBrowserRuntimeSeconds = counts.BrowserRuntimeSeconds
		report.CumulativeBrowserRuntimeCostMicros = counts.BrowserRuntimeCostMicros
	}

	return report
}

// sendReport sends a usage report to the license service
// For activated instances: use instanceID + refreshToken
// For free/trial (pre-activation): use fingerprint only
func (s *UsageSyncer) sendReport(instanceID, refreshToken, fingerprint string, report *licv1.UsageReport) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reqMsg := buildReportUsageRequest(
		instanceID, refreshToken, fingerprint, s.config.GatewayVersion, report,
	)

	req := connect.NewRequest(reqMsg)

	resp, err := s.client.ReportUsage(ctx, req)
	if err != nil {
		return err
	}

	if !resp.Msg.GetAccepted() {
		logger.Warnf("usage_syncer: report not accepted: %s", resp.Msg.GetMessage())
	}

	// Process enforcement limits from response
	s.processLimitsFromResponse(resp.Msg)

	return nil
}

func buildReportUsageRequest(
	instanceID, refreshToken, fingerprint, gatewayVersion string,
	report *licv1.UsageReport,
) *licv1.ReportUsageRequest {
	request := &licv1.ReportUsageRequest{
		Usage:          report,
		GatewayVersion: strings.TrimSpace(gatewayVersion),
	}
	if instanceID != "" && refreshToken != "" {
		request.InstanceId = instanceID
		request.RefreshToken = refreshToken
		request.Fingerprint = fingerprint
	} else {
		request.Fingerprint = fingerprint
	}
	return request
}

// processLimitsFromResponse processes enforcement limits returned by the License Service
func (s *UsageSyncer) processLimitsFromResponse(resp *licv1.ReportUsageResponse) {
	// Get the callback under lock
	s.mu.RLock()
	callback := s.onLimitsUpdate
	s.mu.RUnlock()

	if callback == nil {
		return
	}

	// Only process if limits were returned
	if resp.GetLimits() == nil {
		return
	}

	limits := resp.GetLimits()
	cumulative := resp.GetCumulativeUsage()
	exceeded := resp.GetLimitsExceeded()

	// Log the limits for debugging
	if limits.GetIsTrial() {
		logger.WithFields(
			"daily_requests", limits.GetDailyRequestLimit(),
			"monthly_requests", limits.GetMonthlyRequestLimit(),
			"rpm", limits.GetRpmLimit(),
			"trial_expires", limits.GetTrialExpiresAt().AsTime(),
			"limits_exceeded", exceeded,
		).Debug("usage_syncer: received trial limits from license service")
	} else {
		logger.WithFields(
			"monthly_requests", limits.GetMonthlyRequestLimit(),
			"rpm", limits.GetRpmLimit(),
			"limits_exceeded", exceeded,
		).Debug("usage_syncer: received plan limits from license service")
	}

	// Invoke the callback to update local enforcement
	callback(limits, cumulative, exceeded)
}

// queueReport adds a failed report to the retry queue
func (s *UsageSyncer) queueReport(report *licv1.UsageReport) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pending := pendingReport{
		Report:     report,
		CreatedAt:  time.Now(),
		RetryCount: 0,
	}
	s.pendingReports = append(s.pendingReports, pending)

	// Persist to Redis for durability
	s.savePendingReports()
}

// sendPendingReports attempts to send queued reports
func (s *UsageSyncer) sendPendingReports() {
	s.mu.Lock()
	instanceID := s.config.InstanceID
	refreshToken := s.config.RefreshToken
	fingerprint := s.config.Fingerprint
	pending := s.pendingReports
	s.pendingReports = make([]pendingReport, 0)
	s.mu.Unlock()

	// Can send if activated or have fingerprint
	isActivated := instanceID != "" && refreshToken != ""
	hasFingerprint := fingerprint != ""

	if len(pending) == 0 || (!isActivated && !hasFingerprint) {
		return
	}

	var stillPending []pendingReport
	for _, p := range pending {
		if err := s.sendReport(instanceID, refreshToken, fingerprint, p.Report); err != nil {
			// Check if this is a permanent error
			if isPermanentError(err) {
				logger.WithError(err).Warn("usage_syncer: permanent error on pending report, archiving all pending")
				// Clear credentials and archive pending reports
				s.mu.Lock()
				s.config.InstanceID = ""
				s.config.RefreshToken = ""
				s.mu.Unlock()
				s.archivePendingReports(nil)
				return
			}
			p.RetryCount++
			if p.RetryCount < maxRetryAttempts {
				stillPending = append(stillPending, p)
				logger.WithError(err).Warnf("usage_syncer: retry %d/%d for pending report", p.RetryCount, maxRetryAttempts)
			} else {
				logger.WithError(err).Error("usage_syncer: dropping report after max retries")
			}
		} else {
			logger.Debug("usage_syncer: successfully sent pending report")
		}
	}

	if len(stillPending) > 0 {
		s.mu.Lock()
		s.pendingReports = append(s.pendingReports, stillPending...)
		s.savePendingReports()
		s.mu.Unlock()
	} else {
		// Clear from Redis
		s.clearPendingReports()
	}
}

// savePendingReports persists pending reports to Redis
func (s *UsageSyncer) savePendingReports() {
	if s.redis == nil || !s.redis.IsConnected() {
		return
	}

	data, err := json.Marshal(s.pendingReports)
	if err != nil {
		logger.WithError(err).Warn("usage_syncer: failed to marshal pending reports")
		return
	}

	ctx := context.Background()
	if err := s.redis.Client().Set(ctx, pendingReportsKey, data, 24*time.Hour).Err(); err != nil {
		logger.WithError(err).Warn("usage_syncer: failed to save pending reports to Redis")
	}
}

// loadPendingReports loads pending reports from Redis
func (s *UsageSyncer) loadPendingReports() {
	if s.redis == nil || !s.redis.IsConnected() {
		return
	}

	ctx := context.Background()
	data, err := s.redis.Client().Get(ctx, pendingReportsKey).Bytes()
	if err != nil {
		if err != redis.Nil {
			logger.WithError(err).Warn("usage_syncer: failed to load pending reports from Redis")
		}
		return
	}

	var pending []pendingReport
	if err := json.Unmarshal(data, &pending); err != nil {
		logger.WithError(err).Warn("usage_syncer: failed to unmarshal pending reports")
		return
	}

	s.mu.Lock()
	s.pendingReports = pending
	s.mu.Unlock()

	if len(pending) > 0 {
		logger.Infof("usage_syncer: loaded %d pending reports from Redis", len(pending))
	}
}

// archivePendingReports moves pending reports to a dead-letter Redis key before clearing.
// This preserves usage data that would otherwise be lost on permanent errors.
func (s *UsageSyncer) archivePendingReports(currentReport *licv1.UsageReport) {
	// Collect all reports to archive
	s.mu.RLock()
	pending := make([]pendingReport, len(s.pendingReports))
	copy(pending, s.pendingReports)
	s.mu.RUnlock()

	// Add current report if provided
	if currentReport != nil {
		pending = append(pending, pendingReport{
			Report:    currentReport,
			CreatedAt: time.Now(),
		})
	}

	totalCount := len(pending)

	// Try to archive to Redis
	if s.redis != nil && s.redis.IsConnected() && totalCount > 0 {
		data, err := json.Marshal(pending)
		if err != nil {
			logger.WithError(err).Error("usage_syncer: failed to marshal reports for archival (data loss)")
		} else {
			ctx := context.Background()
			// Append to existing archived reports (use RPUSH with JSON)
			if err := s.redis.Client().Set(ctx, archivedReportsKey, data, archivedReportsTTL).Err(); err != nil {
				logger.WithError(err).Error("usage_syncer: failed to archive pending reports to Redis (data loss)")
			} else {
				logger.Warnf("usage_syncer: archived %d pending reports to dead-letter queue (key: %s, TTL: %v)",
					totalCount, archivedReportsKey, archivedReportsTTL)
			}
		}
	} else if totalCount > 0 {
		logger.Errorf("usage_syncer: Redis unavailable, dropping %d pending reports (data loss)", totalCount)
	}

	// Clear the working queue
	s.clearPendingReports()
}

// clearPendingReports removes pending reports from Redis
func (s *UsageSyncer) clearPendingReports() {
	if s.redis == nil || !s.redis.IsConnected() {
		return
	}

	ctx := context.Background()
	if err := s.redis.Client().Del(ctx, pendingReportsKey).Err(); err != nil {
		logger.WithError(err).Warn("usage_syncer: failed to clear pending reports from Redis")
	}
}

// GetSyncStatus returns the current sync status
func (s *UsageSyncer) GetSyncStatus() (lastSync time.Time, pendingCount int, retryCount int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSyncTime, len(s.pendingReports), s.retryCount
}

// isPermanentError returns true if the error indicates a permanent failure
// that should not be retried (e.g., license released, invalid credentials)
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	// Check for license released error
	if strings.Contains(errMsg, "license has been released") {
		return true
	}
	// Check for Connect error codes that indicate permanent failures
	code := connect.CodeOf(err)
	if code == connect.CodePermissionDenied {
		// Permission denied for released licenses
		if strings.Contains(errMsg, "released") {
			return true
		}
	}
	if code == connect.CodeUnauthenticated {
		// Invalid refresh token - credentials are no longer valid
		return true
	}
	return false
}
