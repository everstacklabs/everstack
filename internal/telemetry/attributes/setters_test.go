package attributes

import (
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSetCostDetails tests the SetCostDetails function
func TestSetCostDetails(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := tp.Tracer("test")

	_, span := tracer.Start(nil, "test-span")

	SetCostDetails(span, 0.001, 0.0005, 0.0005, 0.12, "pay_per_token")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := spans[0].Attributes
	found := false
	for _, attr := range attrs {
		if string(attr.Key) == CostEstimatedUSD {
			found = true
			if attr.Value.AsFloat64() != 0.001 {
				t.Errorf("expected cost 0.001, got %v", attr.Value.AsFloat64())
			}
		}
	}

	if !found {
		t.Error("cost.estimated_usd attribute not found")
	}
}

// TestSetPerformanceMetrics tests the SetPerformanceMetrics function
func TestSetPerformanceMetrics(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := tp.Tracer("test")

	_, span := tracer.Start(nil, "test-span")

	SetPerformanceMetrics(span, 12.5, 8.5, 15.2, 18.7, 4950, "excellent")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := spans[0].Attributes
	foundTTFB := false
	foundP50 := false
	foundCategory := false

	for _, attr := range attrs {
		switch string(attr.Key) {
		case PerformanceTTFBMs:
			foundTTFB = true
			if attr.Value.AsFloat64() != 12.5 {
				t.Errorf("expected TTFB 12.5, got %v", attr.Value.AsFloat64())
			}
		case PerformanceLatencyP50Ms:
			foundP50 = true
			if attr.Value.AsFloat64() != 8.5 {
				t.Errorf("expected P50 8.5, got %v", attr.Value.AsFloat64())
			}
		case PerformanceLatencyCategory:
			foundCategory = true
			if attr.Value.AsString() != "excellent" {
				t.Errorf("expected category 'excellent', got %v", attr.Value.AsString())
			}
		}
	}

	if !foundTTFB {
		t.Error("performance.ttfb_ms attribute not found")
	}
	if !foundP50 {
		t.Error("performance.latency_p50_ms attribute not found")
	}
	if !foundCategory {
		t.Error("performance.latency_category attribute not found")
	}
}

// TestSetBusinessMetrics tests the SetBusinessMetrics function
func TestSetBusinessMetrics(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := tp.Tracer("test")

	_, span := tracer.Start(nil, "test-span")

	SetBusinessMetrics(span, "qa_simple", "geography", "factual", 0.98)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := spans[0].Attributes
	foundUseCase := false
	foundDomain := false
	foundQuality := false

	for _, attr := range attrs {
		switch string(attr.Key) {
		case BusinessUseCase:
			foundUseCase = true
			if attr.Value.AsString() != "qa_simple" {
				t.Errorf("expected use case 'qa_simple', got %v", attr.Value.AsString())
			}
		case BusinessDomain:
			foundDomain = true
			if attr.Value.AsString() != "geography" {
				t.Errorf("expected domain 'geography', got %v", attr.Value.AsString())
			}
		case BusinessResponseQualityScore:
			foundQuality = true
			if attr.Value.AsFloat64() != 0.98 {
				t.Errorf("expected quality 0.98, got %v", attr.Value.AsFloat64())
			}
		}
	}

	if !foundUseCase {
		t.Error("business.use_case attribute not found")
	}
	if !foundDomain {
		t.Error("business.domain attribute not found")
	}
	if !foundQuality {
		t.Error("business.response_quality_score attribute not found")
	}
}

// TestSetCacheAdvancedMetrics tests the SetCacheAdvancedMetrics function
func TestSetCacheAdvancedMetrics(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := tp.Tracer("test")

	_, span := tracer.Start(nil, "test-span")

	SetCacheAdvancedMetrics(span, "redis", "gzip", 0.65, 0.87, 99.2, 45, 1, "corr_01kccn99ppfmqtcb0vgpmm2ejy")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := spans[0].Attributes
	foundBackend := false
	foundCompression := false
	foundEfficiency := false

	for _, attr := range attrs {
		switch string(attr.Key) {
		case CacheStorageBackend:
			foundBackend = true
			if attr.Value.AsString() != "redis" {
				t.Errorf("expected backend 'redis', got %v", attr.Value.AsString())
			}
		case CacheCompression:
			foundCompression = true
			if attr.Value.AsString() != "gzip" {
				t.Errorf("expected compression 'gzip', got %v", attr.Value.AsString())
			}
		case CacheEfficiencyPercentage:
			foundEfficiency = true
			if attr.Value.AsFloat64() != 99.2 {
				t.Errorf("expected efficiency 99.2, got %v", attr.Value.AsFloat64())
			}
		}
	}

	if !foundBackend {
		t.Error("cache.storage_backend attribute not found")
	}
	if !foundCompression {
		t.Error("cache.compression attribute not found")
	}
	if !foundEfficiency {
		t.Error("cache.efficiency_percentage attribute not found")
	}
}

// TestSetRequestDetails tests the SetRequestDetails function
func TestSetRequestDetails(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := tp.Tracer("test")

	_, span := tracer.Start(nil, "test-span")

	SetRequestDetails(span, "corr_123", `{"role":"user","content":"test"}`, "127.0.0.1", "test-agent", 5, 70)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := spans[0].Attributes
	foundID := false
	foundTokens := false

	for _, attr := range attrs {
		switch string(attr.Key) {
		case RequestID:
			foundID = true
			if attr.Value.AsString() != "corr_123" {
				t.Errorf("expected request ID 'corr_123', got %v", attr.Value.AsString())
			}
		case RequestInputTokens:
			foundTokens = true
			if attr.Value.AsInt64() != 5 {
				t.Errorf("expected tokens 5, got %v", attr.Value.AsInt64())
			}
		}
	}

	if !foundID {
		t.Error("request.id attribute not found")
	}
	if !foundTokens {
		t.Error("request.input_tokens attribute not found")
	}
}

// TestSetResponseDetails tests the SetResponseDetails function
func TestSetResponseDetails(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := tp.Tracer("test")

	_, span := tracer.Start(nil, "test-span")

	SetResponseDetails(span, `{"role":"assistant","content":"response"}`, "complete", "command-a-03-2025", 42, 215)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	attrs := spans[0].Attributes
	foundFinish := false
	foundModel := false
	foundTokens := false

	for _, attr := range attrs {
		switch string(attr.Key) {
		case ResponseFinishReason:
			foundFinish = true
			if attr.Value.AsString() != "complete" {
				t.Errorf("expected finish reason 'complete', got %v", attr.Value.AsString())
			}
		case ResponseModelUsed:
			foundModel = true
			if attr.Value.AsString() != "command-a-03-2025" {
				t.Errorf("expected model 'command-a-03-2025', got %v", attr.Value.AsString())
			}
		case ResponseOutputTokens:
			foundTokens = true
			if attr.Value.AsInt64() != 42 {
				t.Errorf("expected tokens 42, got %v", attr.Value.AsInt64())
			}
		}
	}

	if !foundFinish {
		t.Error("response.finish_reason attribute not found")
	}
	if !foundModel {
		t.Error("response.model_used attribute not found")
	}
	if !foundTokens {
		t.Error("response.output_tokens attribute not found")
	}
}
