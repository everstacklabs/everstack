package metrics

import (
	"sync"
	"time"
)

// PercentileCalculator tracks request latencies in a sliding window
// and calculates performance percentiles (p50, p95, p99) in real-time
type PercentileCalculator struct {
	mu        sync.RWMutex
	window    time.Duration
	latencies []latencyEntry
	maxSize   int
}

type latencyEntry struct {
	timestamp time.Time
	latencyMs float64
}

// NewPercentileCalculator creates a new percentile calculator
// window: how far back to look for percentile calculations
// maxSize: maximum number of entries to keep (prevents unbounded growth)
func NewPercentileCalculator(window time.Duration, maxSize int) *PercentileCalculator {
	return &PercentileCalculator{
		window:    window,
		latencies: make([]latencyEntry, 0, maxSize),
		maxSize:   maxSize,
	}
}

// RecordLatency records a new latency measurement
func (pc *PercentileCalculator) RecordLatency(latencyMs float64) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	now := time.Now()

	// Add new entry
	pc.latencies = append(pc.latencies, latencyEntry{
		timestamp: now,
		latencyMs: latencyMs,
	})

	// Trim old entries outside the window
	cutoff := now.Add(-pc.window)
	startIdx := 0
	for i, entry := range pc.latencies {
		if entry.timestamp.After(cutoff) {
			startIdx = i
			break
		}
	}
	pc.latencies = pc.latencies[startIdx:]

	// Enforce max size
	if len(pc.latencies) > pc.maxSize {
		pc.latencies = pc.latencies[len(pc.latencies)-pc.maxSize:]
	}
}

// GetPercentiles calculates p50, p95, and p99 from recent latencies
// Returns (p50, p95, p99, count)
func (pc *PercentileCalculator) GetPercentiles() (p50, p95, p99 float64, count int) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	count = len(pc.latencies)
	if count == 0 {
		return 0, 0, 0, 0
	}

	// Copy latencies for sorting
	sorted := make([]float64, count)
	for i, entry := range pc.latencies {
		sorted[i] = entry.latencyMs
	}

	// Simple insertion sort (good enough for small windows)
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}

	p50 = percentile(sorted, 0.50)
	p95 = percentile(sorted, 0.95)
	p99 = percentile(sorted, 0.99)

	return p50, p95, p99, count
}

// percentile calculates the percentile from a sorted array
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}

	// Linear interpolation between closest ranks
	rank := p * float64(len(sorted)-1)
	lowerIdx := int(rank)
	upperIdx := lowerIdx + 1

	if upperIdx >= len(sorted) {
		return sorted[len(sorted)-1]
	}

	// Interpolate
	weight := rank - float64(lowerIdx)
	return sorted[lowerIdx]*(1-weight) + sorted[upperIdx]*weight
}

// CategorizeLatency categorizes latency into performance buckets
func CategorizeLatency(latencyMs float64) string {
	switch {
	case latencyMs < 100:
		return "excellent"
	case latencyMs < 500:
		return "good"
	case latencyMs < 2000:
		return "acceptable"
	default:
		return "slow"
	}
}

// CategorizeThroughput categorizes throughput into performance buckets
func CategorizeThroughput(tokensPerSec float64) string {
	switch {
	case tokensPerSec > 1000:
		return "high"
	case tokensPerSec > 100:
		return "medium"
	default:
		return "low"
	}
}

// Global percentile calculator instance
var (
	globalPercentileCalculator *PercentileCalculator
	percentileOnce             sync.Once
)

// GetGlobalPercentileCalculator returns the global percentile calculator instance
func GetGlobalPercentileCalculator() *PercentileCalculator {
	percentileOnce.Do(func() {
		// Track last 5 minutes, max 10000 entries
		globalPercentileCalculator = NewPercentileCalculator(5*time.Minute, 10000)
	})
	return globalPercentileCalculator
}

// RecordRequestLatency records a request latency in the global calculator
func RecordRequestLatency(latencyMs float64) {
	GetGlobalPercentileCalculator().RecordLatency(latencyMs)
}

// GetCurrentPercentiles returns current p50, p95, p99 from global calculator
func GetCurrentPercentiles() (p50, p95, p99 float64, count int) {
	return GetGlobalPercentileCalculator().GetPercentiles()
}
