// Package metrics provides observability utilities for the fast-path gateway.
package metrics

import (
	"fmt"
	"sync"

	"github.com/everstacklabs/everstack/internal/providers/health"
)

// PrometheusExporter exports fast-path metrics in Prometheus format.
type PrometheusExporter struct {
	mu sync.RWMutex
}

// NewPrometheusExporter creates a new Prometheus metrics exporter.
func NewPrometheusExporter() *PrometheusExporter {
	return &PrometheusExporter{}
}

// Export returns all metrics in Prometheus text format.
func (e *PrometheusExporter) Export() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snapshot := GetMetricsSnapshot()
	m := globalMetrics

	var output string

	// HELP and TYPE declarations
	output += "# HELP fastpath_auth_latency_microseconds Authentication latency in microseconds\n"
	output += "# TYPE fastpath_auth_latency_microseconds summary\n"
	output += fmt.Sprintf("fastpath_auth_latency_microseconds{quantile=\"0.5\"} %.2f\n", snapshot.AuthP50)
	output += fmt.Sprintf("fastpath_auth_latency_microseconds{quantile=\"0.99\"} %.2f\n", snapshot.AuthP99)
	output += fmt.Sprintf("fastpath_auth_latency_microseconds{quantile=\"0.999\"} %.2f\n", snapshot.AuthP999)
	output += fmt.Sprintf("fastpath_auth_latency_microseconds_count %d\n", m.authHist.count())
	output += fmt.Sprintf("fastpath_auth_latency_microseconds_sum %d\n", m.authHist.sum.Load())
	output += "\n"

	output += "# HELP fastpath_routing_latency_microseconds Routing latency in microseconds\n"
	output += "# TYPE fastpath_routing_latency_microseconds summary\n"
	output += fmt.Sprintf("fastpath_routing_latency_microseconds{quantile=\"0.5\"} %.2f\n", snapshot.RoutingP50)
	output += fmt.Sprintf("fastpath_routing_latency_microseconds{quantile=\"0.99\"} %.2f\n", snapshot.RoutingP99)
	output += fmt.Sprintf("fastpath_routing_latency_microseconds_count %d\n", m.routingHist.count())
	output += fmt.Sprintf("fastpath_routing_latency_microseconds_sum %d\n", m.routingHist.sum.Load())
	output += "\n"

	output += "# HELP fastpath_cache_latency_microseconds Cache lookup latency in microseconds\n"
	output += "# TYPE fastpath_cache_latency_microseconds summary\n"
	output += fmt.Sprintf("fastpath_cache_latency_microseconds{quantile=\"0.5\"} %.2f\n", snapshot.CacheP50)
	output += fmt.Sprintf("fastpath_cache_latency_microseconds{quantile=\"0.99\"} %.2f\n", snapshot.CacheP99)
	output += fmt.Sprintf("fastpath_cache_latency_microseconds_count %d\n", m.cacheHist.count())
	output += fmt.Sprintf("fastpath_cache_latency_microseconds_sum %d\n", m.cacheHist.sum.Load())
	output += "\n"

	output += "# HELP fastpath_streaming_latency_microseconds Streaming latency in microseconds\n"
	output += "# TYPE fastpath_streaming_latency_microseconds summary\n"
	output += fmt.Sprintf("fastpath_streaming_latency_microseconds{quantile=\"0.5\"} %.2f\n", snapshot.StreamingP50)
	output += fmt.Sprintf("fastpath_streaming_latency_microseconds{quantile=\"0.99\"} %.2f\n", snapshot.StreamingP99)
	output += fmt.Sprintf("fastpath_streaming_latency_microseconds_count %d\n", m.streamingHist.count())
	output += fmt.Sprintf("fastpath_streaming_latency_microseconds_sum %d\n", m.streamingHist.sum.Load())
	output += "\n"

	output += "# HELP fastpath_total_latency_microseconds Total request latency in microseconds\n"
	output += "# TYPE fastpath_total_latency_microseconds summary\n"
	output += fmt.Sprintf("fastpath_total_latency_microseconds{quantile=\"0.5\"} %.2f\n", snapshot.TotalP50)
	output += fmt.Sprintf("fastpath_total_latency_microseconds{quantile=\"0.99\"} %.2f\n", snapshot.TotalP99)
	output += fmt.Sprintf("fastpath_total_latency_microseconds{quantile=\"0.999\"} %.2f\n", snapshot.TotalP999)
	output += fmt.Sprintf("fastpath_total_latency_microseconds_count %d\n", m.totalHist.count())
	output += fmt.Sprintf("fastpath_total_latency_microseconds_sum %d\n", m.totalHist.sum.Load())
	output += "\n"

	// Cache hit ratios
	output += "# HELP fastpath_auth_cache_hit_ratio Authentication cache hit ratio\n"
	output += "# TYPE fastpath_auth_cache_hit_ratio gauge\n"
	output += fmt.Sprintf("fastpath_auth_cache_hit_ratio %.4f\n", snapshot.AuthCacheHitRatio)
	output += "\n"

	output += "# HELP fastpath_router_cache_hit_ratio Router cache hit ratio\n"
	output += "# TYPE fastpath_router_cache_hit_ratio gauge\n"
	output += fmt.Sprintf("fastpath_router_cache_hit_ratio %.4f\n", snapshot.RouterCacheHitRatio)
	output += "\n"

	output += "# HELP fastpath_exact_cache_hit_ratio Exact cache hit ratio\n"
	output += "# TYPE fastpath_exact_cache_hit_ratio gauge\n"
	output += fmt.Sprintf("fastpath_exact_cache_hit_ratio %.4f\n", snapshot.ExactCacheHitRatio)
	output += "\n"

	// Cache counters
	output += "# HELP fastpath_auth_cache_hits_total Total authentication cache hits\n"
	output += "# TYPE fastpath_auth_cache_hits_total counter\n"
	output += fmt.Sprintf("fastpath_auth_cache_hits_total %d\n", m.authCacheHits.Load())
	output += "\n"

	output += "# HELP fastpath_auth_cache_misses_total Total authentication cache misses\n"
	output += "# TYPE fastpath_auth_cache_misses_total counter\n"
	output += fmt.Sprintf("fastpath_auth_cache_misses_total %d\n", m.authCacheMisses.Load())
	output += "\n"

	output += "# HELP fastpath_router_cache_hits_total Total router cache hits\n"
	output += "# TYPE fastpath_router_cache_hits_total counter\n"
	output += fmt.Sprintf("fastpath_router_cache_hits_total %d\n", m.routerCacheHits.Load())
	output += "\n"

	output += "# HELP fastpath_router_cache_misses_total Total router cache misses\n"
	output += "# TYPE fastpath_router_cache_misses_total counter\n"
	output += fmt.Sprintf("fastpath_router_cache_misses_total %d\n", m.routerCacheMisses.Load())
	output += "\n"

	output += "# HELP fastpath_exact_cache_hits_total Total exact cache hits\n"
	output += "# TYPE fastpath_exact_cache_hits_total counter\n"
	output += fmt.Sprintf("fastpath_exact_cache_hits_total %d\n", m.exactCacheHits.Load())
	output += "\n"

	output += "# HELP fastpath_exact_cache_misses_total Total exact cache misses\n"
	output += "# TYPE fastpath_exact_cache_misses_total counter\n"
	output += fmt.Sprintf("fastpath_exact_cache_misses_total %d\n", m.exactCacheMisses.Load())
	output += "\n"

	output += "# HELP fastpath_semantic_cache_hits_total Total semantic cache hits\n"
	output += "# TYPE fastpath_semantic_cache_hits_total counter\n"
	output += fmt.Sprintf("fastpath_semantic_cache_hits_total %d\n", m.semanticCacheHits.Load())
	output += "\n"

	output += "# HELP fastpath_semantic_cache_misses_total Total semantic cache misses\n"
	output += "# TYPE fastpath_semantic_cache_misses_total counter\n"
	output += fmt.Sprintf("fastpath_semantic_cache_misses_total %d\n", m.semanticCacheMisses.Load())
	output += "\n"

	// Request counters
	output += "# HELP fastpath_requests_total Total requests processed\n"
	output += "# TYPE fastpath_requests_total counter\n"
	output += fmt.Sprintf("fastpath_requests_total{path=\"fast\"} %d\n", snapshot.FastPathRequests)
	output += fmt.Sprintf("fastpath_requests_total{path=\"legacy\"} %d\n", snapshot.LegacyRequests)
	output += "\n"

	output += "# HELP fastpath_ratio Fast-path usage ratio\n"
	output += "# TYPE fastpath_ratio gauge\n"
	output += fmt.Sprintf("fastpath_ratio %.4f\n", snapshot.FastPathRatio)
	output += "\n"

	// === EMBEDDINGS METRICS ===
	output += "# HELP everstack_embeddings_requests_total Total embedding requests\n"
	output += "# TYPE everstack_embeddings_requests_total counter\n"
	output += fmt.Sprintf("everstack_embeddings_requests_total %d\n", m.embeddingsRequests.Load())
	output += "\n"

	output += "# HELP everstack_embeddings_latency_seconds Embedding request latency in seconds\n"
	output += "# TYPE everstack_embeddings_latency_seconds summary\n"
	output += fmt.Sprintf("everstack_embeddings_latency_seconds{quantile=\"0.5\"} %.6f\n", snapshot.EmbeddingsP50/1e6)
	output += fmt.Sprintf("everstack_embeddings_latency_seconds{quantile=\"0.99\"} %.6f\n", snapshot.EmbeddingsP99/1e6)
	output += fmt.Sprintf("everstack_embeddings_latency_seconds_count %d\n", m.embeddingsHist.count())
	output += "\n"

	// === TOKEN METRICS ===
	output += "# HELP everstack_tokens_total Total tokens processed\n"
	output += "# TYPE everstack_tokens_total counter\n"
	output += fmt.Sprintf("everstack_tokens_total{type=\"input\"} %d\n", m.inputTokens.Load())
	output += fmt.Sprintf("everstack_tokens_total{type=\"output\"} %d\n", m.outputTokens.Load())
	output += "\n"

	// === COST METRICS ===
	output += "# HELP everstack_cost_dollars_total Total cost in USD\n"
	output += "# TYPE everstack_cost_dollars_total counter\n"
	output += fmt.Sprintf("everstack_cost_dollars_total{type=\"input\"} %.6f\n", float64(m.inputCostMicros.Load())/1e6)
	output += fmt.Sprintf("everstack_cost_dollars_total{type=\"output\"} %.6f\n", float64(m.outputCostMicros.Load())/1e6)
	output += "\n"

	output += "# HELP everstack_cost_savings_dollars_total Total cost savings from caching in USD\n"
	output += "# TYPE everstack_cost_savings_dollars_total counter\n"
	output += fmt.Sprintf("everstack_cost_savings_dollars_total %.6f\n", float64(m.costSavingsMicros.Load())/1e6)
	output += "\n"

	// === AGENT CACHE METRICS ===
	output += "# HELP everstack_agent_cache_events_total Agent runtime cache events\n"
	output += "# TYPE everstack_agent_cache_events_total counter\n"
	output += fmt.Sprintf("everstack_agent_cache_events_total{event=\"hit\",cache_type=\"exact\"} %d\n", snapshot.AgentCacheHitsExact)
	output += fmt.Sprintf("everstack_agent_cache_events_total{event=\"hit\",cache_type=\"semantic\"} %d\n", snapshot.AgentCacheHitsSemantic)
	output += fmt.Sprintf("everstack_agent_cache_events_total{event=\"miss\",cache_type=\"none\"} %d\n", snapshot.AgentCacheMisses)
	output += fmt.Sprintf("everstack_agent_cache_events_total{event=\"store\",cache_type=\"exact\"} %d\n", snapshot.AgentCacheStores)
	output += fmt.Sprintf("everstack_agent_cache_events_total{event=\"store\",cache_type=\"semantic\"} %d\n", snapshot.AgentSemanticStores)
	output += "\n"

	output += "# HELP everstack_agent_cache_hit_ratio Agent runtime cache hit ratio\n"
	output += "# TYPE everstack_agent_cache_hit_ratio gauge\n"
	output += fmt.Sprintf("everstack_agent_cache_hit_ratio %.4f\n", snapshot.AgentCacheHitRatio)
	output += "\n"

	// === PROVIDER HEALTH METRICS ===
	healthResp := health.GetAllProviderHealth()
	for _, ph := range healthResp.Providers {
		statusValue := 0
		switch ph.Status {
		case health.StatusHealthy:
			statusValue = 2
		case health.StatusDegraded:
			statusValue = 1
		case health.StatusUnhealthy:
			statusValue = 0
		}

		output += fmt.Sprintf("# HELP everstack_provider_health_status Provider health status (0=unhealthy, 1=degraded, 2=healthy)\n")
		output += fmt.Sprintf("# TYPE everstack_provider_health_status gauge\n")
		output += fmt.Sprintf("everstack_provider_health_status{provider=\"%s\"} %d\n", ph.Name, statusValue)

		output += fmt.Sprintf("everstack_provider_error_rate{provider=\"%s\"} %.4f\n", ph.Name, ph.ErrorRate)
		output += fmt.Sprintf("everstack_provider_latency_avg_ms{provider=\"%s\"} %d\n", ph.Name, ph.AvgLatencyMs)
		output += fmt.Sprintf("everstack_provider_latency_p99_ms{provider=\"%s\"} %d\n", ph.Name, ph.P99LatencyMs)
		output += fmt.Sprintf("everstack_provider_requests_total{provider=\"%s\"} %d\n", ph.Name, ph.RequestCount)
		output += fmt.Sprintf("everstack_provider_errors_total{provider=\"%s\"} %d\n", ph.Name, ph.FailureCount)
	}
	output += "\n"

	return output
}

// ExportJSON returns metrics in JSON format for easier consumption.
func (e *PrometheusExporter) ExportJSON() map[string]interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snapshot := GetMetricsSnapshot()
	m := globalMetrics

	return map[string]interface{}{
		"latency": map[string]interface{}{
			"auth": map[string]interface{}{
				"p50":   snapshot.AuthP50,
				"p99":   snapshot.AuthP99,
				"p999":  snapshot.AuthP999,
				"count": m.authHist.count(),
				"mean":  m.authHist.mean(),
			},
			"routing": map[string]interface{}{
				"p50":   snapshot.RoutingP50,
				"p99":   snapshot.RoutingP99,
				"count": m.routingHist.count(),
				"mean":  m.routingHist.mean(),
			},
			"cache": map[string]interface{}{
				"p50":   snapshot.CacheP50,
				"p99":   snapshot.CacheP99,
				"count": m.cacheHist.count(),
				"mean":  m.cacheHist.mean(),
			},
			"streaming": map[string]interface{}{
				"p50":   snapshot.StreamingP50,
				"p99":   snapshot.StreamingP99,
				"count": m.streamingHist.count(),
				"mean":  m.streamingHist.mean(),
			},
			"total": map[string]interface{}{
				"p50":   snapshot.TotalP50,
				"p99":   snapshot.TotalP99,
				"p999":  snapshot.TotalP999,
				"count": m.totalHist.count(),
				"mean":  m.totalHist.mean(),
			},
		},
		"cache_hit_ratios": map[string]interface{}{
			"auth":   snapshot.AuthCacheHitRatio,
			"router": snapshot.RouterCacheHitRatio,
			"exact":  snapshot.ExactCacheHitRatio,
			"agent":  snapshot.AgentCacheHitRatio,
		},
		"cache_counters": map[string]interface{}{
			"auth": map[string]interface{}{
				"hits":   m.authCacheHits.Load(),
				"misses": m.authCacheMisses.Load(),
			},
			"router": map[string]interface{}{
				"hits":   m.routerCacheHits.Load(),
				"misses": m.routerCacheMisses.Load(),
			},
			"exact": map[string]interface{}{
				"hits":   m.exactCacheHits.Load(),
				"misses": m.exactCacheMisses.Load(),
			},
			"semantic": map[string]interface{}{
				"hits":   m.semanticCacheHits.Load(),
				"misses": m.semanticCacheMisses.Load(),
			},
			"agent": map[string]interface{}{
				"hits_exact":      snapshot.AgentCacheHitsExact,
				"hits_semantic":   snapshot.AgentCacheHitsSemantic,
				"misses":          snapshot.AgentCacheMisses,
				"stores_exact":    snapshot.AgentCacheStores,
				"stores_semantic": snapshot.AgentSemanticStores,
			},
		},
		"requests": map[string]interface{}{
			"total":           snapshot.TotalRequests,
			"fast_path":       snapshot.FastPathRequests,
			"legacy":          snapshot.LegacyRequests,
			"fast_path_ratio": snapshot.FastPathRatio,
		},
	}
}
