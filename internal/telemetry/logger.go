package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// TelemetryLogger wraps the standard logger to emit OTEL logs with tenant context.
// This maintains backward compatibility while adding OpenTelemetry export.
type TelemetryLogger struct {
	otelLogger  log.Logger
	tenantAttrs []attribute.KeyValue
}

// NewTelemetryLogger creates a logger that emits both standard logs and OTEL logs.
func NewTelemetryLogger(provider *sdklog.LoggerProvider, tenantID, tenantType, instanceOwner string) *TelemetryLogger {
	otelLogger := provider.Logger("everstack-gateway")

	return &TelemetryLogger{
		otelLogger: otelLogger,
		tenantAttrs: []attribute.KeyValue{
			attribute.String("tenant.id", tenantID),
			attribute.String("tenant.type", tenantType),
			attribute.String("instance.owner", instanceOwner),
		},
	}
}

// EmitLog emits a structured log entry to OTEL.
func (t *TelemetryLogger) EmitLog(ctx context.Context, severity log.Severity, message string, attrs ...attribute.KeyValue) {
	fmt.Printf("[OTEL DEBUG] EmitLog called: severity=%v, message=%s, attrs_count=%d\n", severity, message, len(attrs))

	// Combine tenant attributes with provided attributes
	allAttrs := make([]log.KeyValue, 0, len(t.tenantAttrs)+len(attrs))

	// Add tenant attributes
	for _, attr := range t.tenantAttrs {
		allAttrs = append(allAttrs, log.KeyValue{
			Key:   string(attr.Key),
			Value: convertAttributeValue(attr.Value),
		})
	}

	// Add provided attributes
	for _, attr := range attrs {
		allAttrs = append(allAttrs, log.KeyValue{
			Key:   string(attr.Key),
			Value: convertAttributeValue(attr.Value),
		})
	}

	// Create log record
	record := log.Record{}
	record.SetTimestamp(time.Now())
	record.SetBody(log.StringValue(message))
	record.SetSeverity(severity)
	record.AddAttributes(allAttrs...)

	// Emit log
	fmt.Printf("[OTEL DEBUG] About to emit log record with %d attributes\n", len(allAttrs))
	t.otelLogger.Emit(ctx, record)
	fmt.Printf("[OTEL DEBUG] Log record emitted to SDK successfully\n")
	fmt.Printf("[OTEL DEBUG] With SimpleProcessor, export should happen immediately to collector\n")
	// Note: with SimpleProcessor, export happens synchronously
}

// Info emits an info-level log.
func (t *TelemetryLogger) Info(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	t.EmitLog(ctx, log.SeverityInfo, message, attrs...)
}

// Warn emits a warning-level log.
func (t *TelemetryLogger) Warn(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	t.EmitLog(ctx, log.SeverityWarn, message, attrs...)
}

// Error emits an error-level log.
func (t *TelemetryLogger) Error(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	t.EmitLog(ctx, log.SeverityError, message, attrs...)
}

// Debug emits a debug-level log.
func (t *TelemetryLogger) Debug(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	t.EmitLog(ctx, log.SeverityDebug, message, attrs...)
}

// convertAttributeValue converts attribute.Value to log.Value
func convertAttributeValue(v attribute.Value) log.Value {
	switch v.Type() {
	case attribute.BOOL:
		return log.BoolValue(v.AsBool())
	case attribute.INT64:
		return log.Int64Value(v.AsInt64())
	case attribute.FLOAT64:
		return log.Float64Value(v.AsFloat64())
	case attribute.STRING:
		return log.StringValue(v.AsString())
	default:
		return log.StringValue(v.AsString())
	}
}
