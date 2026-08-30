package real_time

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// Import types directly for easier access
type (
	ModelUsageSnapshot   = query.ModelUsageSnapshot
	LoadBalancerSnapshot = query.LoadBalancerSnapshot
	ProviderHealthStatus = query.ProviderHealthStatus
)

// RealTimeMetricsQueryHandler handles real-time system metrics queries.
type RealTimeMetricsQueryHandler struct {
	db *sqlx.DB
}

// NewRealTimeMetricsQueryHandler creates a new real-time metrics query handler.
func NewRealTimeMetricsQueryHandler(db *sqlx.DB) *RealTimeMetricsQueryHandler {
	return &RealTimeMetricsQueryHandler{db: db}
}

// QueryType returns the query type this handler processes.
func (h *RealTimeMetricsQueryHandler) QueryType() string {
	return "GetRealTimeMetrics"
}

// Handle processes a GetRealTimeMetricsQuery and returns current system state.
func (h *RealTimeMetricsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	correlationID := correlation.GetCorrelationID(ctx)

	logger.WithFields(
		"query_type", "GetRealTimeMetrics",
		"correlation_id", correlationID,
	).Debug("executing real-time metrics query")

	now := time.Now()
	metrics := &query.RealTimeMetricsReadModel{
		Timestamp: now,
	}

	// Get active connections (simulated - in real system would come from connection pool)
	metrics.ActiveConnections = h.getActiveConnections(ctx)

	// Get requests per second (last minute)
	rps, err := h.getRequestsPerSecond(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to get requests per second")
	} else {
		metrics.RequestsPerSecond = rps
	}

	// Get average latency (last 5 minutes)
	avgLatency, err := h.getAverageLatency(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to get average latency")
	} else {
		metrics.AvgLatencyMs = avgLatency
	}

	// Get error rate (last hour)
	errorRate, err := h.getErrorRate(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to get error rate")
	} else {
		metrics.ErrorRate = errorRate
	}

	// Get top models by usage
	topModels, err := h.getTopModels(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to get top models")
	} else {
		metrics.TopModels = topModels
	}

	// Get load balancer status
	lbStatus, err := h.getLoadBalancerStatus(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to get load balancer status")
	} else {
		metrics.LoadBalancerStatus = lbStatus
	}

	// Get provider health
	providerHealth, err := h.getProviderHealth(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to get provider health")
	} else {
		metrics.ProviderHealthCheck = providerHealth
	}

	logger.WithFields(
		"correlation_id", correlationID,
		"active_connections", metrics.ActiveConnections,
		"requests_per_second", metrics.RequestsPerSecond,
		"avg_latency_ms", metrics.AvgLatencyMs,
		"error_rate", metrics.ErrorRate,
	).Info("real-time metrics query executed successfully")

	return metrics, nil
}

// getActiveConnections returns the current number of active connections.
func (h *RealTimeMetricsQueryHandler) getActiveConnections(ctx context.Context) int {
	// In a real system, this would query connection pool stats
	// For now, return a simulated value
	return 42
}

// getRequestsPerSecond calculates requests per second over the last minute.
func (h *RealTimeMetricsQueryHandler) getRequestsPerSecond(ctx context.Context) (float64, error) {
	query := `
	SELECT COUNT(*)::FLOAT / 60.0 as rps
	FROM events 
	WHERE type IN ('chat.session.started', 'embedding.request.started')
	AND created_at >= EXTRACT(EPOCH FROM NOW() - INTERVAL '1 minute')`

	var rps float64
	err := h.db.GetContext(ctx, &rps, query)
	return rps, err
}

// getAverageLatency calculates average latency over the last 5 minutes.
func (h *RealTimeMetricsQueryHandler) getAverageLatency(ctx context.Context) (float64, error) {
	query := `
	SELECT AVG(CAST(payload->>'latency_ms' AS NUMERIC)) as avg_latency
	FROM events 
	WHERE type IN ('chat.session.completed', 'embedding.request.completed')
	AND created_at >= EXTRACT(EPOCH FROM NOW() - INTERVAL '5 minutes')
	AND payload->>'latency_ms' IS NOT NULL`

	var avgLatency sql.NullFloat64
	err := h.db.GetContext(ctx, &avgLatency, query)
	if err != nil {
		return 0, err
	}
	if !avgLatency.Valid {
		return 0, nil
	}
	return avgLatency.Float64, nil
}

// getErrorRate calculates error rate over the last hour.
func (h *RealTimeMetricsQueryHandler) getErrorRate(ctx context.Context) (float64, error) {
	query := `
	SELECT 
		CASE 
			WHEN total_count > 0 
			THEN (error_count::FLOAT / total_count::FLOAT) * 100
			ELSE 0 
		END as error_rate
	FROM (
		SELECT 
			COUNT(CASE WHEN type LIKE '%.error' THEN 1 END) as error_count,
			COUNT(*) as total_count
		FROM events 
		WHERE created_at >= EXTRACT(EPOCH FROM NOW() - INTERVAL '1 hour')
		AND type IN ('chat.session.started', 'chat.session.error', 'embedding.request.started', 'embedding.request.error')
	) stats`

	var errorRate float64
	err := h.db.GetContext(ctx, &errorRate, query)
	return errorRate, err
}

// getTopModels returns the most used models in the last hour.
func (h *RealTimeMetricsQueryHandler) getTopModels(ctx context.Context) ([]ModelUsageSnapshot, error) {
	query := `
	SELECT 
		CAST(payload->>'provider' AS TEXT) as provider,
		CAST(payload->>'model' AS TEXT) as model,
		COUNT(*) as request_count,
		AVG(CAST(payload->>'latency_ms' AS NUMERIC)) as avg_latency_ms,
		COUNT(CASE WHEN payload->>'success' = 'true' THEN 1 END)::FLOAT / COUNT(*)::FLOAT * 100 as success_rate
	FROM events 
	WHERE type IN ('chat.session.started', 'embedding.request.started')
	AND created_at >= EXTRACT(EPOCH FROM NOW() - INTERVAL '1 hour')
	AND payload->>'provider' IS NOT NULL
	AND payload->>'model' IS NOT NULL
	GROUP BY 
		CAST(payload->>'provider' AS TEXT),
		CAST(payload->>'model' AS TEXT)
	ORDER BY request_count DESC
	LIMIT 5`

	rows, err := h.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []ModelUsageSnapshot
	for rows.Next() {
		var model ModelUsageSnapshot
		var requestCount int
		var avgLatency sql.NullFloat64

		err := rows.Scan(&model.Provider, &model.Model, &requestCount, &avgLatency, &model.SuccessRate)
		if err != nil {
			continue
		}

		model.ActiveRequests = requestCount // Simplified for demo
		if avgLatency.Valid {
			model.AvgLatencyMs = avgLatency.Float64
		}

		models = append(models, model)
	}

	return models, nil
}

// getLoadBalancerStatus returns current load balancer configuration.
func (h *RealTimeMetricsQueryHandler) getLoadBalancerStatus(ctx context.Context) (LoadBalancerSnapshot, error) {
	// In a real system, this would query the current LB configuration
	// For now, return simulated data
	return LoadBalancerSnapshot{
		Strategy:       "round_robin",
		KeySource:      "user_id",
		ActiveTargets:  4,
		FallbackActive: false,
		Weights: map[string]float64{
			"openai":    0.6,
			"anthropic": 0.3,
			"google":    0.1,
		},
	}, nil
}

// getProviderHealth returns health status for each provider.
func (h *RealTimeMetricsQueryHandler) getProviderHealth(ctx context.Context) ([]ProviderHealthStatus, error) {
	query := `
	SELECT 
		CAST(payload->>'provider' AS TEXT) as provider,
		AVG(CAST(payload->>'latency_ms' AS NUMERIC)) as avg_response_time,
		COUNT(CASE WHEN payload->>'success' = 'false' THEN 1 END)::FLOAT / COUNT(*)::FLOAT * 100 as error_rate
	FROM events 
	WHERE type IN ('chat.session.completed', 'embedding.request.completed')
	AND created_at >= EXTRACT(EPOCH FROM NOW() - INTERVAL '10 minutes')
	AND payload->>'provider' IS NOT NULL
	GROUP BY CAST(payload->>'provider' AS TEXT)`

	rows, err := h.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []ProviderHealthStatus
	for rows.Next() {
		var provider ProviderHealthStatus
		var responseTime sql.NullFloat64

		err := rows.Scan(&provider.Provider, &responseTime, &provider.ErrorRate)
		if err != nil {
			continue
		}

		if responseTime.Valid {
			provider.ResponseTime = responseTime.Float64
		}

		provider.LastChecked = time.Now()

		// Determine health status based on error rate and response time
		if provider.ErrorRate > 10 || provider.ResponseTime > 5000 {
			provider.Status = "unhealthy"
		} else if provider.ErrorRate > 5 || provider.ResponseTime > 2000 {
			provider.Status = "degraded"
		} else {
			provider.Status = "healthy"
		}

		providers = append(providers, provider)
	}

	return providers, nil
}
