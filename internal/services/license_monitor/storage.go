// Package license_monitor provides usage tracking and license state monitoring.
//
// IMPORTANT: Local Usage Storage vs License Service
//
// This package provides LOCAL usage tracking for:
//   - Real-time rate limiting (RPM, RPS, RPH enforcement)
//   - Displaying current usage in the Admin UI
//   - Temporary persistence across gateway restarts
//
// This is NOT the authoritative source for billing/usage data. The authoritative
// usage data is stored in Everstack's internal infrastructure via the License Service.
// Local storage exists purely for operational purposes and can be reset without
// affecting billing.
//
// Usage events are dispatched to the License Service which stores them in internal
// ClickHouse/Redis that users cannot access or manipulate.
package license_monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/m2m"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/redis/go-redis/v9"
)

const (
	// Redis key prefix for usage metrics (local operational data only)
	usageKeyPrefix = "everstack:usage:"
	// Default TTL for Redis entries (35 days, slightly longer than monthly reset)
	usageDefaultTTL = 35 * 24 * time.Hour
	// Default sync interval for persisting to Redis
	defaultSyncInterval = 10 * time.Second
)

// UsageStorage defines the interface for persisting usage metrics
type UsageStorage interface {
	// Load retrieves usage stats from storage
	Load(ctx context.Context, instanceID string) (*UsageStats, error)
	// Save persists usage stats to storage
	Save(ctx context.Context, instanceID string, stats *UsageStats) error
}

// UsageStorageImpl provides Redis-backed storage with in-memory fallback
// Uses the existing RedisClient from the cache package
type UsageStorageImpl struct {
	redis  *cache.RedisClient // Reuse existing Redis client from cache package
	memory sync.Map           // Concurrent map for in-memory fallback
	ttl    time.Duration
}

// NewUsageStorage creates a new usage storage using the existing Redis client
// If redisClient is nil, it operates in memory-only mode
func NewUsageStorage(redisClient *cache.RedisClient) *UsageStorageImpl {
	return &UsageStorageImpl{
		redis: redisClient,
		ttl:   usageDefaultTTL,
	}
}

// Load retrieves usage stats, trying Redis first then memory
func (s *UsageStorageImpl) Load(ctx context.Context, instanceID string) (*UsageStats, error) {
	key := usageKeyPrefix + instanceID

	// Try Redis first if available
	if s.redis != nil && s.redis.IsConnected() {
		data, err := s.redis.Client().Get(ctx, key).Bytes()
		if err == nil {
			var stats UsageStats
			if err := json.Unmarshal(data, &stats); err == nil {
				// Update memory cache
				s.memory.Store(instanceID, &stats)
				return &stats, nil
			}
		} else if err != redis.Nil {
			logger.Debugf("license_monitor: redis load failed: %v", err)
		}
	}

	// Fallback to memory
	if val, ok := s.memory.Load(instanceID); ok {
		if stats, ok := val.(*UsageStats); ok {
			// Return a copy
			statsCopy := *stats
			return &statsCopy, nil
		}
	}

	return nil, nil
}

// Save persists usage stats to both Redis and memory
func (s *UsageStorageImpl) Save(ctx context.Context, instanceID string, stats *UsageStats) error {
	// Always save to memory first
	statsCopy := *stats
	s.memory.Store(instanceID, &statsCopy)

	// Try Redis if available (best effort)
	if s.redis != nil && s.redis.IsConnected() {
		data, err := json.Marshal(stats)
		if err != nil {
			return nil // Memory is saved, don't fail
		}

		key := usageKeyPrefix + instanceID
		if err := s.redis.Client().Set(ctx, key, data, s.ttl).Err(); err != nil {
			logger.Debugf("license_monitor: redis save failed: %v", err)
			// Don't fail - memory is the fallback
		}
	}

	return nil
}

// HasRedis returns true if Redis storage is available and connected
func (s *UsageStorageImpl) HasRedis() bool {
	return s.redis != nil && s.redis.IsConnected()
}

// PendingSpendKey returns the Redis key for pending spend delta
// Format: everstack:spend:pending:{instanceID}:{processID}
func PendingSpendKey(instanceID, processID string) string {
	return "everstack:spend:pending:" + instanceID + ":" + processID
}

// IncrementPendingSpend atomically adds to the pending spend delta in Redis
// Returns the new total. If Redis is unavailable, returns 0 and an error.
func (s *UsageStorageImpl) IncrementPendingSpend(ctx context.Context, instanceID, processID string, delta float64) (float64, error) {
	if s.redis == nil || !s.redis.IsConnected() {
		return 0, fmt.Errorf("redis not available")
	}

	key := PendingSpendKey(instanceID, processID)
	// INCRBYFLOAT is atomic - perfect for concurrent spend accumulation
	newVal, err := s.redis.Client().IncrByFloat(ctx, key, delta).Result()
	if err != nil {
		return 0, fmt.Errorf("redis incrbyfloat failed: %w", err)
	}

	// Set TTL to ensure cleanup even if gateway never syncs (e.g., crashes permanently)
	// 48 hours gives plenty of time for recovery
	_ = s.redis.Client().Expire(ctx, key, 48*time.Hour).Err()

	return newVal, nil
}

// GetPendingSpend retrieves the current pending spend delta from Redis
// Returns 0 if key doesn't exist or Redis is unavailable
func (s *UsageStorageImpl) GetPendingSpend(ctx context.Context, instanceID, processID string) (float64, error) {
	if s.redis == nil || !s.redis.IsConnected() {
		return 0, fmt.Errorf("redis not available")
	}

	key := PendingSpendKey(instanceID, processID)
	val, err := s.redis.Client().Get(ctx, key).Float64()
	if err == redis.Nil {
		return 0, nil // Key doesn't exist = no pending spend
	}
	if err != nil {
		return 0, fmt.Errorf("redis get failed: %w", err)
	}

	return val, nil
}

// ClearPendingSpend removes the pending spend delta from Redis (after successful sync)
func (s *UsageStorageImpl) ClearPendingSpend(ctx context.Context, instanceID, processID string) error {
	if s.redis == nil || !s.redis.IsConnected() {
		return fmt.Errorf("redis not available")
	}

	key := PendingSpendKey(instanceID, processID)
	if err := s.redis.Client().Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis del failed: %w", err)
	}

	return nil
}

// SetPendingSpend sets the pending spend delta to a specific value (for recovery scenarios)
func (s *UsageStorageImpl) SetPendingSpend(ctx context.Context, instanceID, processID string, value float64) error {
	if s.redis == nil || !s.redis.IsConnected() {
		return fmt.Errorf("redis not available")
	}

	key := PendingSpendKey(instanceID, processID)
	if err := s.redis.Client().Set(ctx, key, value, 48*time.Hour).Err(); err != nil {
		return fmt.Errorf("redis set failed: %w", err)
	}

	return nil
}

// StorageConfig holds configuration for usage storage
type StorageConfig struct {
	InstanceID     string        // Instance ID for keying storage
	GatewayVersion string        // Running Everstack build version
	SyncInterval   time.Duration // How often to sync to Redis
}

// PersistentMonitor wraps Monitor with storage persistence
type PersistentMonitor struct {
	*Monitor
	storage        UsageStorage
	instanceID     string
	gatewayVersion string
	syncInterval   time.Duration
	syncStopCh     chan struct{}
	usageSyncer    *UsageSyncer
	redisClient    *cache.RedisClient
}

// NewPersistentMonitor creates a monitor with storage persistence
func NewPersistentMonitor(monitor *Monitor, storage UsageStorage, cfg StorageConfig) *PersistentMonitor {
	if cfg.SyncInterval == 0 {
		cfg.SyncInterval = defaultSyncInterval
	}

	pm := &PersistentMonitor{
		Monitor:        monitor,
		storage:        storage,
		instanceID:     cfg.InstanceID,
		gatewayVersion: cfg.GatewayVersion,
		syncInterval:   cfg.SyncInterval,
		syncStopCh:     make(chan struct{}),
	}

	return pm
}

// SetRedisClient sets the Redis client for the usage syncer and underlying monitor
func (pm *PersistentMonitor) SetRedisClient(client *cache.RedisClient) {
	pm.redisClient = client
	// Also set on the underlying Monitor for spend persistence
	pm.Monitor.SetRedisClient(client)
}

// InitUsageSyncer initializes the usage syncer for reporting to license service
func (pm *PersistentMonitor) InitUsageSyncer(licenseServiceURL string) {
	if licenseServiceURL == "" {
		return
	}

	syncerCfg := SyncerConfig{
		SyncInterval:      60 * time.Second, // Sync every 60 seconds
		LicenseServiceURL: licenseServiceURL,
		InstanceID:        pm.instanceID,
		Fingerprint:       pm.instanceID, // Use fingerprint as fallback
		GatewayVersion:    pm.gatewayVersion,
	}

	pm.usageSyncer = NewUsageSyncer(syncerCfg, pm.redisClient, func() UsageStats {
		pm.mu.RLock()
		defer pm.mu.RUnlock()
		return pm.usage
	})
}

// SetSyncerCountsProvider wires a callback that returns the current resource
// counts for this instance. Counts are included in every usage report so the
// billing service can record daily snapshots for charts.
func (pm *PersistentMonitor) SetSyncerCountsProvider(fn func(context.Context) ResourceCounts) {
	if pm.usageSyncer != nil {
		pm.usageSyncer.SetCountsProvider(fn)
	}
}

// SetSyncerCredentials updates the syncer credentials after activation
func (pm *PersistentMonitor) SetSyncerCredentials(instanceID, refreshToken string, signingKey []byte) {
	if pm.usageSyncer != nil {
		pm.usageSyncer.SetCredentials(instanceID, refreshToken, signingKey)
	}
}

// SetSyncerM2MConfig sets the M2M configuration for the syncer
func (pm *PersistentMonitor) SetSyncerM2MConfig(cfg *m2m.Config) {
	if pm.usageSyncer != nil {
		pm.usageSyncer.SetM2MConfig(cfg)
	}
}

// SetLimitsUpdateCallback sets the callback for when limits are updated from License Service
// This allows the trial manager or license enforcer to receive authoritative limits
func (pm *PersistentMonitor) SetLimitsUpdateCallback(cb LimitsUpdateCallback) {
	if pm.usageSyncer != nil {
		pm.usageSyncer.SetLimitsUpdateCallback(cb)
	}
}

// Start begins monitoring with storage sync
func (pm *PersistentMonitor) Start(ctx context.Context) {
	// Load persisted state first
	if pm.storage != nil && pm.instanceID != "" {
		if stats, err := pm.storage.Load(ctx, pm.instanceID); err == nil && stats != nil {
			pm.mu.Lock()
			pm.usage = *stats
			pm.mu.Unlock()
			logger.Infof("license_monitor: loaded persisted usage stats (requests=%d)",
				stats.TotalRequests)
		}
	}

	// Start the underlying monitor
	pm.Monitor.Start(ctx)

	// Start periodic sync to local Redis storage
	go pm.syncLoop(ctx)

	// Start usage syncer to report to license service
	if pm.usageSyncer != nil {
		pm.usageSyncer.Start(ctx.Done())
	}
}

// Stop stops the monitor and final sync
func (pm *PersistentMonitor) Stop() {
	// Final sync before stopping
	pm.syncNow()

	close(pm.syncStopCh)
	pm.Monitor.Stop()
}

// syncLoop periodically persists usage stats
func (pm *PersistentMonitor) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(pm.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pm.syncNow()
		case <-pm.syncStopCh:
			return
		case <-ctx.Done():
			pm.syncNow() // Final sync on context cancellation
			return
		}
	}
}

// syncNow persists the current usage stats immediately
func (pm *PersistentMonitor) syncNow() {
	if pm.storage == nil || pm.instanceID == "" {
		return
	}

	pm.mu.RLock()
	stats := pm.usage
	pm.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pm.storage.Save(ctx, pm.instanceID, &stats); err != nil {
		logger.Debugf("license_monitor: failed to sync usage stats: %v", err)
	}
}

// RecordRequest records a request and triggers async sync if needed
func (pm *PersistentMonitor) RecordRequest() error {
	return pm.Monitor.RecordRequest()
}

// RecordRequestWithMetrics records a request with token/cost metrics
func (pm *PersistentMonitor) RecordRequestWithMetrics(metrics RequestMetrics) error {
	return pm.Monitor.RecordRequestWithMetrics(metrics)
}
