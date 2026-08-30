package validator

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/drone/envsubst"
	"github.com/everstacklabs/everstack/internal/api/grpc/mferrors"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/spf13/viper"
)

const (
	// RemoteConfigTimeout is the timeout for fetching remote config files
	RemoteConfigTimeout = 30 * time.Second
	// RemoteConfigMaxRetries is the maximum number of retries for fetching remote config
	RemoteConfigMaxRetries = 3
	// RemoteConfigRetryDelay is the initial delay between retries
	RemoteConfigRetryDelay = 2 * time.Second
)

// Config represents the main gateway configuration
type Config struct {
	Server        *ServerConfig        `mapstructure:"server"`
	SecretManager *SecretManagerConfig `mapstructure:"secret_manager"`
	Database      *DatabaseConfig      `mapstructure:"database"`
	Auth          *AuthConfig          `mapstructure:"auth"`
	Cache         *CacheConfig         `mapstructure:"cache"`
	Gateway       *GatewayConfig       `mapstructure:"gateway"`
	Backup        *BackupConfig        `mapstructure:"backup"`
	Alerts        *AlertsConfig        `mapstructure:"alerts"`
	Features      *FeaturesConfig      `mapstructure:"features"`
}

// DefaultConfigs holds all default configurations as raw data
// These will be loaded individually as needed
type DefaultConfigs struct {
	Server     []byte
	Models     []byte
	Providers  []byte
	Guardrails []byte
	Alerts     []byte
	Agents     []byte
	Gateway    []byte
}

// embeddedDefaults stores the go:embed defaults set at startup
// This allows LoadModelsAndProvidersDefaults to return embedded data instead of reading from filesystem
var embeddedDefaults struct {
	models    []byte
	providers []byte
	set       bool
}

// SetEmbeddedModelsAndProviders stores the embedded models and providers bytes for later use.
// This should be called during application initialization with the go:embed data.
func SetEmbeddedModelsAndProviders(models, providers []byte) {
	embeddedDefaults.models = models
	embeddedDefaults.providers = providers
	embeddedDefaults.set = true
	logger.Debugf("Embedded models (%d bytes) and providers (%d bytes) registered", len(models), len(providers))
}

// IsRemoteURL checks if the given path is a remote URL (http:// or https://)
func IsRemoteURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

// fetchRemoteConfig fetches configuration from a remote URL with optional bearer token authentication.
// It supports retry logic with exponential backoff and validates TLS certificates.
// The bearer token is read from the EVS_CONFIG_AUTH_TOKEN environment variable.
func fetchRemoteConfig(configURL string) ([]byte, error) {
	// Validate URL scheme - only allow http/https for security
	parsedURL, err := url.Parse(configURL)
	if err != nil {
		return nil, fmt.Errorf("invalid config URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q: only http and https are allowed", parsedURL.Scheme)
	}

	// Create HTTP client with timeout (TLS verification is enabled by default)
	client := &http.Client{
		Timeout: RemoteConfigTimeout,
	}

	var lastErr error
	retryDelay := RemoteConfigRetryDelay

	for attempt := 0; attempt < RemoteConfigMaxRetries; attempt++ {
		if attempt > 0 {
			// Log retry attempt (redact any auth info from URL)
			safeURL := redactURLAuth(configURL)
			logger.Infof("Retrying remote config fetch (attempt %d/%d): %s", attempt+1, RemoteConfigMaxRetries, safeURL)
			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
		}

		data, err := doFetchRemoteConfig(client, configURL)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}

	safeURL := redactURLAuth(configURL)
	return nil, fmt.Errorf("failed to fetch remote config from %s after %d attempts: %w", safeURL, RemoteConfigMaxRetries, lastErr)
}

// doFetchRemoteConfig performs a single HTTP request to fetch the config
func doFetchRemoteConfig(client *http.Client, configURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, configURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add bearer token authentication if provided
	if token := os.Getenv("EVS_CONFIG_AUTH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Set accept header for YAML content
	req.Header.Set("Accept", "application/x-yaml, application/yaml, text/yaml, text/plain, */*")
	req.Header.Set("User-Agent", "Everstack/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read a bit of the body for error context
		bodyPreview := make([]byte, 256)
		n, _ := resp.Body.Read(bodyPreview)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyPreview[:n]))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("received empty config from remote URL")
	}

	return data, nil
}

// redactURLAuth removes sensitive information from URLs for logging
func redactURLAuth(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// If we can't parse it, just return a placeholder
		return "[invalid URL]"
	}
	// Redact password if present
	if parsed.User != nil {
		parsed.User = url.UserPassword(parsed.User.Username(), "[REDACTED]")
	}
	// Redact common auth query parameters
	query := parsed.Query()
	sensitiveParams := []string{"token", "access_token", "api_key", "apikey", "key", "secret", "password"}
	for _, param := range sensitiveParams {
		if query.Has(param) {
			query.Set(param, "[REDACTED]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// LoadConfig loads the main configuration file from a local path or remote URL.
// It supports:
// - Local file paths (e.g., "/app/config/gateway.yaml")
// - Remote URLs (e.g., "https://example.com/config.yaml")
// - Bearer token authentication via EVS_CONFIG_AUTH_TOKEN environment variable
func LoadConfig(configPath string) (*Config, error) {
	var data []byte
	var err error

	// Check if the config path is a remote URL
	if IsRemoteURL(configPath) {
		safeURL := redactURLAuth(configPath)
		logger.Infof("Loading config from remote URL: %s", safeURL)
		data, err = fetchRemoteConfig(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch remote config: %w", err)
		}
	} else {
		// Read from local file
		data, err = os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	// Expand ${VAR} using process environment
	expanded, err := envsubst.EvalEnv(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to expand environment variables in config: %w", err)
	}

	// Parse expanded YAML with isolated Viper that still honors EVS_* overrides
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("EVS")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	if err := v.ReadConfig(strings.NewReader(expanded)); err != nil {
		return nil, fmt.Errorf("failed to read expanded config: %w", err)
	}
	setCatalogDefaults(v)

	// Bind all registered environment variables from the central registry.
	// This is required because Viper's AutomaticEnv doesn't work with Unmarshal
	// for nested struct fields without explicit BindEnv calls.
	// See cmd/config/gateway/validator/envvars.go for the complete list of supported env vars.
	BindEnvVars(v)

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return &config, nil
}

func setCatalogDefaults(v *viper.Viper) {
	catalog := defaultCatalogSyncConfig()
	v.SetDefault("server.catalog.source", catalog.Source)
	v.SetDefault("server.catalog.remote_url", catalog.RemoteURL)
	v.SetDefault("server.catalog.local_path", catalog.LocalPath)
	v.SetDefault("server.catalog.enable_auto_sync", catalog.EnableAutoSync)
	v.SetDefault("server.catalog.sync_interval", catalog.SyncInterval)
	v.SetDefault("server.catalog.channel", catalog.Channel)
	v.SetDefault("server.catalog.public_key", catalog.PublicKey)
	v.SetDefault("server.catalog.public_keys", catalog.PublicKeys)
	v.SetDefault("server.catalog.require_signature", catalog.RequireSignature)
}

// CatalogSyncConfigFromEnvironment resolves the catalog defaults and the
// registered EVS_CATALOG_* overrides without requiring a gateway YAML file.
// The shared cloud gateway is assembled programmatically and uses this path so
// it receives the same layered catalog configuration as self-hosted gateways.
func CatalogSyncConfigFromEnvironment() (CatalogSyncConfig, error) {
	v := viper.New()
	v.SetEnvPrefix("EVS")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	setCatalogDefaults(v)
	BindEnvVars(v)

	var resolved struct {
		Server struct {
			Catalog CatalogSyncConfig `mapstructure:"catalog"`
		} `mapstructure:"server"`
	}
	if err := v.Unmarshal(&resolved); err != nil {
		return CatalogSyncConfig{}, fmt.Errorf("resolve catalog environment configuration: %w", err)
	}
	return resolved.Server.Catalog, nil
}

// LoadDefaultConfigs loads all default configuration files as raw bytes
func LoadDefaultConfigs() (*DefaultConfigs, error) {
	defaults := &DefaultConfigs{}

	// Load server defaults
	if data, err := loadDefaultFileBytes("defaults/server.yaml"); err != nil {
		return nil, fmt.Errorf("failed to load server defaults: %w", err)
	} else {
		defaults.Server = data
	}

	// Load models and providers from model-catalog (source of truth)
	// This uses the new hierarchical structure with fallback to legacy flat files
	modelsData, providersData, err := LoadModelsAndProvidersDefaults()
	if err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to load models/providers catalog: %w", err))
	}
	defaults.Models = modelsData
	defaults.Providers = providersData

	// Load guardrails defaults
	if data, err := loadDefaultFileBytes("defaults/guardrails.yaml"); err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to load guardrails defaults: %w", err))
	} else {
		defaults.Guardrails = data
	}

	// Load alerts defaults
	if data, err := loadDefaultFileBytes("defaults/alerts.yaml"); err != nil {
		return nil, mferrors.MFToConnectError(fmt.Errorf("failed to load alerts defaults: %w", err))
	} else {
		defaults.Alerts = data
	}

	// Load agents defaults (optional)
	if data, err := loadDefaultFileBytes("defaults/agents.yaml"); err != nil {
		// Agents file is optional, so we don't return an error
		logger.Warnf("Warning: agents.yaml not found, skipping: %v", err)
	} else {
		defaults.Agents = data
	}

	// Load gateway defaults (optional; used for SSE routes)
	if data, err := loadDefaultFileBytes("defaults/gateway.yaml"); err != nil {
		logger.Warnf("Warning: gateway.yaml not found, skipping: %v", err)
	} else {
		defaults.Gateway = data
	}

	return defaults, nil
}

// loadDefaultFileBytes loads a single default configuration file as raw bytes
func loadDefaultFileBytes(filepath string) ([]byte, error) {
	// Check if file exists
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		return nil, fmt.Errorf("file not found: %s", filepath)
	}

	// Read file as bytes
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", filepath, err)
	}

	return data, nil
}

// LoadYAMLIntoStruct loads YAML bytes into a struct (exported for use by other packages)
func LoadYAMLIntoStruct(data []byte, target interface{}) error {
	v := viper.New()
	v.SetConfigType("yaml")

	if err := v.ReadConfig(strings.NewReader(string(data))); err != nil {
		return fmt.Errorf("failed to read YAML: %w", err)
	}

	if err := v.Unmarshal(target); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return nil
}

// loadYAMLIntoStruct is kept for backward compatibility within the package
func loadYAMLIntoStruct(data []byte, target interface{}) error {
	return LoadYAMLIntoStruct(data, target)
}

// LoadDefaultConfigsWithRetry loads default configurations with retry logic
func LoadDefaultConfigsWithRetry(maxRetries int) (*DefaultConfigs, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry with exponential backoff
			backoff := time.Duration(attempt) * time.Second
			time.Sleep(backoff)
		}

		defaults, err := LoadDefaultConfigs()
		if err == nil {
			return defaults, nil
		}

		lastErr = err
		fmt.Printf("Attempt %d failed: %v\n", attempt+1, err)
	}

	return nil, fmt.Errorf("failed to load default configs after %d attempts: %w", maxRetries+1, lastErr)
}

// LoadModelsAndProvidersDefaults loads only models and providers.
// Priority order:
// 1. Embedded defaults (set via SetEmbeddedModelsAndProviders at startup)
// 2. Hierarchical directory structure (model-catalog/providers/)
// 3. Legacy flat files (model-catalog/models.yaml, providers.yaml)
// 4. Empty data (catalog refresh can run asynchronously after startup)
//
// This function NEVER fails - if catalog files are not found locally, it returns
// empty data and logs a warning. The catalog sync service can fetch from the
// configured distribution URL after the service starts.
func LoadModelsAndProvidersDefaults() (models []byte, providers []byte, err error) {
	// First, check if embedded defaults were set (production/Docker mode)
	if embeddedDefaults.set && len(embeddedDefaults.models) > 0 && len(embeddedDefaults.providers) > 0 {
		logger.Debugf("Using embedded models and providers defaults")
		return embeddedDefaults.models, embeddedDefaults.providers, nil
	}

	// Try multiple catalog paths (for different run locations) - development mode
	catalogPaths := []string{
		"model-catalog",
		"../model-catalog",
		"/app/model-catalog", // Docker container path
	}

	// First, try loading from the new hierarchical structure (providers/ directory)
	for _, catalogPath := range catalogPaths {
		providersDir := catalogPath + "/providers"
		if _, statErr := os.Stat(providersDir); statErr == nil {
			modelsData, providersData, loadErr := LoadCatalogFromDirectory(catalogPath)
			if loadErr == nil {
				return modelsData, providersData, nil
			}
			// Log but don't fail - try fallback
			logger.Debugf("Warning: failed to load from hierarchical catalog at %s: %v", catalogPath, loadErr)
		}
	}

	// Fallback to legacy flat file structure for backward compatibility
	modelsPaths := []string{
		"model-catalog/models.yaml",
		"../model-catalog/models.yaml",
		"/app/model-catalog/models.yaml",
	}
	providersPaths := []string{
		"model-catalog/providers.yaml",
		"../model-catalog/providers.yaml",
		"/app/model-catalog/providers.yaml",
	}

	var modelsData []byte
	var modelsErr error
	for _, path := range modelsPaths {
		modelsData, modelsErr = loadDefaultFileBytes(path)
		if modelsErr == nil {
			break
		}
	}

	// If we can't find models locally, return empty data instead of failing
	// The catalog sync service will fetch from remote later
	if modelsErr != nil {
		logger.Warnf("Model catalog not found locally (tried %v). Catalog will be synced from remote.", modelsPaths)
		// Return empty but valid YAML structures
		emptyModels := []byte("models: []\n")
		emptyProviders := []byte("providers: {}\n")
		return emptyModels, emptyProviders, nil
	}

	var providersData []byte
	var providersErr error
	for _, path := range providersPaths {
		providersData, providersErr = loadDefaultFileBytes(path)
		if providersErr == nil {
			break
		}
	}
	if providersErr != nil {
		logger.Warnf("Provider catalog not found locally (tried %v). Catalog will be synced from remote.", providersPaths)
		// Return empty but valid YAML structure
		providersData = []byte("providers: {}\n")
	}

	return modelsData, providersData, nil
}

// ValidateDefaultConfigs validates that all required default files are present
// Note: Models and Providers are optional at startup - they can be synced from remote later
func ValidateDefaultConfigs(defaults *DefaultConfigs) error {
	var errors []string

	// Check for required sections (core config that must be present)
	if len(defaults.Server) == 0 {
		errors = append(errors, "missing server configuration")
	}
	if len(defaults.Guardrails) == 0 {
		errors = append(errors, "missing guardrails configuration")
	}
	if len(defaults.Alerts) == 0 {
		errors = append(errors, "missing alerts configuration")
	}

	// Models and Providers are optional at startup - they can be synced from remote
	// Log a warning if missing, but don't fail
	if len(defaults.Models) == 0 {
		logger.Warnf("Models catalog not loaded at startup. Will sync from remote catalog.")
	}
	if len(defaults.Providers) == 0 {
		logger.Warnf("Providers catalog not loaded at startup. Will sync from remote catalog.")
	}

	if len(errors) > 0 {
		return fmt.Errorf("validation errors: %v", errors)
	}

	return nil
}
