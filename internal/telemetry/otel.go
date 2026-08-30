package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds the configuration for OpenTelemetry initialization.
type Config struct {
	ServiceName           string
	ServiceVersion        string
	Mode                  string // "embedded" or "external" (default: external)
	CollectorURL          string // OTLP collector endpoint (e.g., "localhost:4317")
	TenantID              string // Gateway owner ID (dynamically fetched from license context)
	TenantType            string // "self_hosted" or "cloud"
	InstanceOwner         string // "user" or "everstack"
	DeploymentEnvironment string // "production", "dev", "staging", etc.
	DirectExport          DirectExportConfig
	TracingConfig         *TracingConfig // Optional tracing configuration
}

// DirectExportConfig holds configuration for direct ClickHouse export via collector sidecar
type DirectExportConfig struct {
	Enabled        bool
	ClickHouseHost string // e.g., "clickhouse.internal" (just hostname, no protocol)
	Database       string // e.g., "everstack"
	Username       string
	Password       string
}

// Telemetry holds the OTEL providers
type Telemetry struct {
	LoggerProvider *sdklog.LoggerProvider
	TracerProvider trace.TracerProvider
}

// InitOTEL initializes OpenTelemetry based on deployment mode.
// Returns Telemetry providers and a shutdown function.
func InitOTEL(cfg Config) (*Telemetry, func(), error) {
	// Default to external mode if not specified
	if cfg.Mode == "" {
		cfg.Mode = "external"
	}

	// Route to appropriate initialization based on mode
	if cfg.Mode == "embedded" && cfg.DirectExport.Enabled {
		return initEmbeddedOTEL(cfg)
	}
	return initExternalOTEL(cfg)
}

// initExternalOTEL initializes OTEL with external collector (cloud deployments)
// Provides rich observability with logs + traces
func initExternalOTEL(cfg Config) (*Telemetry, func(), error) {
	ctx := context.Background()

	// Create resource with tenant attributes
	res, err := createResource(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Normalize collector URL to host:port
	collectorEndpoint := normalizeCollectorURL(cfg.CollectorURL)

	// Create OTLP log exporter (async with BatchProcessor for low latency)
	// No grpc.WithBlock() — connect lazily so startup is never blocked by an
	// unreachable collector. Failed exports are retried by the batch processor.
	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(collectorEndpoint),
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OTLP log exporter: %w", err)
	}

	// Use BatchProcessor to avoid blocking the request path
	// Logs are buffered and sent asynchronously in batches
	logProcessor := sdklog.NewBatchProcessor(logExporter,
		sdklog.WithExportInterval(100*time.Millisecond), // Export every 100ms
		sdklog.WithExportMaxBatchSize(512),              // Max 512 logs per batch
		sdklog.WithExportTimeout(5*time.Second),         // Timeout for export
		sdklog.WithMaxQueueSize(2048),                   // Buffer up to 2048 logs
	)
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(logProcessor),
	)

	// Create trace provider if tracing config exists or EnableTraces is set
	// TracingConfig presence automatically enables tracing
	var traceProvider trace.TracerProvider
	tracingEnabled := cfg.TracingConfig != nil

	if tracingEnabled {
		// Set global tracing config if provided
		if cfg.TracingConfig != nil {
			SetGlobalTracingConfig(cfg.TracingConfig)
		}

		traceExporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(collectorEndpoint),
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithTimeout(5*time.Second),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
		}

		traceProcessor := sdktrace.NewBatchSpanProcessor(traceExporter)
		traceProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			// Order matters: tenant stamp runs before the batch exporter sees
			// the span, so every exported span carries tenant.id as a
			// SpanAttribute (which is what every read-side handler filters on).
			sdktrace.WithSpanProcessor(newTenantSpanProcessor(cfg.TenantID)),
			sdktrace.WithSpanProcessor(traceProcessor),
		)
	} else {
		traceProvider = trace.NewNoopTracerProvider()
	}

	// Set global providers
	SetGlobalLoggerProvider(loggerProvider)
	SetGlobalTracerProvider(traceProvider)

	// Shutdown function
	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := loggerProvider.Shutdown(shutdownCtx); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to shutdown logger provider")
		}

		if tp, ok := traceProvider.(*sdktrace.TracerProvider); ok {
			if err := tp.Shutdown(shutdownCtx); err != nil {
				logger.WithFields("error", err.Error()).Warn("failed to shutdown tracer provider")
			}
		}
	}

	return &Telemetry{
		LoggerProvider: loggerProvider,
		TracerProvider: traceProvider,
	}, shutdown, nil
}

// initEmbeddedOTEL initializes OTEL with local collector sidecar (self-hosted deployments)
// Optimized for low latency - logs via local collector, optional tracing
func initEmbeddedOTEL(cfg Config) (*Telemetry, func(), error) {
	ctx := context.Background()

	// Create resource with tenant attributes
	res, err := createResource(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Determine collector endpoint (default to localhost for embedded mode)
	collectorEndpoint := "localhost:4317"
	if cfg.CollectorURL != "" {
		collectorEndpoint = normalizeCollectorURL(cfg.CollectorURL)
	}

	// Create OTLP log exporter to local collector sidecar
	// The collector handles ClickHouse insertion with official exporter
	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(collectorEndpoint),
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		// Use no-op providers if collector not available
		loggerProvider := sdklog.NewLoggerProvider()
		traceProvider := trace.NewNoopTracerProvider()

		SetGlobalLoggerProvider(loggerProvider)
		SetGlobalTracerProvider(traceProvider)

		return &Telemetry{
			LoggerProvider: loggerProvider,
			TracerProvider: traceProvider,
		}, func() {}, nil
	}

	// Use BatchProcessor for async, low-latency delivery to local collector
	// This prevents blocking the request path while still ensuring logs are delivered
	logProcessor := sdklog.NewBatchProcessor(logExporter,
		sdklog.WithExportInterval(100*time.Millisecond), // Export every 100ms
		sdklog.WithExportMaxBatchSize(512),              // Max 512 logs per batch
		sdklog.WithExportTimeout(5*time.Second),         // Timeout for export
		sdklog.WithMaxQueueSize(2048),                   // Buffer up to 2048 logs
	)
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(logProcessor),
	)

	// Create trace provider if tracing config exists
	var traceProvider trace.TracerProvider
	tracingEnabled := cfg.TracingConfig != nil

	if tracingEnabled {
		// Set global tracing config if provided
		if cfg.TracingConfig != nil {
			SetGlobalTracingConfig(cfg.TracingConfig)
		}

		logger.WithFields(
			"endpoint", collectorEndpoint,
			"sampling_rate", cfg.TracingConfig.SamplingRate,
			"granularity", cfg.TracingConfig.Granularity,
		).Info("Creating OTLP trace exporter for embedded mode")

		traceExporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(collectorEndpoint),
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithTimeout(5*time.Second),
		)
		if err != nil {
			logger.WithFields("error", err.Error()).Error("Failed to create OTLP trace exporter for embedded mode - traces will not be exported")
			traceProvider = trace.NewNoopTracerProvider()
		} else {
			traceProcessor := sdktrace.NewBatchSpanProcessor(traceExporter)
			traceProvider = sdktrace.NewTracerProvider(
				sdktrace.WithResource(res),
				// See initExternalOTEL for rationale — tenant stamp must run
				// before the batch exporter so every span carries tenant.id
				// as a SpanAttribute, not just resource-level.
				sdktrace.WithSpanProcessor(newTenantSpanProcessor(cfg.TenantID)),
				sdktrace.WithSpanProcessor(traceProcessor),
			)
			logger.Info("OTLP trace exporter created successfully - traces will be exported to collector")
		}
	} else {
		logger.Debug("No tracing config provided - using no-op tracer for low latency")
		// No tracing config - use no-op for low latency (self-hosted priority ~11μs like Bifrost)
		traceProvider = trace.NewNoopTracerProvider()
	}

	// Set global providers
	SetGlobalLoggerProvider(loggerProvider)
	SetGlobalTracerProvider(traceProvider)

	// Shutdown function
	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := loggerProvider.Shutdown(shutdownCtx); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to shutdown logger provider")
		}

		// Shutdown trace provider if it's not a no-op
		if tracingEnabled {
			if tp, ok := traceProvider.(*sdktrace.TracerProvider); ok {
				if err := tp.Shutdown(shutdownCtx); err != nil {
					logger.WithFields("error", err.Error()).Warn("failed to shutdown tracer provider")
				}
			}
		}
	}

	return &Telemetry{
		LoggerProvider: loggerProvider,
		TracerProvider: traceProvider,
	}, shutdown, nil
}

// createResource creates an OTEL resource with service and tenant attributes
func createResource(cfg Config) (*resource.Resource, error) {
	environment := strings.ToLower(strings.TrimSpace(cfg.DeploymentEnvironment))
	if environment == "" || environment == "prod" {
		environment = "production"
	}
	return resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			attribute.String("deployment.mode", cfg.Mode),
			attribute.String("tenant.id", cfg.TenantID),
			attribute.String("tenant.type", cfg.TenantType),
			attribute.String("instance.owner", cfg.InstanceOwner),
			attribute.String("deployment.environment", environment),
		),
	)
}

// normalizeCollectorURL converts various collector URL formats to host:port
func normalizeCollectorURL(url string) string {
	// Remove protocol prefixes
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")

	// Convert HTTP port to gRPC port
	if strings.HasSuffix(url, ":4318") {
		url = strings.TrimSuffix(url, ":4318") + ":4317"
	}

	// Add default gRPC port if not specified
	if !strings.Contains(url, ":") {
		url = url + ":4317"
	}

	return url
}

// Global provider storage
var (
	globalLoggerProvider *sdklog.LoggerProvider
	globalTracerProvider trace.TracerProvider
)

// SetGlobalLoggerProvider sets the global logger provider
func SetGlobalLoggerProvider(p *sdklog.LoggerProvider) {
	globalLoggerProvider = p
}

// GetGlobalLoggerProvider gets the global logger provider
func GetGlobalLoggerProvider() *sdklog.LoggerProvider {
	return globalLoggerProvider
}

// SetGlobalTracerProvider sets the global tracer provider
func SetGlobalTracerProvider(p trace.TracerProvider) {
	globalTracerProvider = p
}

// GetGlobalTracerProvider gets the global tracer provider
func GetGlobalTracerProvider() trace.TracerProvider {
	if globalTracerProvider == nil {
		return trace.NewNoopTracerProvider()
	}
	return globalTracerProvider
}

// WithLoggerProvider stores the LoggerProvider in context
func WithLoggerProvider(ctx context.Context, provider *sdklog.LoggerProvider) context.Context {
	return context.WithValue(ctx, loggerProviderKey, provider)
}

// GetLoggerProvider retrieves the LoggerProvider from context
func GetLoggerProvider(ctx context.Context) *sdklog.LoggerProvider {
	if provider, ok := ctx.Value(loggerProviderKey).(*sdklog.LoggerProvider); ok {
		return provider
	}
	return nil
}

// contextKey is a private type for context keys
type contextKey string

const loggerProviderKey contextKey = "otel-logger-provider"
