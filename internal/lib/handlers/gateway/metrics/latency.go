// Package metrics provides observability utilities for the fast-path gateway.
package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// LatencyTracker provides microsecond-precision latency tracking for each
// stage of the fast-path request pipeline.
//
// Usage:
//
//	tracker := metrics.StartLatencyTracker()
//	defer tracker.RecordTotal()
//
//	// Auth stage
//	tracker.StartStage("auth")
//	// ... do auth ...
//	tracker.EndStage("auth")
//
//	// Routing stage
//	tracker.StartStage("routing")
//	// ... do routing ...
//	tracker.EndStage("routing")
type LatencyTracker struct {
	start       time.Time
	stageStarts map[string]time.Time
	stageTimes  map[string]time.Duration
	mu          sync.Mutex
	recorded    bool
}

// StartLatencyTracker creates a new tracker and starts the overall timer.
func StartLatencyTracker() *LatencyTracker {
	return &LatencyTracker{
		start:       time.Now(),
		stageStarts: make(map[string]time.Time),
		stageTimes:  make(map[string]time.Duration),
	}
}

// StartStage marks the start of a named stage.
func (t *LatencyTracker) StartStage(name string) {
	t.mu.Lock()
	t.stageStarts[name] = time.Now()
	t.mu.Unlock()
}

// EndStage marks the end of a named stage and records the duration.
func (t *LatencyTracker) EndStage(name string) time.Duration {
	end := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if start, ok := t.stageStarts[name]; ok {
		duration := end.Sub(start)
		t.stageTimes[name] = duration
		delete(t.stageStarts, name)

		// Record to global metrics
		globalMetrics.recordStage(name, duration)
		return duration
	}
	return 0
}

// StageDuration returns the recorded duration for a stage.
func (t *LatencyTracker) StageDuration(name string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stageTimes[name]
}

// TotalDuration returns the total elapsed time since tracker creation.
func (t *LatencyTracker) TotalDuration() time.Duration {
	return time.Since(t.start)
}

// RecordTotal records the total request duration to global metrics.
func (t *LatencyTracker) RecordTotal() {
	t.mu.Lock()
	if t.recorded {
		t.mu.Unlock()
		return
	}
	t.recorded = true
	t.mu.Unlock()

	globalMetrics.recordTotal(time.Since(t.start))
}

// AllStages returns all recorded stage durations.
func (t *LatencyTracker) AllStages() map[string]time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make(map[string]time.Duration, len(t.stageTimes))
	for k, v := range t.stageTimes {
		result[k] = v
	}
	return result
}

// Stage names for consistent naming
const (
	StageAuth           = "auth"
	StageRouting        = "routing"
	StageCacheLookup    = "cache_lookup"
	StageCacheStore     = "cache_store"
	StageSemanticLookup = "semantic_lookup"
	StageSemanticStore  = "semantic_store"
	StageProvider       = "provider"
	StageStreaming      = "streaming"
	StageEncode         = "encode"
)

// FastPathMetrics holds aggregated metrics for the fast-path gateway.
type FastPathMetrics struct {
	// Per-stage latency histograms (microseconds)
	authHist       *histogram
	routingHist    *histogram
	cacheHist      *histogram
	semanticHist   *histogram
	streamingHist  *histogram
	totalHist      *histogram
	embeddingsHist *histogram

	// Counters
	authCacheHits       atomic.Uint64
	authCacheMisses     atomic.Uint64
	routerCacheHits     atomic.Uint64
	routerCacheMisses   atomic.Uint64
	exactCacheHits      atomic.Uint64
	exactCacheMisses    atomic.Uint64
	semanticCacheHits   atomic.Uint64
	semanticCacheMisses atomic.Uint64

	// Agent runtime cache counters
	agentCacheHitsExact    atomic.Uint64
	agentCacheHitsSemantic atomic.Uint64
	agentCacheMisses       atomic.Uint64
	agentCacheStores       atomic.Uint64
	agentSemanticStores    atomic.Uint64

	// Request counters
	totalRequests      atomic.Uint64
	fastPathRequests   atomic.Uint64
	legacyRequests     atomic.Uint64
	embeddingsRequests atomic.Uint64

	// Token counters
	inputTokens  atomic.Uint64
	outputTokens atomic.Uint64

	// Cost tracking (in microdollars for precision)
	inputCostMicros   atomic.Uint64
	outputCostMicros  atomic.Uint64
	costSavingsMicros atomic.Uint64
}

var globalMetrics = newFastPathMetrics()

func newFastPathMetrics() *FastPathMetrics {
	return &FastPathMetrics{
		// Buckets in microseconds: 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000
		authHist:       newHistogram([]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000}),
		routingHist:    newHistogram([]float64{1, 5, 10, 25, 50}),
		cacheHist:      newHistogram([]float64{10, 25, 50, 100, 250, 500, 1000}),
		semanticHist:   newHistogram([]float64{100, 250, 500, 1000, 2500, 5000}), // Semantic is slower than exact
		streamingHist:  newHistogram([]float64{10, 25, 50, 100, 250, 500}),
		totalHist:      newHistogram([]float64{100, 250, 500, 1000, 2500, 5000, 10000, 25000, 50000}),
		embeddingsHist: newHistogram([]float64{50000, 100000, 250000, 500000, 1000000, 2500000}), // Embeddings in microseconds (50ms-2.5s)
	}
}

func (m *FastPathMetrics) recordStage(name string, d time.Duration) {
	us := float64(d.Microseconds())
	switch name {
	case StageAuth:
		m.authHist.observe(us)
	case StageRouting:
		m.routingHist.observe(us)
	case StageCacheLookup, StageCacheStore:
		m.cacheHist.observe(us)
	case StageSemanticLookup, StageSemanticStore:
		m.semanticHist.observe(us)
	case StageStreaming, StageEncode:
		m.streamingHist.observe(us)
	}
}

func (m *FastPathMetrics) recordTotal(d time.Duration) {
	m.totalHist.observe(float64(d.Microseconds()))
	m.totalRequests.Add(1)
}

// RecordAuthCacheHit increments the auth cache hit counter.
func RecordAuthCacheHit() {
	globalMetrics.authCacheHits.Add(1)
}

// RecordAuthCacheMiss increments the auth cache miss counter.
func RecordAuthCacheMiss() {
	globalMetrics.authCacheMisses.Add(1)
}

// RecordRouterCacheHit increments the router cache hit counter.
func RecordRouterCacheHit() {
	globalMetrics.routerCacheHits.Add(1)
}

// RecordRouterCacheMiss increments the router cache miss counter.
func RecordRouterCacheMiss() {
	globalMetrics.routerCacheMisses.Add(1)
}

// RecordExactCacheHit increments the exact cache hit counter.
func RecordExactCacheHit() {
	globalMetrics.exactCacheHits.Add(1)
}

// RecordExactCacheMiss increments the exact cache miss counter.
func RecordExactCacheMiss() {
	globalMetrics.exactCacheMisses.Add(1)
}

// RecordSemanticCacheHit increments the semantic cache hit counter.
func RecordSemanticCacheHit() {
	globalMetrics.semanticCacheHits.Add(1)
}

// RecordSemanticCacheMiss increments the semantic cache miss counter.
func RecordSemanticCacheMiss() {
	globalMetrics.semanticCacheMisses.Add(1)
}

// RecordFastPathRequest increments the fast-path request counter.
func RecordFastPathRequest() {
	globalMetrics.fastPathRequests.Add(1)
}

// RecordLegacyRequest increments the legacy request counter.
func RecordLegacyRequest() {
	globalMetrics.legacyRequests.Add(1)
}

// RecordEmbeddingsRequest increments the embeddings request counter.
func RecordEmbeddingsRequest(latencyUs int64) {
	globalMetrics.embeddingsRequests.Add(1)
	globalMetrics.embeddingsHist.observe(float64(latencyUs))
}

// RecordTokens records input and output token counts.
func RecordTokens(input, output int64) {
	if input > 0 {
		globalMetrics.inputTokens.Add(uint64(input))
	}
	if output > 0 {
		globalMetrics.outputTokens.Add(uint64(output))
	}
}

// RecordCost records cost in USD (will be converted to microdollars internally).
func RecordCost(inputCostUSD, outputCostUSD, savingsUSD float64) {
	if inputCostUSD > 0 {
		globalMetrics.inputCostMicros.Add(uint64(inputCostUSD * 1e6))
	}
	if outputCostUSD > 0 {
		globalMetrics.outputCostMicros.Add(uint64(outputCostUSD * 1e6))
	}
	if savingsUSD > 0 {
		globalMetrics.costSavingsMicros.Add(uint64(savingsUSD * 1e6))
	}
}

// MetricsSnapshot contains a point-in-time snapshot of all metrics.
type MetricsSnapshot struct {
	// Latency percentiles (microseconds)
	AuthP50, AuthP99, AuthP999    float64
	RoutingP50, RoutingP99        float64
	CacheP50, CacheP99            float64
	StreamingP50, StreamingP99    float64
	TotalP50, TotalP99, TotalP999 float64
	EmbeddingsP50, EmbeddingsP99  float64

	// Cache hit ratios
	AuthCacheHitRatio   float64
	RouterCacheHitRatio float64
	ExactCacheHitRatio  float64
	AgentCacheHitRatio  float64

	// Request counts
	TotalRequests      uint64
	FastPathRequests   uint64
	LegacyRequests     uint64
	EmbeddingsRequests uint64
	FastPathRatio      float64

	// Token counts
	InputTokens  uint64
	OutputTokens uint64

	// Cost (in USD)
	InputCostUSD   float64
	OutputCostUSD  float64
	CostSavingsUSD float64

	// Agent runtime cache counters
	AgentCacheHitsExact    uint64
	AgentCacheHitsSemantic uint64
	AgentCacheMisses       uint64
	AgentCacheStores       uint64
	AgentSemanticStores    uint64
}

// GetMetricsSnapshot returns a snapshot of all fast-path metrics.
func GetMetricsSnapshot() MetricsSnapshot {
	m := globalMetrics

	authHits := m.authCacheHits.Load()
	authMisses := m.authCacheMisses.Load()
	routerHits := m.routerCacheHits.Load()
	routerMisses := m.routerCacheMisses.Load()
	exactHits := m.exactCacheHits.Load()
	exactMisses := m.exactCacheMisses.Load()
	agentExactHits := m.agentCacheHitsExact.Load()
	agentSemanticHits := m.agentCacheHitsSemantic.Load()
	agentMisses := m.agentCacheMisses.Load()
	total := m.totalRequests.Load()
	fastPath := m.fastPathRequests.Load()
	legacy := m.legacyRequests.Load()

	snapshot := MetricsSnapshot{
		AuthP50:                m.authHist.percentile(0.50),
		AuthP99:                m.authHist.percentile(0.99),
		AuthP999:               m.authHist.percentile(0.999),
		RoutingP50:             m.routingHist.percentile(0.50),
		RoutingP99:             m.routingHist.percentile(0.99),
		CacheP50:               m.cacheHist.percentile(0.50),
		CacheP99:               m.cacheHist.percentile(0.99),
		StreamingP50:           m.streamingHist.percentile(0.50),
		StreamingP99:           m.streamingHist.percentile(0.99),
		TotalP50:               m.totalHist.percentile(0.50),
		TotalP99:               m.totalHist.percentile(0.99),
		TotalP999:              m.totalHist.percentile(0.999),
		EmbeddingsP50:          m.embeddingsHist.percentile(0.50),
		EmbeddingsP99:          m.embeddingsHist.percentile(0.99),
		TotalRequests:          total,
		FastPathRequests:       fastPath,
		LegacyRequests:         legacy,
		EmbeddingsRequests:     m.embeddingsRequests.Load(),
		InputTokens:            m.inputTokens.Load(),
		OutputTokens:           m.outputTokens.Load(),
		InputCostUSD:           float64(m.inputCostMicros.Load()) / 1e6,
		OutputCostUSD:          float64(m.outputCostMicros.Load()) / 1e6,
		CostSavingsUSD:         float64(m.costSavingsMicros.Load()) / 1e6,
		AgentCacheHitsExact:    agentExactHits,
		AgentCacheHitsSemantic: agentSemanticHits,
		AgentCacheMisses:       agentMisses,
		AgentCacheStores:       m.agentCacheStores.Load(),
		AgentSemanticStores:    m.agentSemanticStores.Load(),
	}

	// Calculate hit ratios
	if authTotal := authHits + authMisses; authTotal > 0 {
		snapshot.AuthCacheHitRatio = float64(authHits) / float64(authTotal)
	}
	if routerTotal := routerHits + routerMisses; routerTotal > 0 {
		snapshot.RouterCacheHitRatio = float64(routerHits) / float64(routerTotal)
	}
	if exactTotal := exactHits + exactMisses; exactTotal > 0 {
		snapshot.ExactCacheHitRatio = float64(exactHits) / float64(exactTotal)
	}
	if agentTotal := agentExactHits + agentSemanticHits + agentMisses; agentTotal > 0 {
		snapshot.AgentCacheHitRatio = float64(agentExactHits+agentSemanticHits) / float64(agentTotal)
	}
	if total > 0 {
		snapshot.FastPathRatio = float64(fastPath) / float64(total)
	}

	return snapshot
}

// RecordAgentCacheHit increments agent runtime cache hit counters.
func RecordAgentCacheHit(cacheType string) {
	switch cacheType {
	case "semantic":
		globalMetrics.agentCacheHitsSemantic.Add(1)
	default:
		globalMetrics.agentCacheHitsExact.Add(1)
	}
}

// RecordAgentCacheMiss increments the agent runtime cache miss counter.
func RecordAgentCacheMiss() {
	globalMetrics.agentCacheMisses.Add(1)
}

// RecordAgentCacheStore increments agent runtime cache store counters.
func RecordAgentCacheStore(semanticStored bool) {
	globalMetrics.agentCacheStores.Add(1)
	if semanticStored {
		globalMetrics.agentSemanticStores.Add(1)
	}
}

// ResetMetrics resets all metrics to zero.
func ResetMetrics() {
	globalMetrics = newFastPathMetrics()
}

// histogram is a simple histogram implementation for latency tracking.
// For production, consider using prometheus or HDR histogram.
type histogram struct {
	buckets    []float64
	counts     []atomic.Uint64
	totalCount atomic.Uint64
	sum        atomic.Uint64 // Sum of all values (for mean calculation)
	mu         sync.RWMutex
}

func newHistogram(buckets []float64) *histogram {
	return &histogram{
		buckets: buckets,
		counts:  make([]atomic.Uint64, len(buckets)+1), // +1 for overflow bucket
	}
}

func (h *histogram) observe(value float64) {
	h.totalCount.Add(1)
	h.sum.Add(uint64(value))

	// Find bucket
	for i, b := range h.buckets {
		if value <= b {
			h.counts[i].Add(1)
			return
		}
	}
	// Overflow bucket
	h.counts[len(h.buckets)].Add(1)
}

func (h *histogram) percentile(p float64) float64 {
	total := h.totalCount.Load()
	if total == 0 {
		return 0
	}

	target := uint64(float64(total) * p)
	var cumulative uint64

	for i, b := range h.buckets {
		cumulative += h.counts[i].Load()
		if cumulative >= target {
			return b
		}
	}

	// Above all buckets
	if len(h.buckets) > 0 {
		return h.buckets[len(h.buckets)-1] * 2
	}
	return 0
}

func (h *histogram) mean() float64 {
	total := h.totalCount.Load()
	if total == 0 {
		return 0
	}
	return float64(h.sum.Load()) / float64(total)
}

func (h *histogram) count() uint64 {
	return h.totalCount.Load()
}
