package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/metrics"
)

// FastPathMetricsHandler returns fast-path metrics in Prometheus format.
func FastPathMetricsHandler(w http.ResponseWriter, r *http.Request) {
	exporter := metrics.NewPrometheusExporter()

	// Support both Prometheus text format and JSON
	acceptHeader := r.Header.Get("Accept")
	if acceptHeader == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(exporter.ExportJSON())
		return
	}

	// Default to Prometheus text format
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Write([]byte(exporter.Export()))
}

// FastPathStatsHandler returns detailed fast-path engine statistics.
func FastPathStatsHandler(w http.ResponseWriter, r *http.Request) {
	engine := fastpath.GetGlobalEngine()
	if engine == nil {
		http.Error(w, "Fast-path engine not initialized", http.StatusServiceUnavailable)
		return
	}

	stats := engine.Stats()
	snapshot := metrics.GetMetricsSnapshot()

	response := map[string]interface{}{
		"enabled": stats.Enabled,
		"requests": map[string]interface{}{
			"total":           stats.RequestsProcessed,
			"fast_path":       stats.RequestsFastPath,
			"fallback":        stats.RequestsFallback,
			"fast_path_ratio": float64(stats.RequestsFastPath) / float64(stats.RequestsProcessed),
		},
		"auth_cache": map[string]interface{}{
			"hits":      stats.AuthCacheHits,
			"misses":    stats.AuthCacheMisses,
			"hit_ratio": stats.AuthCacheHitRatio,
		},
		"router_cache": map[string]interface{}{
			"hits":      stats.RouterCacheHits,
			"misses":    stats.RouterCacheMisses,
			"hit_ratio": stats.RouterCacheHitRatio,
		},
		"exact_cache": map[string]interface{}{
			"hits":      stats.ExactCacheHits,
			"misses":    stats.ExactCacheMisses,
			"evictions": stats.ExactCacheEvicts,
			"hit_ratio": stats.ExactCacheHitRatio,
			"size":      stats.ExactCacheSize,
		},
		"agent_cache": map[string]interface{}{
			"hits_exact":      snapshot.AgentCacheHitsExact,
			"hits_semantic":   snapshot.AgentCacheHitsSemantic,
			"misses":          snapshot.AgentCacheMisses,
			"stores_exact":    snapshot.AgentCacheStores,
			"stores_semantic": snapshot.AgentSemanticStores,
			"hit_ratio":       snapshot.AgentCacheHitRatio,
		},
		"latency_percentiles_us": map[string]interface{}{
			"auth": map[string]interface{}{
				"p50":  snapshot.AuthP50,
				"p99":  snapshot.AuthP99,
				"p999": snapshot.AuthP999,
			},
			"routing": map[string]interface{}{
				"p50": snapshot.RoutingP50,
				"p99": snapshot.RoutingP99,
			},
			"cache": map[string]interface{}{
				"p50": snapshot.CacheP50,
				"p99": snapshot.CacheP99,
			},
			"streaming": map[string]interface{}{
				"p50": snapshot.StreamingP50,
				"p99": snapshot.StreamingP99,
			},
			"total": map[string]interface{}{
				"p50":  snapshot.TotalP50,
				"p99":  snapshot.TotalP99,
				"p999": snapshot.TotalP999,
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// FastPathHealthHandler returns health status of fast-path components.
func FastPathHealthHandler(w http.ResponseWriter, r *http.Request) {
	engine := fastpath.GetGlobalEngine()

	health := map[string]interface{}{
		"status": "healthy",
		"components": map[string]interface{}{
			"engine": map[string]interface{}{
				"initialized": engine != nil,
				"enabled":     engine != nil && engine.IsEnabled(),
			},
		},
	}

	if engine != nil {
		stats := engine.Stats()

		// Check if caches are performing well
		authHealth := "healthy"
		if stats.AuthCacheHitRatio < 0.7 && stats.AuthCacheHits+stats.AuthCacheMisses > 100 {
			authHealth = "degraded"
		}

		routerHealth := "healthy"
		if stats.RouterCacheHitRatio < 0.8 && stats.RouterCacheHits+stats.RouterCacheMisses > 100 {
			routerHealth = "degraded"
		}

		cacheHealth := "healthy"
		if stats.ExactCacheHitRatio < 0.5 && stats.ExactCacheHits+stats.ExactCacheMisses > 100 {
			cacheHealth = "degraded"
		}

		health["components"].(map[string]interface{})["auth_cache"] = map[string]interface{}{
			"status":    authHealth,
			"hit_ratio": stats.AuthCacheHitRatio,
		}
		health["components"].(map[string]interface{})["router_cache"] = map[string]interface{}{
			"status":    routerHealth,
			"hit_ratio": stats.RouterCacheHitRatio,
		}
		health["components"].(map[string]interface{})["exact_cache"] = map[string]interface{}{
			"status":    cacheHealth,
			"hit_ratio": stats.ExactCacheHitRatio,
			"size":      stats.ExactCacheSize,
		}

		// Overall health based on component health
		if authHealth == "degraded" || routerHealth == "degraded" || cacheHealth == "degraded" {
			health["status"] = "degraded"
		}
	} else {
		health["status"] = "unavailable"
	}

	statusCode := http.StatusOK
	if health["status"] == "degraded" {
		statusCode = http.StatusOK // Still return 200 but indicate degraded
	} else if health["status"] == "unavailable" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(health)
}
