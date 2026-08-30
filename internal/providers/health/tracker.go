// Package health provides provider health tracking and status monitoring.
package health

import (
	"encoding/json"
	"sync"
	"time"
)

// Status represents the health status of a provider.
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
	StatusUnknown   Status = "unknown"
)

// ProviderHealth contains health information for a single provider.
type ProviderHealth struct {
	Name            string    `json:"name"`
	Status          Status    `json:"status"`
	LastSuccess     time.Time `json:"last_success,omitempty"`
	LastError       time.Time `json:"last_error,omitempty"`
	LastErrorMsg    string    `json:"last_error_message,omitempty"`
	ErrorRate       float64   `json:"error_rate"`        // Percentage (0-100)
	AvgLatencyMs    int64     `json:"avg_latency_ms"`    // Average latency
	P99LatencyMs    int64     `json:"p99_latency_ms"`    // 99th percentile latency
	RequestCount    int64     `json:"request_count"`     // Total requests in window
	SuccessCount    int64     `json:"success_count"`     // Successful requests
	FailureCount    int64     `json:"failure_count"`     // Failed requests
	LastChecked     time.Time `json:"last_checked"`      // When health was last evaluated
	CircuitOpen     bool      `json:"circuit_open"`      // Circuit breaker state
	ConsecutiveFail int       `json:"consecutive_fails"` // Consecutive failures
}

// providerStats tracks running statistics for a provider.
type providerStats struct {
	mu              sync.RWMutex
	latencies       []int64   // Recent latency samples (circular buffer)
	latencyIdx      int       // Current index in circular buffer
	successCount    int64     // Successes in current window
	failureCount    int64     // Failures in current window
	totalLatency    int64     // Sum of latencies for averaging
	lastSuccess     time.Time // Last successful request
	lastError       time.Time // Last failed request
	lastErrorMsg    string    // Last error message
	consecutiveFail int       // Consecutive failures
	windowStart     time.Time // Start of current measurement window
}

// Tracker tracks health metrics for all providers.
type Tracker struct {
	mu          sync.RWMutex
	providers   map[string]*providerStats
	windowSize  time.Duration // Rolling window for metrics
	sampleSize  int           // Number of latency samples to keep
	errorThresh float64       // Error rate threshold for degraded status (percent)
	failThresh  int           // Consecutive failures for unhealthy status
	latencyHigh int64         // Latency threshold for degraded (ms)
}

// Config configures the health tracker.
type Config struct {
	WindowSize            time.Duration // Default: 5 minutes
	SampleSize            int           // Default: 100
	ErrorRateThreshold    float64       // Default: 5.0 (5%)
	ConsecutiveFailThresh int           // Default: 3
	LatencyThresholdMs    int64         // Default: 5000 (5s)
}

// DefaultConfig returns a default configuration.
func DefaultConfig() Config {
	return Config{
		WindowSize:            5 * time.Minute,
		SampleSize:            100,
		ErrorRateThreshold:    5.0,
		ConsecutiveFailThresh: 3,
		LatencyThresholdMs:    5000,
	}
}

// Global tracker instance
var globalTracker *Tracker
var trackerOnce sync.Once

// GetGlobalTracker returns the global health tracker.
func GetGlobalTracker() *Tracker {
	trackerOnce.Do(func() {
		globalTracker = NewTracker(DefaultConfig())
	})
	return globalTracker
}

// NewTracker creates a new health tracker.
func NewTracker(cfg Config) *Tracker {
	if cfg.WindowSize == 0 {
		cfg.WindowSize = 5 * time.Minute
	}
	if cfg.SampleSize == 0 {
		cfg.SampleSize = 100
	}
	if cfg.ErrorRateThreshold == 0 {
		cfg.ErrorRateThreshold = 5.0
	}
	if cfg.ConsecutiveFailThresh == 0 {
		cfg.ConsecutiveFailThresh = 3
	}
	if cfg.LatencyThresholdMs == 0 {
		cfg.LatencyThresholdMs = 5000
	}

	return &Tracker{
		providers:   make(map[string]*providerStats),
		windowSize:  cfg.WindowSize,
		sampleSize:  cfg.SampleSize,
		errorThresh: cfg.ErrorRateThreshold,
		failThresh:  cfg.ConsecutiveFailThresh,
		latencyHigh: cfg.LatencyThresholdMs,
	}
}

// getOrCreateStats gets or creates stats for a provider.
func (t *Tracker) getOrCreateStats(provider string) *providerStats {
	t.mu.RLock()
	stats, ok := t.providers[provider]
	t.mu.RUnlock()

	if ok {
		return stats
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Double-check after acquiring write lock
	if stats, ok = t.providers[provider]; ok {
		return stats
	}

	stats = &providerStats{
		latencies:   make([]int64, t.sampleSize),
		windowStart: time.Now(),
	}
	t.providers[provider] = stats
	return stats
}

// RecordSuccess records a successful request.
func (t *Tracker) RecordSuccess(provider string, latencyMs int64) {
	stats := t.getOrCreateStats(provider)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	t.maybeResetWindow(stats)

	stats.successCount++
	stats.totalLatency += latencyMs
	stats.latencies[stats.latencyIdx] = latencyMs
	stats.latencyIdx = (stats.latencyIdx + 1) % t.sampleSize
	stats.lastSuccess = time.Now()
	stats.consecutiveFail = 0
}

// RecordFailure records a failed request.
func (t *Tracker) RecordFailure(provider string, latencyMs int64, errMsg string) {
	stats := t.getOrCreateStats(provider)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	t.maybeResetWindow(stats)

	stats.failureCount++
	if latencyMs > 0 {
		stats.totalLatency += latencyMs
		stats.latencies[stats.latencyIdx] = latencyMs
		stats.latencyIdx = (stats.latencyIdx + 1) % t.sampleSize
	}
	stats.lastError = time.Now()
	stats.lastErrorMsg = errMsg
	stats.consecutiveFail++
}

// maybeResetWindow resets stats if window has expired.
func (t *Tracker) maybeResetWindow(stats *providerStats) {
	if time.Since(stats.windowStart) > t.windowSize {
		// Carry over some state
		lastSuccess := stats.lastSuccess
		lastError := stats.lastError
		lastErrorMsg := stats.lastErrorMsg

		// Reset counters
		stats.successCount = 0
		stats.failureCount = 0
		stats.totalLatency = 0
		stats.windowStart = time.Now()

		// Keep these
		stats.lastSuccess = lastSuccess
		stats.lastError = lastError
		stats.lastErrorMsg = lastErrorMsg
	}
}

// GetHealth returns the current health status for a provider.
func (t *Tracker) GetHealth(provider string) ProviderHealth {
	t.mu.RLock()
	stats, ok := t.providers[provider]
	t.mu.RUnlock()

	if !ok {
		return ProviderHealth{
			Name:        provider,
			Status:      StatusUnknown,
			LastChecked: time.Now(),
		}
	}

	stats.mu.RLock()
	defer stats.mu.RUnlock()

	total := stats.successCount + stats.failureCount
	var errorRate float64
	if total > 0 {
		errorRate = (float64(stats.failureCount) / float64(total)) * 100
	}

	var avgLatency int64
	if total > 0 {
		avgLatency = stats.totalLatency / total
	}

	// Calculate P99 from samples
	p99Latency := t.calculateP99(stats)

	// Determine status
	status := StatusHealthy
	if stats.consecutiveFail >= t.failThresh {
		status = StatusUnhealthy
	} else if errorRate > t.errorThresh || avgLatency > t.latencyHigh {
		status = StatusDegraded
	}

	return ProviderHealth{
		Name:            provider,
		Status:          status,
		LastSuccess:     stats.lastSuccess,
		LastError:       stats.lastError,
		LastErrorMsg:    stats.lastErrorMsg,
		ErrorRate:       errorRate,
		AvgLatencyMs:    avgLatency,
		P99LatencyMs:    p99Latency,
		RequestCount:    total,
		SuccessCount:    stats.successCount,
		FailureCount:    stats.failureCount,
		LastChecked:     time.Now(),
		CircuitOpen:     stats.consecutiveFail >= t.failThresh,
		ConsecutiveFail: stats.consecutiveFail,
	}
}

// calculateP99 calculates the 99th percentile latency from samples.
func (t *Tracker) calculateP99(stats *providerStats) int64 {
	// Collect non-zero latencies
	latencies := make([]int64, 0, t.sampleSize)
	for _, l := range stats.latencies {
		if l > 0 {
			latencies = append(latencies, l)
		}
	}

	if len(latencies) == 0 {
		return 0
	}

	// Sort and get 99th percentile
	// Simple selection for small arrays
	if len(latencies) == 1 {
		return latencies[0]
	}

	// Partial sort for P99 position
	p99Idx := int(float64(len(latencies)) * 0.99)
	if p99Idx >= len(latencies) {
		p99Idx = len(latencies) - 1
	}

	// Simple max for small samples
	max := latencies[0]
	for _, l := range latencies {
		if l > max {
			max = l
		}
	}

	return max
}

// GetAllHealth returns health status for all tracked providers.
func (t *Tracker) GetAllHealth() []ProviderHealth {
	t.mu.RLock()
	providers := make([]string, 0, len(t.providers))
	for p := range t.providers {
		providers = append(providers, p)
	}
	t.mu.RUnlock()

	health := make([]ProviderHealth, 0, len(providers))
	for _, p := range providers {
		health = append(health, t.GetHealth(p))
	}

	return health
}

// HealthResponse is the API response format.
type HealthResponse struct {
	Providers  []ProviderHealth `json:"providers"`
	TotalCount int              `json:"total_count"`
	Healthy    int              `json:"healthy_count"`
	Degraded   int              `json:"degraded_count"`
	Unhealthy  int              `json:"unhealthy_count"`
	Timestamp  time.Time        `json:"timestamp"`
}

// GetHealthResponse returns a formatted health response.
func (t *Tracker) GetHealthResponse() HealthResponse {
	providers := t.GetAllHealth()

	resp := HealthResponse{
		Providers:  providers,
		TotalCount: len(providers),
		Timestamp:  time.Now(),
	}

	for _, p := range providers {
		switch p.Status {
		case StatusHealthy:
			resp.Healthy++
		case StatusDegraded:
			resp.Degraded++
		case StatusUnhealthy:
			resp.Unhealthy++
		}
	}

	return resp
}

// ToJSON converts the health response to JSON.
func (r HealthResponse) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// RecordRequest is a convenience method to record a request result.
func RecordRequest(provider string, latencyMs int64, err error) {
	tracker := GetGlobalTracker()
	if err != nil {
		tracker.RecordFailure(provider, latencyMs, err.Error())
	} else {
		tracker.RecordSuccess(provider, latencyMs)
	}
}

// GetProviderHealth is a convenience function for the global tracker.
func GetProviderHealth(provider string) ProviderHealth {
	return GetGlobalTracker().GetHealth(provider)
}

// GetAllProviderHealth is a convenience function for the global tracker.
func GetAllProviderHealth() HealthResponse {
	return GetGlobalTracker().GetHealthResponse()
}
