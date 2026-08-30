package logger

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Package-level OTEL logger provider (set externally to avoid import cycles)
var globalOTELProvider *sdklog.LoggerProvider

// SetOTELProvider sets the OTEL logger provider for the hook
func SetOTELProvider(provider *sdklog.LoggerProvider) {
	globalOTELProvider = provider
}

// OTELHook forwards logrus logs to OpenTelemetry
type OTELHook struct {
	logger otellog.Logger
}

// NewOTELHook creates a new hook that forwards logs to OTEL
func NewOTELHook() *OTELHook {
	if globalOTELProvider == nil {
		return nil
	}

	// Create a logger from the provider
	logger := globalOTELProvider.Logger("everstack-gateway")

	return &OTELHook{
		logger: logger,
	}
}

// Levels returns the log levels this hook handles (all levels)
func (h *OTELHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire is called when a log entry is made
func (h *OTELHook) Fire(entry *logrus.Entry) error {
	if h.logger == nil {
		return nil
	}

	// Filter out logs below the minimum level (e.g., skip DEBUG/TRACE)
	if entry.Level > minOTELLevel {
		return nil
	}

	// Only forward logs that are explicitly marked as operational
	// All other logs (system, unmarked) are excluded from OTEL
	category, hasCategory := entry.Data["log_category"].(string)
	if !hasCategory || category != "operational" {
		return nil
	}

	// Convert logrus severity to OTEL severity
	severity := logrusToOTELSeverity(entry.Level)

	// Convert logrus fields to OTEL attributes
	attrs := make([]otellog.KeyValue, 0, len(entry.Data)+1)

	// Add all logrus fields as attributes
	for key, value := range entry.Data {
		attrs = append(attrs, logValueToKeyValue(key, value))
	}

	// Add source location if available
	if entry.HasCaller() {
		attrs = append(attrs,
			otellog.String("source.function", entry.Caller.Function),
			otellog.String("source.file", entry.Caller.File),
			otellog.Int("source.line", entry.Caller.Line),
		)
	}

	// Add event if present (for fast indexed filtering in ClickHouse)
	// Export as 'event' (not 'log_event') for clean LogAttributes structure
	if logEvent, ok := entry.Data["log_event"].(string); ok {
		attrs = append(attrs, otellog.String("event", logEvent))
	}

	// Add payload if present (structured nested context)
	// Export as 'payload' (not 'log_payload') to avoid duplication in LogAttributes
	if payload, ok := entry.Data["log_payload"].(string); ok {
		attrs = append(attrs, otellog.String("payload", payload))
	}

	// Emit the log record
	var record otellog.Record
	record.SetTimestamp(entry.Time)
	record.SetSeverity(severity)
	record.SetSeverityText(entry.Level.String())
	record.SetBody(otellog.StringValue(entry.Message))
	record.AddAttributes(attrs...)

	ctx := context.Background()
	h.logger.Emit(ctx, record)

	return nil
}

// logrusToOTELSeverity converts logrus levels to OTEL severity
func logrusToOTELSeverity(level logrus.Level) otellog.Severity {
	switch level {
	case logrus.TraceLevel:
		return otellog.SeverityTrace
	case logrus.DebugLevel:
		return otellog.SeverityDebug
	case logrus.InfoLevel:
		return otellog.SeverityInfo
	case logrus.WarnLevel:
		return otellog.SeverityWarn
	case logrus.ErrorLevel:
		return otellog.SeverityError
	case logrus.FatalLevel:
		return otellog.SeverityFatal
	case logrus.PanicLevel:
		return otellog.SeverityFatal4
	default:
		return otellog.SeverityInfo
	}
}

// logValueToKeyValue converts a logrus field value to an OTEL KeyValue
func logValueToKeyValue(key string, value interface{}) otellog.KeyValue {
	switch v := value.(type) {
	case string:
		return otellog.String(key, v)
	case int:
		return otellog.Int(key, v)
	case int64:
		return otellog.Int64(key, v)
	case float64:
		return otellog.Float64(key, v)
	case bool:
		return otellog.Bool(key, v)
	case time.Time:
		return otellog.String(key, v.Format(time.RFC3339Nano))
	case time.Duration:
		return otellog.Int64(key, v.Milliseconds())
	default:
		// For all other types, convert to string
		return otellog.String(key, fmt.Sprint(v))
	}
}

// EnableOTELForwarding adds the OTEL hook to the global logrus logger
// By default, only forwards INFO and above to OTEL (skips DEBUG/TRACE initialization logs)
func EnableOTELForwarding() {
	hook := NewOTELHook()
	if hook == nil {
		return
	}
	if hook.logger == nil {
		return
	}
	(*logrus.Logger)(log).AddHook(hook)
}

// SetOTELMinLevel sets the minimum log level that will be forwarded to OTEL
// This is useful to filter out verbose DEBUG/TRACE logs from operational telemetry
var minOTELLevel = logrus.InfoLevel

func SetOTELMinLevel(level logrus.Level) {
	minOTELLevel = level
}
