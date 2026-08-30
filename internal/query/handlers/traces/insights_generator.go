package traces

import (
	"fmt"
)

// Insight represents an auto-generated insight from trace data
type Insight struct {
	Type     string `json:"type"`     // "performance", "cost", "optimization", "error"
	Severity string `json:"severity"` // "info", "success", "warning", "error"
	Message  string `json:"message"`
}

// InsightsGenerator generates insights from trace analytics
type InsightsGenerator struct{}

// NewInsightsGenerator creates a new insights generator
func NewInsightsGenerator() *InsightsGenerator {
	return &InsightsGenerator{}
}

// GenerateInsights analyzes trace data and generates actionable insights
func (g *InsightsGenerator) GenerateInsights(summary TraceSummaryData) []Insight {
	insights := []Insight{}

	// Cache performance insights
	if summary.CacheHitRate > 0 {
		insights = append(insights, g.generateCacheInsights(summary)...)
	}

	// Cost insights
	if summary.TotalCostUSD > 0 || summary.TotalSavingsUSD > 0 {
		insights = append(insights, g.generateCostInsights(summary)...)
	}

	// Performance insights
	insights = append(insights, g.generatePerformanceInsights(summary)...)

	// Error insights
	if summary.ErrorRate > 0 {
		insights = append(insights, g.generateErrorInsights(summary)...)
	}

	// Model usage insights
	if len(summary.ModelUsage) > 0 {
		insights = append(insights, g.generateModelInsights(summary)...)
	}

	return insights
}

// generateCacheInsights generates insights about cache performance
func (g *InsightsGenerator) generateCacheInsights(summary TraceSummaryData) []Insight {
	insights := []Insight{}

	hitRate := summary.CacheHitRate * 100

	switch {
	case hitRate == 100:
		insights = append(insights, Insight{
			Type:     "performance",
			Severity: "success",
			Message:  fmt.Sprintf("Perfect cache performance: 100%% hit rate saving an average of %.0fms per request", summary.AvgCacheSavedTimeMs),
		})
	case hitRate >= 80:
		insights = append(insights, Insight{
			Type:     "performance",
			Severity: "success",
			Message:  fmt.Sprintf("Excellent cache performance: %.1f%% hit rate saving an average of %.0fms per request", hitRate, summary.AvgCacheSavedTimeMs),
		})
	case hitRate >= 50:
		insights = append(insights, Insight{
			Type:     "performance",
			Severity: "info",
			Message:  fmt.Sprintf("Good cache performance: %.1f%% hit rate. Consider tuning cache TTL for better results", hitRate),
		})
	case hitRate >= 20:
		insights = append(insights, Insight{
			Type:     "optimization",
			Severity: "warning",
			Message:  fmt.Sprintf("Moderate cache performance: %.1f%% hit rate. Review cache strategy and query patterns", hitRate),
		})
	default:
		insights = append(insights, Insight{
			Type:     "optimization",
			Severity: "warning",
			Message:  fmt.Sprintf("Low cache performance: %.1f%% hit rate. Consider enabling semantic caching or reviewing cache configuration", hitRate),
		})
	}

	return insights
}

// generateCostInsights generates insights about costs and savings
func (g *InsightsGenerator) generateCostInsights(summary TraceSummaryData) []Insight {
	insights := []Insight{}

	if summary.TotalSavingsUSD > 0 {
		savingsPercent := 0.0
		if summary.TotalCostUSD+summary.TotalSavingsUSD > 0 {
			savingsPercent = (summary.TotalSavingsUSD / (summary.TotalCostUSD + summary.TotalSavingsUSD)) * 100
		}

		insights = append(insights, Insight{
			Type:     "cost",
			Severity: "success",
			Message:  fmt.Sprintf("Cache savings: $%.6f (%.1f%% cost reduction)", summary.TotalSavingsUSD, savingsPercent),
		})

		if summary.TotalCarbonSavedGrams > 0 {
			insights = append(insights, Insight{
				Type:     "cost",
				Severity: "info",
				Message:  fmt.Sprintf("Environmental impact: %.2fg CO2 saved through caching", summary.TotalCarbonSavedGrams),
			})
		}
	}

	if summary.TotalCostUSD > 0.01 {
		insights = append(insights, Insight{
			Type:     "cost",
			Severity: "info",
			Message:  fmt.Sprintf("Total API costs: $%.6f across %d requests", summary.TotalCostUSD, summary.TotalTraces),
		})
	}

	return insights
}

// generatePerformanceInsights generates insights about performance
func (g *InsightsGenerator) generatePerformanceInsights(summary TraceSummaryData) []Insight {
	insights := []Insight{}

	avgDuration := summary.AvgDurationMs

	switch {
	case avgDuration < 100:
		insights = append(insights, Insight{
			Type:     "performance",
			Severity: "success",
			Message:  fmt.Sprintf("Excellent performance: All requests completed in under 100ms (avg: %.1fms)", avgDuration),
		})
	case avgDuration < 500:
		insights = append(insights, Insight{
			Type:     "performance",
			Severity: "info",
			Message:  fmt.Sprintf("Good performance: Average response time %.1fms", avgDuration),
		})
	case avgDuration < 2000:
		insights = append(insights, Insight{
			Type:     "performance",
			Severity: "warning",
			Message:  fmt.Sprintf("Acceptable performance: Average response time %.1fms. Consider optimization", avgDuration),
		})
	default:
		insights = append(insights, Insight{
			Type:     "performance",
			Severity: "warning",
			Message:  fmt.Sprintf("Slow performance: Average response time %.1fms. Review provider selection and caching strategy", avgDuration),
		})
	}

	// P95 insights
	if summary.P95DurationMs > 0 {
		if summary.P95DurationMs > 5000 {
			insights = append(insights, Insight{
				Type:     "performance",
				Severity: "warning",
				Message:  fmt.Sprintf("High P95 latency: %.1fms. Some requests are experiencing significant delays", summary.P95DurationMs),
			})
		}
	}

	// Throughput insights
	if summary.AvgTokensPerSecond > 0 {
		if summary.AvgTokensPerSecond > 1000 {
			insights = append(insights, Insight{
				Type:     "performance",
				Severity: "success",
				Message:  fmt.Sprintf("High throughput: %.0f tokens/second average", summary.AvgTokensPerSecond),
			})
		}
	}

	return insights
}

// generateErrorInsights generates insights about errors
func (g *InsightsGenerator) generateErrorInsights(summary TraceSummaryData) []Insight {
	insights := []Insight{}

	errorPercent := summary.ErrorRate * 100

	switch {
	case errorPercent > 10:
		insights = append(insights, Insight{
			Type:     "error",
			Severity: "error",
			Message:  fmt.Sprintf("High error rate: %.1f%% of requests failed. Immediate attention required", errorPercent),
		})
	case errorPercent > 5:
		insights = append(insights, Insight{
			Type:     "error",
			Severity: "warning",
			Message:  fmt.Sprintf("Elevated error rate: %.1f%% of requests failed. Review provider health and retry policies", errorPercent),
		})
	case errorPercent > 1:
		insights = append(insights, Insight{
			Type:     "error",
			Severity: "warning",
			Message:  fmt.Sprintf("Some errors detected: %.1f%% of requests failed", errorPercent),
		})
	}

	return insights
}

// generateModelInsights generates insights about model usage
func (g *InsightsGenerator) generateModelInsights(summary TraceSummaryData) []Insight {
	insights := []Insight{}

	if len(summary.ModelUsage) > 3 {
		insights = append(insights, Insight{
			Type:     "optimization",
			Severity: "info",
			Message:  fmt.Sprintf("Using %d different models. Consider consolidating for better cache efficiency", len(summary.ModelUsage)),
		})
	}

	// Find most used model
	var mostUsedModel string
	var maxRequests int
	for model, usage := range summary.ModelUsage {
		if usage.Requests > maxRequests {
			maxRequests = usage.Requests
			mostUsedModel = model
		}
	}

	if mostUsedModel != "" && maxRequests > 0 {
		insights = append(insights, Insight{
			Type:     "optimization",
			Severity: "info",
			Message:  fmt.Sprintf("Most used model: %s (%d requests, avg %.1fms)", mostUsedModel, maxRequests, summary.ModelUsage[mostUsedModel].AvgDurationMs),
		})
	}

	return insights
}

// TraceSummaryData contains aggregated trace data for insight generation
type TraceSummaryData struct {
	TotalTraces          int
	TotalCostUSD         float64
	TotalSavingsUSD      float64
	TotalCarbonSavedGrams float64
	CacheHitRate         float64
	AvgCacheSavedTimeMs  float64
	AvgDurationMs        float64
	P50DurationMs        float64
	P95DurationMs        float64
	P99DurationMs        float64
	AvgTokensPerSecond   float64
	ErrorRate            float64
	ModelUsage           map[string]ModelUsageStats
}

// ModelUsageStats contains usage statistics for a model
type ModelUsageStats struct {
	Requests      int
	CacheHits     int
	AvgDurationMs float64
}

