package validator

import (
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/config/network"
	"github.com/everstacklabs/everstack/internal/lib/idgenerator"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Server configuration structs - these are ready to use
type ServerConfig struct {
	Config        ServerConfigConfig  `mapstructure:"config"`
	Log           *logger.Config      // Will be *logger.Config when logger is implemented
	Metrics       MetricsConfig       `mapstructure:"metrics"`
	Tracing       TracingConfig       `mapstructure:"tracing"`
	Telemetry     TelemetryConfig     `mapstructure:"telemetry"`
	TLS           network.TLS         `mapstructure:"tls"`
	CORS          CORSConfig          `mapstructure:"cors"`
	CustomHeaders CustomHeadersConfig `mapstructure:"custom_headers"`
	Quotas        QuotasConfig        `mapstructure:"quotas"`
	Machine       *idgenerator.Config // Will be *idgenerator.Config when idgenerator is implemented
	Catalog       CatalogSyncConfig   `mapstructure:"catalog"`
}

type ServerConfigConfig struct {
	Port                  int      `mapstructure:"port"`
	ExternalPort          int      `mapstructure:"external_port"`
	ExternalDomain        string   `mapstructure:"external_domain"`
	EnforceExternalDomain bool     `mapstructure:"enforce_external_domain"` // When true, rejects requests not matching external_domain
	ExternalSecure        bool     `mapstructure:"external_secure"`
	PublicHostHeaders     []string `mapstructure:"public_host_headers"`
	InstanceHostHeaders   []string `mapstructure:"instance_host_headers"`
}

type MetricsConfig struct {
	Type     string `mapstructure:"type"`
	Enabled  bool   `mapstructure:"enabled"`
	Port     int    `mapstructure:"port"`
	Path     string `mapstructure:"path"`
	Interval string `mapstructure:"interval"`
}

type TracingConfig struct {
	Type     string     `mapstructure:"type"`
	Fraction float64    `mapstructure:"fraction"`
	Enabled  bool       `mapstructure:"enabled"`
	OTEL     OtelConfig `mapstructure:"otel"`
}

type OtelConfig struct {
	Endpoint       string        `mapstructure:"endpoint"`
	ServiceName    string        `mapstructure:"service_name"`
	ServiceVersion string        `mapstructure:"service_version"`
	Timeout        string        `mapstructure:"timeout"`
	Retry          RetryConfig   `mapstructure:"retry"`
	TLS            OtelTLSConfig `mapstructure:"tls"`
}

type RetryConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	MaxAttempts     int    `mapstructure:"max_attempts"`
	BackoffDuration string `mapstructure:"backoff_duration"`
}

type OtelTLSConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	CACert             string `mapstructure:"ca_cert"`
	ClientCert         string `mapstructure:"client_cert"`
	ClientKey          string `mapstructure:"client_key"`
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify"`
}

type TelemetryConfig struct {
	Endpoint string      `mapstructure:"endpoint"`
	Types    []string    `mapstructure:"types"`
	Batch    BatchConfig `mapstructure:"batch"`
	// OTEL-specific configuration for OpenTelemetry logging
	OTEL OTELConfig `mapstructure:"otel"`
}

type BatchConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	MaxSize  int    `mapstructure:"max_size"`
	Interval string `mapstructure:"interval"`
}

// OTELConfig configures OpenTelemetry logging and tracing with OTLP exporter
type OTELConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Mode         string `mapstructure:"mode"`          // "embedded" or "external" (default: external)
	CollectorURL string `mapstructure:"collector_url"` // OTLP endpoint for external mode (e.g., "otel-collector:4317")
	ServiceName  string `mapstructure:"service_name"`
	// TenantID is dynamically fetched from license service/context (not configured)
	TenantType    string                 `mapstructure:"tenant_type"`    // "self_hosted" or "cloud"
	InstanceOwner string                 `mapstructure:"instance_owner"` // "user" or "everstack"
	EnableTraces  bool                   `mapstructure:"enable_traces"`  // Enable distributed tracing (default: false for embedded, true for external)
	DirectExport  OTELDirectExportConfig `mapstructure:"direct_export"`  // For embedded mode (self-hosted)
	Tracing       *OTELTracingConfig     `mapstructure:"tracing"`        // Optional distributed tracing configuration
}

// OTELDirectExportConfig configures local collector sidecar for embedded mode
// ClickHouse connection details are derived from database.clickhouse config
type OTELDirectExportConfig struct {
	Enabled bool `mapstructure:"enabled"` // Enable direct export to ClickHouse via embedded collector
}

// OTELTracingConfig configures distributed tracing behavior
type OTELTracingConfig struct {
	SamplingRate       float64 `mapstructure:"sampling_rate"`        // 0.0 to 1.0 (1.0 = trace 100% of requests)
	Granularity        string  `mapstructure:"granularity"`          // "minimal", "standard", "detailed"
	TraceProviderCalls bool    `mapstructure:"trace_provider_calls"` // Trace individual provider HTTP requests
	TraceStreamChunks  bool    `mapstructure:"trace_stream_chunks"`  // Trace individual streaming chunks
	TraceFallbacks     bool    `mapstructure:"trace_fallbacks"`      // Create child spans for fallback attempts
	TraceKeyRotation   bool    `mapstructure:"trace_key_rotation"`   // Trace key rotation attempts
}

type CORSConfig struct {
	Enabled          bool     `mapstructure:"enabled"`
	AllowedOrigins   []string `mapstructure:"allowed_origins"`
	AllowedMethods   []string `mapstructure:"allowed_methods"`
	AllowedHeaders   []string `mapstructure:"allowed_headers"`
	ExposedHeaders   []string `mapstructure:"exposed_headers"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
	MaxAge           string   `mapstructure:"max_age"`
}

type CustomHeadersConfig struct {
	Enabled bool                `mapstructure:"enabled"`
	Headers []map[string]string `mapstructure:"headers"`
}

type QuotasConfig struct {
	Access    QuotaAccessConfig    `mapstructure:"access"`
	Execution QuotaExecutionConfig `mapstructure:"execution"`
}

type QuotaAccessConfig struct {
	Enabled               bool   `mapstructure:"enabled"`
	MinFrequency          string `mapstructure:"min_frequency"`
	MaxBulkSize           int    `mapstructure:"max_bulk_size"`
	ExhaustedCookieKey    string `mapstructure:"exhausted_cookie_key"`
	ExhaustedCookieMaxAge string `mapstructure:"exhausted_cookie_max_age"`
}

type QuotaExecutionConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	MinFrequency string `mapstructure:"min_frequency"`
	MaxBulkSize  int    `mapstructure:"max_bulk_size"`
}

// CatalogSyncConfig represents catalog sync configuration
type CatalogSyncConfig struct {
	Source           string `mapstructure:"source"`            // "remote" or "local"
	RemoteURL        string `mapstructure:"remote_url"`        // URL for remote catalog
	LocalPath        string `mapstructure:"local_path"`        // Path for local catalog
	EnableAutoSync   bool   `mapstructure:"enable_auto_sync"`  // Enable automatic syncing
	SyncInterval     string `mapstructure:"sync_interval"`     // Sync interval (e.g., "5m")
	Channel          string `mapstructure:"channel"`           // Signed release channel
	PublicKey        string `mapstructure:"public_key"`        // Singular base64 Ed25519 key
	PublicKeys       string `mapstructure:"public_keys"`       // Comma-separated rotation set
	RequireSignature bool   `mapstructure:"require_signature"` // Reject unsigned distribution
}

func defaultCatalogSyncConfig() CatalogSyncConfig {
	return CatalogSyncConfig{
		Source:           "remote",
		RemoteURL:        "https://catalog.everstack.ai/v1",
		LocalPath:        "model-catalog",
		EnableAutoSync:   true,
		SyncInterval:     "5m",
		Channel:          "stable",
		RequireSignature: true,
	}
}

// LoadServerConfig validates user configuration against defaults
func LoadServerConfig(userConfig *Config, defaults *DefaultConfigs) (*ServerConfig, error) {
	// Validate that user provided server configuration
	if userConfig.Server == nil {
		return nil, fmt.Errorf("server configuration is required in gateway.yaml")
	}

	// Load the embedded defaults to use as validation reference
	var defaultServerConfig ServerConfig
	if len(defaults.Server) > 0 {
		if err := loadYAMLIntoStruct(defaults.Server, &defaultServerConfig); err != nil {
			return nil, fmt.Errorf("failed to load server defaults for validation: %w", err)
		}
	}

	mergeCatalogDefaults(&userConfig.Server.Catalog, defaultServerConfig.Catalog)

	// Validate the user's server configuration
	if err := validateServerConfigAgainstDefaults(*userConfig.Server); err != nil {
		return nil, fmt.Errorf("server configuration validation failed: %w", err)
	}

	// Validate the configuration structure
	if err := userConfig.Server.Validate(); err != nil {
		return nil, fmt.Errorf("invalid server config: %w", err)
	}

	return userConfig.Server, nil
}

func mergeCatalogDefaults(catalog *CatalogSyncConfig, defaults CatalogSyncConfig) {
	sourceWasEmpty := catalog.Source == ""
	if catalog.Source == "" {
		catalog.Source = defaults.Source
	}
	if catalog.RemoteURL == "" {
		catalog.RemoteURL = defaults.RemoteURL
	}
	if catalog.LocalPath == "" {
		catalog.LocalPath = defaults.LocalPath
	}
	if catalog.SyncInterval == "" {
		catalog.SyncInterval = defaults.SyncInterval
	}
	if catalog.Channel == "" {
		catalog.Channel = defaults.Channel
	}
	if catalog.PublicKey == "" {
		catalog.PublicKey = defaults.PublicKey
	}
	if catalog.PublicKeys == "" {
		catalog.PublicKeys = defaults.PublicKeys
	}
	// LoadConfig applies boolean defaults with Viper, which preserves explicit
	// false values. This fallback covers direct programmatic Config construction.
	if sourceWasEmpty {
		catalog.EnableAutoSync = defaults.EnableAutoSync
		catalog.RequireSignature = defaults.RequireSignature
	}
}

// validateServerConfigAgainstDefaults validates user config against default specifications
func validateServerConfigAgainstDefaults(userConfig ServerConfig) error {
	var errors []string

	// Validate port configuration
	if userConfig.Config.Port <= 0 || userConfig.Config.Port > 65535 {
		errors = append(errors, fmt.Sprintf("invalid server port: %d (must be between 1-65535)", userConfig.Config.Port))
	}

	// Validate TLS configuration if enabled
	if userConfig.TLS.Enabled {
		if userConfig.TLS.CertPath == "" && userConfig.TLS.Cert == nil {
			errors = append(errors, "TLS is enabled but no certificate provided (cert_path or cert required)")
		}
		if userConfig.TLS.KeyPath == "" && userConfig.TLS.Key == nil {
			errors = append(errors, "TLS is enabled but no private key provided (key_path or key required)")
		}
	}

	// Validate CORS configuration if enabled
	if userConfig.CORS.Enabled {
		if len(userConfig.CORS.AllowedOrigins) == 0 {
			errors = append(errors, "CORS is enabled but no allowed origins specified")
		}
		if len(userConfig.CORS.AllowedMethods) == 0 {
			errors = append(errors, "CORS is enabled but no allowed methods specified")
		}
	}

	// Validate metrics configuration if enabled
	if userConfig.Metrics.Enabled {
		if userConfig.Metrics.Port <= 0 || userConfig.Metrics.Port > 65535 {
			errors = append(errors, fmt.Sprintf("invalid metrics port: %d (must be between 1-65535)", userConfig.Metrics.Port))
		}
	}

	// Validate tracing configuration if enabled
	if userConfig.Tracing.Enabled {
		if userConfig.Tracing.OTEL.Endpoint == "" {
			errors = append(errors, "tracing is enabled but no OTEL endpoint specified")
		}
		if userConfig.Tracing.Fraction < 0 || userConfig.Tracing.Fraction > 1 {
			errors = append(errors, fmt.Sprintf("invalid tracing fraction: %f (must be between 0-1)", userConfig.Tracing.Fraction))
		}
	}

	// Validate telemetry configuration
	if userConfig.Telemetry.Endpoint == "" {
		// Only require telemetry endpoint if telemetry types are specified
		if len(userConfig.Telemetry.Types) > 0 {
			errors = append(errors, "telemetry types specified but no endpoint provided")
		}
	}

	// Validate custom headers configuration if enabled
	if userConfig.CustomHeaders.Enabled {
		if len(userConfig.CustomHeaders.Headers) == 0 {
			errors = append(errors, "custom headers is enabled but no headers specified")
		}
	}

	// Validate quotas configuration if enabled
	if userConfig.Quotas.Access.Enabled {
		if userConfig.Quotas.Access.MinFrequency == "" {
			errors = append(errors, "access quotas enabled but min_frequency not specified")
		}
		if userConfig.Quotas.Access.MaxBulkSize <= 0 {
			errors = append(errors, fmt.Sprintf("invalid access quota max_bulk_size: %d (must be > 0)", userConfig.Quotas.Access.MaxBulkSize))
		}
	}

	if userConfig.Quotas.Execution.Enabled {
		if userConfig.Quotas.Execution.MinFrequency == "" {
			errors = append(errors, "execution quotas enabled but min_frequency not specified")
		}
		if userConfig.Quotas.Execution.MaxBulkSize <= 0 {
			errors = append(errors, fmt.Sprintf("invalid execution quota max_bulk_size: %d (must be > 0)", userConfig.Quotas.Execution.MaxBulkSize))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("validation errors: %s", strings.Join(errors, "; "))
	}

	return nil
}

func (s *ServerConfig) Validate() error {
	if s.Config.Port <= 0 || s.Config.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", s.Config.Port)
	}
	return nil
}
