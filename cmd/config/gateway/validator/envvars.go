package validator

import (
	"github.com/spf13/viper"
)

// EnvVar represents a single environment variable binding
type EnvVar struct {
	// ConfigKey is the dot-notation key in the config (e.g., "database.mode")
	ConfigKey string
	// EnvName is the environment variable name (e.g., "EVS_DATABASE_MODE")
	EnvName string
	// Description documents what this env var controls
	Description string
	// Required indicates if this env var is mandatory (for documentation purposes)
	Required bool
	// DefaultValue is the default if not set (for documentation purposes)
	DefaultValue string
}

// EnvVarRegistry contains all supported environment variables for Everstack configuration.
// This serves as both the binding registry and documentation for available env vars.
//
// Environment variables take precedence over config file values.
// All env vars use the EVS_ prefix.
var EnvVarRegistry = []EnvVar{
	// ==========================================================================
	// Secret Manager Configuration
	// ==========================================================================
	{
		ConfigKey:   "secret_manager.type",
		EnvName:     "EVS_SECRET_MANAGER_TYPE",
		Description: "General secret manager type",
	},
	{
		ConfigKey:    "secret_manager.storage_credentials.backend",
		EnvName:      "EVS_STORAGE_CREDENTIAL_BACKEND",
		Description:  "External storage credential backend: inherit, postgres, or vault",
		DefaultValue: "inherit",
	},
	{
		ConfigKey:    "secret_manager.storage_credentials.key_id",
		EnvName:      "EVS_STORAGE_CREDENTIAL_KEY_ID",
		Description:  "Active external storage credential encryption key ID",
		DefaultValue: "v1",
	},
	{
		ConfigKey:   "secret_manager.storage_credentials.master_key",
		EnvName:     "EVS_STORAGE_CREDENTIAL_MASTER_KEY",
		Description: "Dedicated external storage credential master key",
	},
	{
		ConfigKey:   "secret_manager.storage_credentials.previous_keys",
		EnvName:     "EVS_STORAGE_CREDENTIAL_PREVIOUS_KEYS",
		Description: "JSON map of previous storage credential keys retained during rotation",
	},
	{
		ConfigKey:    "secret_manager.storage_credentials.path_prefix",
		EnvName:      "EVS_STORAGE_CREDENTIAL_PATH_PREFIX",
		Description:  "Path prefix used by the external storage credential backend",
		DefaultValue: "everstack/storage-credentials",
	},
	{
		ConfigKey:   "secret_manager.vault.address",
		EnvName:     "EVS_SECRET_MANAGER_VAULT_ADDRESS",
		Description: "HashiCorp Vault address",
	},
	{
		ConfigKey:   "secret_manager.vault.token",
		EnvName:     "EVS_SECRET_MANAGER_VAULT_TOKEN",
		Description: "HashiCorp Vault authentication token",
	},
	{
		ConfigKey:   "secret_manager.vault.namespace",
		EnvName:     "EVS_SECRET_MANAGER_VAULT_NAMESPACE",
		Description: "HashiCorp Vault Enterprise namespace",
	},
	{
		ConfigKey:    "secret_manager.vault.mount_path",
		EnvName:      "EVS_SECRET_MANAGER_VAULT_MOUNT_PATH",
		Description:  "HashiCorp Vault KV v2 mount path",
		DefaultValue: "secret",
	},

	// ==========================================================================
	// Database Configuration
	// ==========================================================================
	{
		ConfigKey:    "database.mode",
		EnvName:      "EVS_DATABASE_MODE",
		Description:  "Database mode: 'single' (Postgres only) or 'hybrid' (Postgres + ClickHouse)",
		DefaultValue: "single",
	},
	{
		ConfigKey:    "database.type",
		EnvName:      "EVS_DATABASE_TYPE",
		Description:  "Primary database type: 'postgres', 'clickhouse', or 'memory'",
		DefaultValue: "postgres",
	},
	{
		ConfigKey:   "database.postgres.dsn",
		EnvName:     "EVS_POSTGRES_DSN",
		Description: "PostgreSQL connection DSN (e.g., postgres://user:pass@host:5432/dbname?sslmode=disable)",
	},
	{
		ConfigKey:   "database.clickhouse.dsn",
		EnvName:     "EVS_CLICKHOUSE_DSN",
		Description: "ClickHouse connection DSN (e.g., clickhouse://user:pass@host:9000/dbname)",
	},

	// ==========================================================================
	// Server Configuration
	// ==========================================================================
	{
		ConfigKey:    "server.config.port",
		EnvName:      "EVS_SERVER_PORT",
		Description:  "HTTP server port",
		DefaultValue: "8089",
	},
	{
		ConfigKey:    "server.config.external_domain",
		EnvName:      "EVS_SERVER_EXTERNAL_DOMAIN",
		Description:  "External domain for the server (e.g., api.example.com)",
		DefaultValue: "",
	},
	{
		ConfigKey:    "server.config.external_secure",
		EnvName:      "EVS_SERVER_EXTERNAL_SECURE",
		Description:  "Whether external connections use HTTPS",
		DefaultValue: "false",
	},

	// ==========================================================================
	// Cache Configuration
	// ==========================================================================
	{
		ConfigKey:    "cache.enabled",
		EnvName:      "EVS_CACHE_ENABLED",
		Description:  "Enable caching",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "cache.type",
		EnvName:      "EVS_CACHE_TYPE",
		Description:  "Cache backend type: 'memory' or 'redis'",
		DefaultValue: "memory",
	},
	{
		ConfigKey:   "cache.redis.address",
		EnvName:     "EVS_CACHE_REDIS_ADDRESS",
		Description: "Redis server address (e.g., redis:6379)",
	},
	{
		ConfigKey:   "cache.redis.password",
		EnvName:     "EVS_CACHE_REDIS_PASSWORD",
		Description: "Redis password",
	},
	{
		ConfigKey:    "cache.redis.db",
		EnvName:      "EVS_CACHE_REDIS_DB",
		Description:  "Redis database number",
		DefaultValue: "0",
	},

	// ==========================================================================
	// Telemetry Configuration
	// ==========================================================================
	{
		ConfigKey:    "server.telemetry.otel.enabled",
		EnvName:      "EVS_TELEMETRY_ENABLED",
		Description:  "Enable OpenTelemetry telemetry",
		DefaultValue: "false",
	},
	{
		ConfigKey:   "server.telemetry.otel.collector_url",
		EnvName:     "EVS_TELEMETRY_OTLP_ENDPOINT",
		Description: "OTLP collector endpoint (e.g., http://otel-collector:4317)",
	},
	{
		ConfigKey:    "server.telemetry.otel.service_name",
		EnvName:      "EVS_TELEMETRY_SERVICE_NAME",
		Description:  "Service name for telemetry",
		DefaultValue: "everstack-gateway",
	},

	// ==========================================================================
	// Feature Flags
	// ==========================================================================
	{
		ConfigKey:    "features.gateway.enable_streaming",
		EnvName:      "EVS_FEATURES_GATEWAY_ENABLE_STREAMING",
		Description:  "Enable streaming responses",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "features.gateway.enable_response_caching",
		EnvName:      "EVS_FEATURES_GATEWAY_ENABLE_RESPONSE_CACHING",
		Description:  "Enable response caching",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "features.gateway.enable_request_logging",
		EnvName:      "EVS_FEATURES_GATEWAY_ENABLE_REQUEST_LOGGING",
		Description:  "Enable request logging",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "features.gateway.enable_cost_tracking",
		EnvName:      "EVS_FEATURES_GATEWAY_ENABLE_COST_TRACKING",
		Description:  "Enable cost tracking for API calls",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "features.gateway.enable_embeddings",
		EnvName:      "EVS_FEATURES_GATEWAY_ENABLE_EMBEDDINGS",
		Description:  "Enable embeddings endpoint",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "features.gateway.enable_function_calling",
		EnvName:      "EVS_FEATURES_GATEWAY_ENABLE_FUNCTION_CALLING",
		Description:  "Enable function calling support",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "features.fastpath.enabled",
		EnvName:      "EVS_FEATURES_FASTPATH_ENABLED",
		Description:  "Enable fast-path optimizations",
		DefaultValue: "true",
	},

	// ==========================================================================
	// TLS Configuration
	// ==========================================================================
	{
		ConfigKey:    "server.tls.enabled",
		EnvName:      "EVS_TLS_ENABLED",
		Description:  "Enable TLS/HTTPS",
		DefaultValue: "false",
	},
	{
		ConfigKey:   "server.tls.cert_path",
		EnvName:     "EVS_TLS_CERT_PATH",
		Description: "Path to TLS certificate file",
	},
	{
		ConfigKey:   "server.tls.key_path",
		EnvName:     "EVS_TLS_KEY_PATH",
		Description: "Path to TLS private key file",
	},

	// ==========================================================================
	// Catalog Configuration
	// ==========================================================================
	{
		ConfigKey:    "server.catalog.source",
		EnvName:      "EVS_CATALOG_SOURCE",
		Description:  "Catalog source: 'remote' or 'local'",
		DefaultValue: "remote",
	},
	{
		ConfigKey:   "server.catalog.remote_url",
		EnvName:     "EVS_CATALOG_REMOTE_URL",
		Description: "URL for remote model catalog",
	},
	{
		ConfigKey:   "server.catalog.local_path",
		EnvName:     "EVS_CATALOG_LOCAL_PATH",
		Description: "Path to local model catalog directory",
	},
	{
		ConfigKey:    "server.catalog.enable_auto_sync",
		EnvName:      "EVS_CATALOG_ENABLE_AUTO_SYNC",
		Description:  "Enable automatic catalog syncing",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "server.catalog.sync_interval",
		EnvName:      "EVS_CATALOG_SYNC_INTERVAL",
		Description:  "Interval between model catalog update checks",
		DefaultValue: "5m",
	},
	{
		ConfigKey:    "server.catalog.channel",
		EnvName:      "EVS_CATALOG_CHANNEL",
		Description:  "Signed model catalog release channel",
		DefaultValue: "stable",
	},
	{
		ConfigKey:   "server.catalog.public_key",
		EnvName:     "EVS_CATALOG_PUBLIC_KEY",
		Description: "Base64 Ed25519 model catalog verification key",
	},
	{
		ConfigKey:   "server.catalog.public_keys",
		EnvName:     "EVS_CATALOG_PUBLIC_KEYS",
		Description: "Comma-separated model catalog verification keys for rotation",
	},
	{
		ConfigKey:    "server.catalog.require_signature",
		EnvName:      "EVS_CATALOG_REQUIRE_SIGNATURE",
		Description:  "Require signed model catalog channel documents",
		DefaultValue: "true",
	},

	// ==========================================================================
	// CORS Configuration
	// ==========================================================================
	{
		ConfigKey:    "server.cors.enabled",
		EnvName:      "EVS_CORS_ENABLED",
		Description:  "Enable CORS",
		DefaultValue: "true",
	},

	// ==========================================================================
	// Metrics Configuration
	// ==========================================================================
	{
		ConfigKey:    "server.metrics.enabled",
		EnvName:      "EVS_METRICS_ENABLED",
		Description:  "Enable Prometheus metrics",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "server.metrics.port",
		EnvName:      "EVS_METRICS_PORT",
		Description:  "Metrics server port",
		DefaultValue: "9090",
	},

	// ==========================================================================
	// Tracing Configuration
	// ==========================================================================
	{
		ConfigKey:    "server.tracing.enabled",
		EnvName:      "EVS_TRACING_ENABLED",
		Description:  "Enable distributed tracing",
		DefaultValue: "false",
	},
	{
		ConfigKey:   "server.tracing.otel.endpoint",
		EnvName:     "EVS_TRACING_OTEL_ENDPOINT",
		Description: "OpenTelemetry tracing endpoint",
	},
	{
		ConfigKey:    "server.tracing.fraction",
		EnvName:      "EVS_TRACING_FRACTION",
		Description:  "Sampling fraction for traces (0.0 to 1.0)",
		DefaultValue: "0.1",
	},

	// ==========================================================================
	// Remote Config
	// ==========================================================================
	{
		ConfigKey:   "config_url",
		EnvName:     "EVS_CONFIG_URL",
		Description: "URL to fetch configuration from (supports http/https)",
	},
	{
		ConfigKey:   "config_auth_token",
		EnvName:     "EVS_CONFIG_AUTH_TOKEN",
		Description: "Bearer token for authenticated remote config fetching",
	},

	// ==========================================================================
	// Cloud Services (Everstack Platform)
	// ==========================================================================
	{
		ConfigKey:    "services.license.url",
		EnvName:      "EVS_SERVICES_LICENSE_URL",
		Description:  "License service URL (default: https://license.everstack.ai)",
		DefaultValue: "https://license.everstack.ai",
	},
	{
		ConfigKey:    "services.auth.url",
		EnvName:      "EVS_SERVICES_AUTH_URL",
		Description:  "Auth service URL (future)",
		DefaultValue: "https://auth.everstack.ai",
	},
	{
		ConfigKey:    "services.functions.url",
		EnvName:      "EVS_SERVICES_FUNCTIONS_URL",
		Description:  "Functions service URL (future)",
		DefaultValue: "https://functions.everstack.ai",
	},
	{
		ConfigKey:    "services.prompts.url",
		EnvName:      "EVS_SERVICES_PROMPTS_URL",
		Description:  "Prompts service URL (future)",
		DefaultValue: "https://prompts.everstack.ai",
	},

	// ==========================================================================
	// License / Activation
	// ==========================================================================
	{
		ConfigKey:   "activation.platform_url",
		EnvName:     "EVS_SERVICES_ACTIVATION_PLATFORM_URL",
		Description: "Legacy: License service URL for activation (use EVS_SERVICES_LICENSE_URL instead)",
	},
	{
		ConfigKey:   "license.activation_token",
		EnvName:     "EVS_ACTIVATION_TOKEN",
		Description: "License activation token for gateway",
	},

	// ==========================================================================
	// Authentication Configuration
	// ==========================================================================
	{
		ConfigKey:    "auth.mode",
		EnvName:      "EVS_AUTH_MODE",
		Description:  "Authentication mode: 'none', 'builtin', or 'oidc'",
		DefaultValue: "none",
	},
	{
		ConfigKey:   "auth.builtin.session_secret",
		EnvName:     "EVS_AUTH_BUILTIN_SESSION_SECRET",
		Description: "Secret for signing session cookies (auto-generated if empty, but won't persist across restarts)",
	},
	{
		ConfigKey:    "auth.builtin.session_max_age",
		EnvName:      "EVS_AUTH_BUILTIN_SESSION_MAX_AGE",
		Description:  "Session duration in seconds",
		DefaultValue: "604800",
	},
	{
		ConfigKey:    "auth.builtin.session_secure",
		EnvName:      "EVS_AUTH_BUILTIN_SESSION_SECURE",
		Description:  "Whether session cookies require HTTPS",
		DefaultValue: "true",
	},
	{
		ConfigKey:    "auth.builtin.session_same_site",
		EnvName:      "EVS_AUTH_BUILTIN_SESSION_SAME_SITE",
		Description:  "Cookie SameSite attribute: 'lax', 'strict', or 'none'",
		DefaultValue: "lax",
	},
	{
		ConfigKey:   "auth.oidc.issuer_url",
		EnvName:     "EVS_AUTH_OIDC_ISSUER_URL",
		Description: "OIDC provider issuer URL (e.g., https://auth.example.com/realms/everstack)",
	},
	{
		ConfigKey:   "auth.oidc.client_id",
		EnvName:     "EVS_AUTH_OIDC_CLIENT_ID",
		Description: "OIDC client ID",
	},
	{
		ConfigKey:   "auth.oidc.client_secret",
		EnvName:     "EVS_AUTH_OIDC_CLIENT_SECRET",
		Description: "OIDC client secret",
	},
	{
		ConfigKey:   "auth.oidc.redirect_uri",
		EnvName:     "EVS_AUTH_OIDC_REDIRECT_URI",
		Description: "OIDC redirect URI (e.g., http://localhost:8089/auth/callback)",
	},

	// ==========================================================================
	// Environment
	// ==========================================================================
	{
		ConfigKey:    "env",
		EnvName:      "EVS_ENV",
		Description:  "Environment: 'development', 'staging', 'production'",
		DefaultValue: "production",
	},
}

// BindEnvVars binds all registered environment variables to a Viper instance.
// This must be called before Unmarshal to ensure env vars override config values.
func BindEnvVars(v *viper.Viper) {
	for _, ev := range EnvVarRegistry {
		_ = v.BindEnv(ev.ConfigKey, ev.EnvName)
	}
}

// GetEnvVarByConfigKey looks up an env var definition by its config key
func GetEnvVarByConfigKey(configKey string) *EnvVar {
	for i := range EnvVarRegistry {
		if EnvVarRegistry[i].ConfigKey == configKey {
			return &EnvVarRegistry[i]
		}
	}
	return nil
}

// GetEnvVarByName looks up an env var definition by its environment variable name
func GetEnvVarByName(envName string) *EnvVar {
	for i := range EnvVarRegistry {
		if EnvVarRegistry[i].EnvName == envName {
			return &EnvVarRegistry[i]
		}
	}
	return nil
}

// ListEnvVars returns all registered environment variables (useful for documentation)
func ListEnvVars() []EnvVar {
	result := make([]EnvVar, len(EnvVarRegistry))
	copy(result, EnvVarRegistry)
	return result
}
