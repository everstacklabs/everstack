package traces

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// DetailedTraceHandler provides detailed trace export functionality
type DetailedTraceHandler struct {
	conn clickhouse.Conn
}

// NewDetailedTraceHandler creates a new detailed trace handler
func NewDetailedTraceHandler(conn clickhouse.Conn) *DetailedTraceHandler {
	return &DetailedTraceHandler{
		conn: conn,
	}
}

func (h *DetailedTraceHandler) QueryType() string {
	return "GetDetailedTrace"
}

// DetailedTraceQuery represents a query for detailed trace data
type DetailedTraceQuery struct {
	TraceID string
}

func (q *DetailedTraceQuery) QueryType() string {
	return "GetDetailedTrace"
}

func (q *DetailedTraceQuery) Validate() error {
	if q.TraceID == "" {
		return fmt.Errorf("trace_id is required")
	}
	return nil
}

// DetailedTrace represents a complete trace with all spans and events
type DetailedTrace struct {
	TraceID string               `json:"trace_id"`
	Spans   []DetailedSpan       `json:"spans"`
	Summary DetailedTraceSummary `json:"summary,omitempty"`
}

// DetailedSpan represents a single span with full details
type DetailedSpan struct {
	Timestamp          time.Time           `json:"timestamp"`
	TraceID            string              `json:"trace_id"`
	SpanID             string              `json:"span_id"`
	ParentSpanID       string              `json:"parent_span_id,omitempty"`
	TraceState         string              `json:"trace_state,omitempty"`
	SpanName           string              `json:"span_name"`
	SpanKind           string              `json:"span_kind"`
	ServiceName        string              `json:"service_name"`
	ResourceAttributes map[string]string   `json:"resource_attributes"`
	ScopeName          string              `json:"scope_name"`
	ScopeVersion       string              `json:"scope_version"`
	SpanAttributes     map[string]string   `json:"span_attributes"`
	Duration           int64               `json:"duration"` // int64 to match ClickHouse Int64
	DurationMs         float64             `json:"duration_ms"`
	StatusCode         string              `json:"status_code"`
	StatusMessage      string              `json:"status_message,omitempty"`
	Events             []SpanEvent         `json:"events,omitempty"`
	Links              []SpanLink          `json:"links,omitempty"`
	PerformanceMetrics *PerformanceMetrics `json:"performance_metrics,omitempty"`
	BusinessMetrics    *BusinessMetrics    `json:"business_metrics,omitempty"`
	StepNumber         *int                `json:"step_number,omitempty"`
	NodeName           *string             `json:"node_name,omitempty"`
	ObservationType    string              `json:"observation_type,omitempty"`
}

// SpanEvent represents an event within a span
type SpanEvent struct {
	Timestamp  time.Time         `json:"timestamp"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// SpanLink represents a link to another span
type SpanLink struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	TraceState string            `json:"trace_state,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// PerformanceMetrics contains calculated performance metrics
type PerformanceMetrics struct {
	IsCached                  bool    `json:"is_cached"`
	CacheEfficiencyPercentage float64 `json:"cache_efficiency_percentage,omitempty"`
	EstimatedSavedTimeMs      float64 `json:"estimated_saved_time_ms,omitempty"`
	TokensPerSecond           float64 `json:"tokens_per_second,omitempty"`
	LatencyCategory           string  `json:"latency_category,omitempty"`
	ThroughputCategory        string  `json:"throughput_category,omitempty"`
	CacheSpeedupFactor        int     `json:"cache_speedup_factor,omitempty"`
}

// BusinessMetrics contains business intelligence metrics
type BusinessMetrics struct {
	CostSavingsUSD        float64 `json:"cost_savings_usd,omitempty"`
	CarbonSavedGrams      float64 `json:"carbon_saved_grams,omitempty"`
	UserSatisfactionScore float64 `json:"user_satisfaction_score,omitempty"`
	QueryComplexity       string  `json:"query_complexity,omitempty"`
	DomainConfidence      string  `json:"domain_confidence,omitempty"`
}

// DetailedTraceSummary contains summary information for the trace
type DetailedTraceSummary struct {
	TotalSpans      int     `json:"total_spans"`
	TotalDurationMs float64 `json:"total_duration_ms"`
	TotalCostUSD    float64 `json:"total_cost_usd,omitempty"`
	CacheHitRate    float64 `json:"cache_hit_rate,omitempty"`
}

// Handle executes the detailed trace query
func (h *DetailedTraceHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	detailedQuery, ok := q.(*DetailedTraceQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for DetailedTraceHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — single-trace lookup would otherwise match a
		// foreign tenant's spans by TraceId.
		return nil, fmt.Errorf("trace not found")
	}
	tenantClause, tenantArgs := tenantBridgeFilter(tenantID)
	tenantFilter := "AND " + tenantClause

	// Query all spans for the trace
	sqlQuery := fmt.Sprintf(`
		SELECT
			Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind,
			ServiceName, ResourceAttributes, ScopeName, ScopeVersion,
			SpanAttributes, toInt64(Duration) as Duration, StatusCode, StatusMessage,
			Events.Timestamp as EventTimestamps,
			Events.Name as EventNames,
			Events.Attributes as EventAttributes,
			Links.TraceId as LinkTraceIds,
			Links.SpanId as LinkSpanIds,
			Links.TraceState as LinkTraceStates,
			Links.Attributes as LinkAttributes
		FROM otel_traces
		WHERE TraceId = ? %s
		ORDER BY Timestamp ASC
	`, tenantFilter)

	args := append([]interface{}{detailedQuery.TraceID}, tenantArgs...)

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("trace_id", detailedQuery.TraceID, "error", err.Error()).Error("failed to query detailed trace")
		return nil, fmt.Errorf("failed to query detailed trace: %w", err)
	}
	defer rows.Close()

	var spans []DetailedSpan
	for rows.Next() {
		span, err := h.scanDetailedSpan(rows)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan detailed span")
			continue
		}
		spans = append(spans, span)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating detailed trace: %w", err)
	}

	if len(spans) == 0 {
		return nil, fmt.Errorf("trace not found: %s", detailedQuery.TraceID)
	}

	// Calculate summary
	summary := h.calculateSummary(spans)

	return &DetailedTrace{
		TraceID: detailedQuery.TraceID,
		Spans:   spans,
		Summary: summary,
	}, nil
}

// scanDetailedSpan scans a row into a DetailedSpan
func (h *DetailedTraceHandler) scanDetailedSpan(scanner interface {
	Scan(dest ...interface{}) error
}) (DetailedSpan, error) {
	var span DetailedSpan
	var resourceAttrs, spanAttrs map[string]string
	var eventTimestamps []time.Time
	var eventNames []string
	var eventAttributes []map[string]string
	var linkTraceIds, linkSpanIds, linkTraceStates []string
	var linkAttributes []map[string]string

	err := scanner.Scan(
		&span.Timestamp,
		&span.TraceID,
		&span.SpanID,
		&span.ParentSpanID,
		&span.TraceState,
		&span.SpanName,
		&span.SpanKind,
		&span.ServiceName,
		&resourceAttrs,
		&span.ScopeName,
		&span.ScopeVersion,
		&spanAttrs,
		&span.Duration,
		&span.StatusCode,
		&span.StatusMessage,
		&eventTimestamps,
		&eventNames,
		&eventAttributes,
		&linkTraceIds,
		&linkSpanIds,
		&linkTraceStates,
		&linkAttributes,
	)

	if err != nil {
		return DetailedSpan{}, err
	}

	span.ResourceAttributes = resourceAttrs
	span.SpanAttributes = spanAttrs
	span.DurationMs = float64(span.Duration) / 1_000_000 // Convert nanoseconds to milliseconds

	// Parse events
	for i := range eventNames {
		event := SpanEvent{
			Name: eventNames[i],
		}
		if i < len(eventTimestamps) {
			event.Timestamp = eventTimestamps[i]
		}
		if i < len(eventAttributes) {
			event.Attributes = eventAttributes[i]
		}
		span.Events = append(span.Events, event)
	}

	// Parse links
	for i := range linkTraceIds {
		link := SpanLink{
			TraceID: linkTraceIds[i],
		}
		if i < len(linkSpanIds) {
			link.SpanID = linkSpanIds[i]
		}
		if i < len(linkTraceStates) {
			link.TraceState = linkTraceStates[i]
		}
		if i < len(linkAttributes) {
			link.Attributes = linkAttributes[i]
		}
		span.Links = append(span.Links, link)
	}

	// Extract observation type
	if obsType, ok := spanAttrs["observation.type"]; ok {
		span.ObservationType = obsType
	}

	// Calculate performance metrics
	span.PerformanceMetrics = h.calculatePerformanceMetrics(spanAttrs)

	// Calculate business metrics
	span.BusinessMetrics = h.calculateBusinessMetrics(spanAttrs)

	return span, nil
}

// calculatePerformanceMetrics extracts performance metrics from span attributes
func (h *DetailedTraceHandler) calculatePerformanceMetrics(attrs map[string]string) *PerformanceMetrics {
	metrics := &PerformanceMetrics{}

	if cacheHit, ok := attrs["cache.hit"]; ok && cacheHit == "true" {
		metrics.IsCached = true
	}

	if efficiency, ok := attrs["cache.efficiency_percentage"]; ok {
		fmt.Sscanf(efficiency, "%f", &metrics.CacheEfficiencyPercentage)
	}

	if tps, ok := attrs["performance.throughput_tokens_per_sec"]; ok {
		fmt.Sscanf(tps, "%f", &metrics.TokensPerSecond)
	}

	if category, ok := attrs["performance.latency_category"]; ok {
		metrics.LatencyCategory = category
	}

	if speedup, ok := attrs["cache.speedup_factor"]; ok {
		fmt.Sscanf(speedup, "%d", &metrics.CacheSpeedupFactor)
	}

	return metrics
}

// calculateBusinessMetrics extracts business metrics from span attributes
func (h *DetailedTraceHandler) calculateBusinessMetrics(attrs map[string]string) *BusinessMetrics {
	metrics := &BusinessMetrics{}

	if savings, ok := attrs["cost.savings_usd"]; ok {
		fmt.Sscanf(savings, "%f", &metrics.CostSavingsUSD)
	}

	if carbon, ok := attrs["carbon.saved_grams"]; ok {
		fmt.Sscanf(carbon, "%f", &metrics.CarbonSavedGrams)
	}

	if complexity, ok := attrs["business.query_complexity"]; ok {
		metrics.QueryComplexity = complexity
	}

	if confidence, ok := attrs["business.domain_confidence"]; ok {
		metrics.DomainConfidence = confidence
	}

	return metrics
}

// calculateSummary calculates summary metrics for the trace
func (h *DetailedTraceHandler) calculateSummary(spans []DetailedSpan) DetailedTraceSummary {
	summary := DetailedTraceSummary{
		TotalSpans: len(spans),
	}

	var maxDuration int64
	var totalCost float64
	cacheHits := 0

	for _, span := range spans {
		if span.Duration > maxDuration {
			maxDuration = span.Duration
		}

		// Use new detailed cost attributes
		if cost, ok := span.SpanAttributes["cost.estimated_usd"]; ok {
			var costVal float64
			fmt.Sscanf(cost, "%f", &costVal)
			totalCost += costVal
		} else if cost, ok := span.SpanAttributes["llm.cost.total"]; ok {
			// Fallback to legacy attribute for backward compatibility
			var costVal float64
			fmt.Sscanf(cost, "%f", &costVal)
			totalCost += costVal
		}

		if cacheHit, ok := span.SpanAttributes["cache.hit"]; ok && cacheHit == "true" {
			cacheHits++
		}
	}

	summary.TotalDurationMs = float64(maxDuration) / 1_000_000
	summary.TotalCostUSD = totalCost

	if len(spans) > 0 {
		summary.CacheHitRate = float64(cacheHits) / float64(len(spans))
	}

	return summary
}
