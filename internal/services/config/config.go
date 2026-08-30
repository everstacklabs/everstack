package config

import (
	"bytes"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Global embedded defaults (set by cmd/everstack.go)
var embeddedServicesDefaults []byte

// SetEmbeddedDefaults sets the embedded services configuration defaults.
// This should be called early in the application startup (from cmd/everstack.go).
func SetEmbeddedDefaults(defaults []byte) {
	embeddedServicesDefaults = defaults
}

// CloudService represents a cloud service configuration
type CloudService struct {
	URL     string `mapstructure:"url" yaml:"url"`
	Enabled bool   `mapstructure:"enabled" yaml:"enabled"`
}

// CloudServices contains all Everstack cloud service configurations
type CloudServices struct {
	License   CloudService `mapstructure:"license" yaml:"license"`
	Auth      CloudService `mapstructure:"auth" yaml:"auth"`
	Functions CloudService `mapstructure:"functions" yaml:"functions"`
	Prompts   CloudService `mapstructure:"prompts" yaml:"prompts"`
}

type SecurityPolicy struct {
	BypassServices []string `mapstructure:"bypass_services" yaml:"bypass_services"`
	BypassPrefixes []string `mapstructure:"bypass_prefixes" yaml:"bypass_prefixes"`
}

type SecurityLicenseEnforcement struct {
	Enabled  bool          `mapstructure:"enabled" yaml:"enabled"`
	DryRun   bool          `mapstructure:"dry_run" yaml:"dry_run"`
	CacheTTL time.Duration `mapstructure:"cache_ttl" yaml:"cache_ttl"`
}

type SecurityConfig struct {
	Policy             SecurityPolicy             `mapstructure:"policy" yaml:"policy"`
	LicenseEnforcement SecurityLicenseEnforcement `mapstructure:"license_enforcement" yaml:"license_enforcement"`
	M2M                M2MConfig                  `mapstructure:"m2m" yaml:"m2m"`
}

// M2MConfig holds configuration for M2M (machine-to-machine) authentication.
// Supports two modes:
//   - "simple": Self-contained JWT (no external dependencies)
//   - "oidc": Generic OIDC provider (Auth0, Keycloak, Zitadel, Okta, etc.)
type M2MConfig struct {
	// Enabled controls whether M2M validation is active
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`

	// Provider specifies the M2M provider type: "simple" or "oidc"
	// Default: "simple" for backward compatibility
	Provider string `mapstructure:"provider" yaml:"provider"`

	// Simple contains configuration for the simple JWT provider
	Simple M2MSimpleConfig `mapstructure:"simple" yaml:"simple"`

	// OIDC contains configuration for generic OIDC providers
	OIDC M2MOIDCConfig `mapstructure:"oidc" yaml:"oidc"`

	// Clients contains basic client config
	// For simple provider: only ClientID is needed (secrets are derived from signing key)
	Clients map[string]M2MClientCredentials `mapstructure:"clients" yaml:"clients"`

	// OIDCClients contains OIDC-specific credentials from your identity provider
	// Required when provider: "oidc"
	OIDCClients map[string]M2MClientCredentials `mapstructure:"oidc_clients" yaml:"oidc_clients"`

	// PublicEndpoints are procedures that bypass M2M authentication entirely
	PublicEndpoints []string `mapstructure:"public_endpoints" yaml:"public_endpoints"`

	// SessionAuth configures session-based authentication for cloud portal endpoints
	// These endpoints accept session cookies from the cloud dashboard as an alternative to M2M tokens
	SessionAuth SessionAuthConfig `mapstructure:"session_auth" yaml:"session_auth"`

	// ScopePolicy configures automatic scope derivation from endpoint names
	ScopePolicy ScopePolicyConfig `mapstructure:"scope_policy" yaml:"scope_policy"`

	// EndpointScopes defines scope requirements for specific endpoints (legacy, use ScopePolicy instead)
	// Using list format to avoid Viper's dot-key interpretation
	EndpointScopes []EndpointScopeRule `mapstructure:"endpoint_scopes" yaml:"endpoint_scopes"`

	// Policy defines which client types can access which procedures
	Policy M2MPolicyConfig `mapstructure:"policy" yaml:"policy"`

	// ===== Legacy fields (for backward compatibility) =====

	// TimestampWindowSecs is the maximum age of a request signature (default: 300 = 5 minutes)
	// DEPRECATED: Use Simple.TokenTTL instead
	TimestampWindowSecs int `mapstructure:"timestamp_window_seconds" yaml:"timestamp_window_seconds"`

	// NonceTTLSecs is how long nonces are stored for replay prevention (default: 600 = 10 minutes)
	// DEPRECATED: No longer used with JWT-based auth
	NonceTTLSecs int `mapstructure:"nonce_ttl_seconds" yaml:"nonce_ttl_seconds"`

	// NonceCleanupIntervalSecs is how often to clean up expired nonces (default: 300 = 5 minutes)
	// DEPRECATED: No longer used with JWT-based auth
	NonceCleanupIntervalSecs int `mapstructure:"nonce_cleanup_interval_seconds" yaml:"nonce_cleanup_interval_seconds"`

	// Services contains credentials for pre-registered services (portal, internal, gateway)
	// DEPRECATED: Use Clients instead
	Services map[string]M2MServiceCredential `mapstructure:"services" yaml:"services"`

	// Bootstrap contains client-side credentials for the gateway to call the license service
	// DEPRECATED: Use Clients instead
	Bootstrap M2MBootstrapConfig `mapstructure:"bootstrap" yaml:"bootstrap"`
}

// SessionAuthConfig configures session-based authentication for cloud portal endpoints.
// This allows authenticated users from the cloud dashboard to access specific endpoints
// using their session cookie instead of M2M tokens.
type SessionAuthConfig struct {
	// Enabled controls whether session auth is active
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`

	// CookieName is the name of the session cookie (default: "everstack_session")
	CookieName string `mapstructure:"cookie_name" yaml:"cookie_name"`

	// Endpoints are procedures that accept session authentication
	// These can be accessed with either a valid session cookie OR M2M token
	Endpoints []string `mapstructure:"endpoints" yaml:"endpoints"`

	// DatabaseDSN is the connection string for the auth database
	// If empty, uses the same database as the license service
	DatabaseDSN string `mapstructure:"database_dsn" yaml:"database_dsn"`
}

// M2MSimpleConfig configures the simple JWT provider (self-contained, no external deps).
type M2MSimpleConfig struct {
	// SigningKey is the HMAC-SHA256 signing key (must be 32+ bytes, base64 encoded)
	SigningKey string `mapstructure:"signing_key" yaml:"signing_key"`

	// Issuer is the token issuer claim (default: "everstack")
	Issuer string `mapstructure:"issuer" yaml:"issuer"`

	// Audience is the expected audience claim (default: "everstack-services")
	Audience string `mapstructure:"audience" yaml:"audience"`

	// TokenTTL is how long tokens are valid (default: "5m")
	TokenTTL string `mapstructure:"token_ttl" yaml:"token_ttl"`
}

// M2MOIDCConfig configures a generic OIDC provider (Auth0, Keycloak, Zitadel, Okta, etc.).
type M2MOIDCConfig struct {
	// IssuerURL is the OIDC issuer URL (used for discovery)
	// e.g., "https://your-tenant.auth0.com" or "https://keycloak.example.com/realms/myrealm"
	IssuerURL string `mapstructure:"issuer_url" yaml:"issuer_url"`

	// TokenURL is the OAuth2 token endpoint (optional, discovered from issuer if not set)
	TokenURL string `mapstructure:"token_url" yaml:"token_url"`

	// JWKSURL is the JWKS endpoint for token validation (optional, discovered from issuer if not set)
	JWKSURL string `mapstructure:"jwks_url" yaml:"jwks_url"`

	// Audience is the expected audience claim for validation
	Audience string `mapstructure:"audience" yaml:"audience"`

	// Scopes are additional scopes to request (client_credentials is implicit)
	Scopes []string `mapstructure:"scopes" yaml:"scopes"`

	// SkipIssuerCheck skips issuer validation (not recommended for production)
	SkipIssuerCheck bool `mapstructure:"skip_issuer_check" yaml:"skip_issuer_check"`
}

// M2MClientCredentials holds OAuth2 client credentials for a service.
type M2MClientCredentials struct {
	// ClientID is the OAuth2 client identifier
	ClientID string `mapstructure:"client_id" yaml:"client_id"`

	// ClientSecret is the OAuth2 client secret
	ClientSecret string `mapstructure:"client_secret" yaml:"client_secret"`

	// Scopes are the scopes to request (optional, overrides OIDC.Scopes)
	Scopes []string `mapstructure:"scopes" yaml:"scopes"`
}

// M2MServiceCredential holds credentials for a pre-registered service
type M2MServiceCredential struct {
	// TokenHash is the SHA256 hash of the service token (for secure storage)
	TokenHash string `mapstructure:"token_hash" yaml:"token_hash"`

	// SigningKey is the HMAC signing key for this service (can reference env var like ${EVS_M2M_PORTAL_SIGNING_KEY})
	SigningKey string `mapstructure:"signing_key" yaml:"signing_key"`
}

// M2MPolicyConfig defines access control for M2M authentication
type M2MPolicyConfig struct {
	// AllowAllAuthenticated: if true, any authenticated client can access any endpoint
	// This is the recommended simple approach - M2M handles authentication,
	// and the application layer handles authorization.
	AllowAllAuthenticated bool `mapstructure:"allow_all_authenticated" yaml:"allow_all_authenticated"`

	// AllowedClients maps procedure names/prefixes to allowed client types (optional)
	// Only used if AllowAllAuthenticated is false
	// e.g., "LicenseService": ["gateway", "portal", "internal"]
	AllowedClients map[string][]string `mapstructure:"allowed_clients" yaml:"allowed_clients"`
}

// EndpointScopeRule defines scope requirements for a specific endpoint.
// Uses list format to avoid Viper's dot-key interpretation issues.
type EndpointScopeRule struct {
	// Endpoint is the procedure name suffix (e.g., "InstanceService/ReportUsage")
	// Matches any procedure ending with this value
	Endpoint string `mapstructure:"endpoint" yaml:"endpoint"`

	// Scopes are the required scopes for this endpoint (any one is sufficient)
	Scopes []string `mapstructure:"scopes" yaml:"scopes"`
}

// ScopePolicyConfig configures automatic scope derivation from endpoint names.
// This eliminates the need to list every endpoint - scopes are derived automatically.
type ScopePolicyConfig struct {
	// AutoDerive enables automatic scope derivation from endpoint names
	// When true, scopes are derived as: {service}:{action}
	// e.g., InstanceService/ReportUsage -> instance:write
	AutoDerive bool `mapstructure:"auto_derive" yaml:"auto_derive"`

	// ActionPatterns define how method prefixes map to scope actions
	// e.g., prefix "Get" -> action "read", prefix "Create" -> action "write"
	ActionPatterns []ActionPattern `mapstructure:"action_patterns" yaml:"action_patterns"`

	// Overrides for endpoints that don't fit the pattern
	// Only list exceptions, not every endpoint
	Overrides []EndpointScopeRule `mapstructure:"overrides" yaml:"overrides"`
}

// ActionPattern maps a method name prefix to a scope action.
type ActionPattern struct {
	// Prefix is the method name prefix to match (e.g., "Get", "Create", "Delete")
	Prefix string `mapstructure:"prefix" yaml:"prefix"`

	// Action is the scope action suffix (e.g., "read", "write", "admin")
	Action string `mapstructure:"action" yaml:"action"`
}

// M2MBootstrapConfig holds the gateway's bootstrap M2M credentials
// These are used for pre-activation calls (GetPlans, ActivateInstance) before
// the gateway has its own instance-specific signing key.
type M2MBootstrapConfig struct {
	// ServiceID is the service identifier (e.g., "gateway")
	ServiceID string `mapstructure:"service_id" yaml:"service_id"`

	// Token is the service token (plain text - will be hashed when sending)
	Token string `mapstructure:"token" yaml:"token"`

	// SigningKey is the HMAC signing key (base64 encoded)
	SigningKey string `mapstructure:"signing_key" yaml:"signing_key"`
}

// Services contains service-specific configurations (for standalone license service)
type Services struct {
	Activation ActivationConfig `mapstructure:"activation" yaml:"activation"`
	License    LicenseConfig    `mapstructure:"license" yaml:"license"`
	Security   SecurityConfig   `mapstructure:"security" yaml:"security"`
	Gateway    GatewayConfig    `mapstructure:"gateway" yaml:"gateway"`
	Auth       AuthConfig       `mapstructure:"auth" yaml:"auth"`
	Billing    BillingConfig    `mapstructure:"billing" yaml:"billing"`
}

// ServicesConfig is the root configuration structure
// It supports both:
// - New unified format (config/default.yaml): services.license.url, security, activation at root
// - Legacy format (services/config/config.yaml): services.license (full config), services.activation, etc.
type ServicesConfig struct {
	// New unified format - cloud service URLs
	CloudServices CloudServices `mapstructure:"-" yaml:"-"` // Populated manually

	// Root-level configs (new format)
	Security   SecurityConfig   `mapstructure:"security" yaml:"security"`
	Activation ActivationConfig `mapstructure:"activation" yaml:"activation"`

	// Legacy nested format (services.* for backward compatibility with license service)
	Services Services `mapstructure:"services" yaml:"services"`
}

// Load loads services configuration with the following priority (highest to lowest):
// 1. Environment variables (EVS_SERVICES_* prefix)
// 2. config/local.yaml (if exists, for dev overrides)
// 3. External config file (if specified)
// 4. Embedded defaults (config/default.yaml, baked into binary)
//
// Example: EVS_SERVICES_LICENSE_URL=http://localhost:8091 for local dev
//
// NOTE: This function also merges the loaded config into the global Viper instance
// so that policy.FromGlobal() can access it for API key bypass rules.
func Load(path string) (*ServicesConfig, error) {
	vp := viper.New()
	vp.SetConfigType("yaml")

	// 1. First, load embedded defaults if available (baked into binary)
	if len(embeddedServicesDefaults) > 0 {
		if err := vp.ReadConfig(bytes.NewReader(embeddedServicesDefaults)); err != nil {
			return nil, err
		}
	}

	// 2. Merge local.yaml if exists (for dev overrides, gitignored)
	localConfigPaths := []string{
		"cmd/config/local.yaml", // When running from project root
		"config/local.yaml",     // Alternative location
	}
	for _, localPath := range localConfigPaths {
		if _, err := os.Stat(localPath); err == nil {
			vp.SetConfigFile(localPath)
			_ = vp.MergeInConfig()
			break
		}
	}

	// 3. Merge user config file (if specified or found)
	if strings.TrimSpace(path) == "" {
		path = defaultPath()
	}
	if path != "" {
		vp.SetConfigFile(path)
		// Don't fail if file doesn't exist - embedded defaults are sufficient
		_ = vp.MergeInConfig()
	}

	// 4. Enable environment variable overrides (highest priority)
	vp.SetEnvPrefix("EVS")
	vp.AutomaticEnv()
	vp.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Explicitly bind environment variables for nested structs
	// Viper's AutomaticEnv doesn't automatically bind nested fields

	// Auth session cookie domain — used to share the session across
	// subdomains (e.g. .everstack.ai so the cookie works for both
	// app.everstack.ai and *.tenants.everstack.ai).
	_ = vp.BindEnv("services.auth.session.domain", "EVS_SESSION_COOKIE_DOMAIN")
	_ = vp.BindEnv("services.auth.external_url", "EVS_SERVICES_AUTH_EXTERNAL_URL")

	// Cloud service URLs (new unified format)
	_ = vp.BindEnv("services.license.url", "EVS_SERVICES_LICENSE_URL")
	_ = vp.BindEnv("services.auth.url", "EVS_SERVICES_AUTH_URL")
	_ = vp.BindEnv("services.functions.url", "EVS_SERVICES_FUNCTIONS_URL")
	_ = vp.BindEnv("services.prompts.url", "EVS_SERVICES_PROMPTS_URL")

	// Legacy activation config (backward compatibility)
	_ = vp.BindEnv("activation.platform_url", "EVS_SERVICES_ACTIVATION_PLATFORM_URL")
	_ = vp.BindEnv("activation.instance_salt", "EVS_SERVICES_ACTIVATION_INSTANCE_SALT")
	// Also bind to services.activation path for license service config
	_ = vp.BindEnv("services.activation.instance_salt", "EVS_SERVICES_ACTIVATION_INSTANCE_SALT")

	// Also bind to new path for license URL -> activation.platform_url
	// This allows EVS_SERVICES_LICENSE_URL to set activation.platform_url
	if licenseURL := vp.GetString("services.license.url"); licenseURL != "" {
		vp.Set("activation.platform_url", licenseURL)
	}

	// Security settings
	_ = vp.BindEnv("security.license_enforcement.enabled", "EVS_SECURITY_LICENSE_ENFORCEMENT_ENABLED")
	_ = vp.BindEnv("security.license_enforcement.dry_run", "EVS_SECURITY_LICENSE_ENFORCEMENT_DRY_RUN")

	// M2M authentication settings
	_ = vp.BindEnv("security.m2m.enabled", "EVS_SECURITY_M2M_ENABLED")
	_ = vp.BindEnv("security.m2m.provider", "EVS_M2M_PROVIDER")

	// Simple JWT provider settings
	_ = vp.BindEnv("security.m2m.simple.signing_key", "EVS_M2M_SIGNING_KEY")
	_ = vp.BindEnv("security.m2m.simple.issuer", "EVS_M2M_ISSUER")
	_ = vp.BindEnv("security.m2m.simple.audience", "EVS_M2M_AUDIENCE")
	_ = vp.BindEnv("security.m2m.simple.token_ttl", "EVS_M2M_TOKEN_TTL")

	// OIDC provider settings
	_ = vp.BindEnv("security.m2m.oidc.issuer_url", "EVS_M2M_OIDC_ISSUER_URL")
	_ = vp.BindEnv("security.m2m.oidc.token_url", "EVS_M2M_OIDC_TOKEN_URL")
	_ = vp.BindEnv("security.m2m.oidc.jwks_url", "EVS_M2M_OIDC_JWKS_URL")
	_ = vp.BindEnv("security.m2m.oidc.audience", "EVS_M2M_OIDC_AUDIENCE")

	// Client credentials (new format)
	_ = vp.BindEnv("security.m2m.clients.gateway.client_id", "EVS_M2M_GATEWAY_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.clients.gateway.client_secret", "EVS_M2M_GATEWAY_CLIENT_SECRET")
	_ = vp.BindEnv("security.m2m.clients.portal.client_id", "EVS_M2M_PORTAL_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.clients.portal.client_secret", "EVS_M2M_PORTAL_CLIENT_SECRET")
	_ = vp.BindEnv("security.m2m.clients.billing.client_id", "EVS_M2M_BILLING_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.clients.billing.client_secret", "EVS_M2M_BILLING_CLIENT_SECRET")
	_ = vp.BindEnv("security.m2m.clients.license.client_id", "EVS_M2M_LICENSE_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.clients.license.client_secret", "EVS_M2M_LICENSE_CLIENT_SECRET")

	// OIDC client credentials are separate from the simple-provider client
	// declarations. Bind them explicitly so `${...}` placeholders from the
	// checked-in config never reach an identity provider as literal values.
	_ = vp.BindEnv("security.m2m.oidc_clients.gateway.client_id", "EVS_M2M_OIDC_GATEWAY_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.oidc_clients.gateway.client_secret", "EVS_M2M_OIDC_GATEWAY_CLIENT_SECRET")
	_ = vp.BindEnv("security.m2m.oidc_clients.portal.client_id", "EVS_M2M_OIDC_PORTAL_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.oidc_clients.portal.client_secret", "EVS_M2M_OIDC_PORTAL_CLIENT_SECRET")
	_ = vp.BindEnv("security.m2m.oidc_clients.billing.client_id", "EVS_M2M_OIDC_BILLING_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.oidc_clients.billing.client_secret", "EVS_M2M_OIDC_BILLING_CLIENT_SECRET")
	_ = vp.BindEnv("security.m2m.oidc_clients.license.client_id", "EVS_M2M_OIDC_LICENSE_CLIENT_ID")
	_ = vp.BindEnv("security.m2m.oidc_clients.license.client_secret", "EVS_M2M_OIDC_LICENSE_CLIENT_SECRET")

	// Legacy M2M settings (for backward compatibility)
	_ = vp.BindEnv("security.m2m.timestamp_window_seconds", "EVS_SECURITY_M2M_TIMESTAMP_WINDOW_SECONDS")
	_ = vp.BindEnv("security.m2m.nonce_ttl_seconds", "EVS_SECURITY_M2M_NONCE_TTL_SECONDS")
	// Legacy service credentials (server-side validation)
	_ = vp.BindEnv("security.m2m.services.gateway.token_hash", "EVS_M2M_GATEWAY_TOKEN_HASH")
	_ = vp.BindEnv("security.m2m.services.gateway.signing_key", "EVS_M2M_GATEWAY_SIGNING_KEY")
	_ = vp.BindEnv("security.m2m.services.portal.token_hash", "EVS_M2M_PORTAL_TOKEN_HASH")
	_ = vp.BindEnv("security.m2m.services.portal.signing_key", "EVS_M2M_PORTAL_SIGNING_KEY")
	_ = vp.BindEnv("security.m2m.services.internal.token_hash", "EVS_M2M_INTERNAL_TOKEN_HASH")
	_ = vp.BindEnv("security.m2m.services.internal.signing_key", "EVS_M2M_INTERNAL_SIGNING_KEY")
	// Legacy bootstrap credentials (client-side, for gateway pre-activation calls)
	_ = vp.BindEnv("security.m2m.bootstrap.service_id", "EVS_M2M_BOOTSTRAP_SERVICE_ID")
	_ = vp.BindEnv("security.m2m.bootstrap.token", "EVS_M2M_GATEWAY_TOKEN")
	_ = vp.BindEnv("security.m2m.bootstrap.signing_key", "EVS_M2M_GATEWAY_SIGNING_KEY")

	// License service config (for standalone license service deployments)
	_ = vp.BindEnv("services.license.database.dsn", "EVS_SERVICES_LICENSE_DATABASE_DSN")
	_ = vp.BindEnv("services.license.events.dsn", "EVS_SERVICES_LICENSE_EVENTS_DSN")
	_ = vp.BindEnv("services.license.signing.secret", "EVS_SERVICES_LICENSE_SIGNING_SECRET")
	_ = vp.BindEnv("services.license.http.address", "EVS_SERVICES_LICENSE_HTTP_ADDRESS")
	_ = vp.BindEnv("services.license.billing_service_url", "EVS_SERVICES_LICENSE_BILLING_SERVICE_URL")

	// Auth service config (for standalone auth service deployments)
	_ = vp.BindEnv("services.auth.database.dsn", "EVS_SERVICES_AUTH_DATABASE_DSN")
	_ = vp.BindEnv("services.auth.http.address", "EVS_SERVICES_AUTH_HTTP_ADDRESS")
	_ = vp.BindEnv("services.auth.identity.enabled", "EVS_SERVICES_AUTH_IDENTITY_ENABLED")
	_ = vp.BindEnv("services.auth.identity.base_url", "EVS_SERVICES_AUTH_IDENTITY_BASE_URL")
	_ = vp.BindEnv("services.auth.identity.internal_token", "EVS_IDENTITY_INTERNAL_TOKEN")

	// Billing service config (for standalone billing service deployments)
	_ = vp.BindEnv("services.billing.database.dsn", "EVS_SERVICES_BILLING_DATABASE_DSN")
	_ = vp.BindEnv("services.billing.events.dsn", "EVS_SERVICES_BILLING_EVENTS_DSN")
	_ = vp.BindEnv("services.billing.signing.secret", "EVS_SERVICES_BILLING_SIGNING_SECRET")
	_ = vp.BindEnv("services.billing.http.address", "EVS_SERVICES_BILLING_HTTP_ADDRESS")

	// Gateway callback config (for cloud-to-gateway automatic activation)
	_ = vp.BindEnv("services.gateway.callback.cloud_public_key", "EVS_CLOUD_PUBLIC_KEY")

	// Cloud callback private key (for billing and license services to sign JWTs to gateways)
	_ = vp.BindEnv("services.billing.cloud_callback_private_key", "EVS_CLOUD_CALLBACK_PRIVATE_KEY")

	// Merge into global Viper so policy.FromGlobal() can access bypass rules
	globalViper := viper.GetViper()
	for _, key := range vp.AllKeys() {
		globalViper.Set(key, vp.Get(key))
	}

	var out ServicesConfig
	if err := vp.Unmarshal(&out); err != nil {
		return nil, err
	}

	// Manually populate CloudServices from Viper (new unified format)
	// This handles the case where services.license.url is set (new format)
	// vs services.license being a full config object (legacy format)
	out.CloudServices = CloudServices{
		License: CloudService{
			URL:     vp.GetString("services.license.url"),
			Enabled: vp.GetBool("services.license.enabled"),
		},
		Auth: CloudService{
			URL:     vp.GetString("services.auth.url"),
			Enabled: vp.GetBool("services.auth.enabled"),
		},
		Functions: CloudService{
			URL:     vp.GetString("services.functions.url"),
			Enabled: vp.GetBool("services.functions.enabled"),
		},
		Prompts: CloudService{
			URL:     vp.GetString("services.prompts.url"),
			Enabled: vp.GetBool("services.prompts.enabled"),
		},
	}

	// Ensure backward compatibility:
	// 1. If activation.platform_url is empty, use services.license.url (new format)
	if out.Activation.PlatformURL == "" && out.CloudServices.License.URL != "" {
		out.Activation.PlatformURL = out.CloudServices.License.URL
	}
	// 2. If services.activation.platform_url is set (legacy format), use that
	if out.Services.Activation.PlatformURL != "" && out.Activation.PlatformURL == "" {
		out.Activation.PlatformURL = out.Services.Activation.PlatformURL
	}
	// 3. Copy root-level security to legacy path if legacy is empty
	if out.Services.Security.Policy.BypassServices == nil && out.Security.Policy.BypassServices != nil {
		out.Services.Security = out.Security
	}

	// 4. Override M2M service credentials from environment variables
	// Viper's BindEnv doesn't work well with map keys, so we manually override
	overrideM2MCredentials(&out.Security.M2M)

	// 5. Manually populate M2M client scopes from Viper
	// Viper's Unmarshal doesn't handle map[string]struct{} with slices well
	populateM2MClients(vp, &out.Security.M2M)

	return &out, nil
}

// populateM2MClients manually populates M2M client credentials from Viper.
// This is needed because Viper's Unmarshal doesn't properly handle
// map[string]struct{} where the struct contains slices (like scopes).
func populateM2MClients(vp *viper.Viper, m2m *M2MConfig) {
	// Get the raw clients map from viper
	clientsRaw := vp.GetStringMap("security.m2m.clients")
	if len(clientsRaw) == 0 {
		return
	}

	if m2m.Clients == nil {
		m2m.Clients = make(map[string]M2MClientCredentials)
	}

	for clientName, clientDataRaw := range clientsRaw {
		clientData, ok := clientDataRaw.(map[string]interface{})
		if !ok {
			continue
		}

		cred := m2m.Clients[clientName]

		// Get client_id
		if clientID, ok := clientData["client_id"].(string); ok {
			cred.ClientID = clientID
		}
		if cred.ClientID == "" {
			cred.ClientID = clientName // Default to client name
		}

		// Get client_secret
		if clientSecret, ok := clientData["client_secret"].(string); ok {
			cred.ClientSecret = os.ExpandEnv(clientSecret)
		}

		// Get scopes - Viper doesn't properly unmarshal []string in nested maps
		if scopesRaw, ok := clientData["scopes"]; ok {
			switch scopes := scopesRaw.(type) {
			case []interface{}:
				cred.Scopes = make([]string, 0, len(scopes))
				for _, s := range scopes {
					if str, ok := s.(string); ok {
						cred.Scopes = append(cred.Scopes, str)
					}
				}
			case []string:
				cred.Scopes = scopes
			}
		}

		m2m.Clients[clientName] = cred
	}
}

// overrideM2MCredentials overrides M2M service credentials from environment variables
// and expands any ${ENV_VAR} placeholders in the values
func overrideM2MCredentials(m2m *M2MConfig) {
	if m2m.Services == nil {
		m2m.Services = make(map[string]M2MServiceCredential)
	}

	// Override portal credentials
	portal := m2m.Services["portal"]
	if v := os.Getenv("EVS_M2M_PORTAL_TOKEN_HASH"); v != "" {
		portal.TokenHash = v
	} else {
		portal.TokenHash = os.ExpandEnv(portal.TokenHash)
	}
	if v := os.Getenv("EVS_M2M_PORTAL_SIGNING_KEY"); v != "" {
		portal.SigningKey = v
	} else {
		portal.SigningKey = os.ExpandEnv(portal.SigningKey)
	}
	m2m.Services["portal"] = portal

	// Override internal credentials
	internal := m2m.Services["internal"]
	if v := os.Getenv("EVS_M2M_INTERNAL_TOKEN_HASH"); v != "" {
		internal.TokenHash = v
	} else {
		internal.TokenHash = os.ExpandEnv(internal.TokenHash)
	}
	if v := os.Getenv("EVS_M2M_INTERNAL_SIGNING_KEY"); v != "" {
		internal.SigningKey = v
	} else {
		internal.SigningKey = os.ExpandEnv(internal.SigningKey)
	}
	m2m.Services["internal"] = internal

	// Override gateway credentials
	gateway := m2m.Services["gateway"]
	if v := os.Getenv("EVS_M2M_GATEWAY_TOKEN_HASH"); v != "" {
		gateway.TokenHash = v
	} else {
		gateway.TokenHash = os.ExpandEnv(gateway.TokenHash)
	}
	if v := os.Getenv("EVS_M2M_GATEWAY_SIGNING_KEY"); v != "" {
		gateway.SigningKey = v
	} else {
		gateway.SigningKey = os.ExpandEnv(gateway.SigningKey)
	}
	m2m.Services["gateway"] = gateway
}

// defaultPath returns the path to an optional external config file.
// The external file is NOT required - embedded defaults are sufficient.
// This allows users to provide custom overrides if needed.
func defaultPath() string {
	if p := os.Getenv("EVS_CONFIG_PATH"); strings.TrimSpace(p) != "" {
		return p
	}
	// Legacy env var support
	if p := os.Getenv("EVS_SERVICES_CONFIG"); strings.TrimSpace(p) != "" {
		return p
	}
	// Check common locations for optional override file
	candidates := []string{
		"cmd/config/local.yaml",       // New location (dev overrides)
		"services/config/config.yaml", // Legacy location (for license service)
	}
	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	// Return empty - no external config found, embedded defaults will be used
	return ""
}

// GetLicenseServiceURL returns the license service URL from the loaded config
// This is a convenience method for components that need the URL
func (c *ServicesConfig) GetLicenseServiceURL() string {
	// Prefer the new unified format
	if c.CloudServices.License.URL != "" {
		return c.CloudServices.License.URL
	}
	// Fall back to activation config
	return c.Activation.PlatformURL
}

// ToM2MConfig converts the M2MConfig to the m2m package format.
// This handles both new and legacy config formats.
func (c *M2MConfig) ToM2MConfig() *M2MPackageConfig {
	cfg := &M2MPackageConfig{
		Enabled:         c.Enabled,
		Provider:        c.Provider,
		PublicEndpoints: c.PublicEndpoints,
		Clients:         make(map[string]M2MClientCredentials),
		OIDCClients:     make(map[string]M2MClientCredentials),
	}

	// Default to simple provider if not specified
	if cfg.Provider == "" {
		cfg.Provider = "simple"
	}

	// Configure simple provider
	if c.Simple.SigningKey != "" {
		cfg.SimpleConfig = &M2MSimpleConfig{
			SigningKey: c.Simple.SigningKey,
			Issuer:     c.Simple.Issuer,
			Audience:   c.Simple.Audience,
			TokenTTL:   c.Simple.TokenTTL,
		}
	}

	// Configure OIDC provider
	if c.OIDC.IssuerURL != "" {
		cfg.OIDCConfig = &M2MOIDCConfig{
			IssuerURL:       c.OIDC.IssuerURL,
			TokenURL:        c.OIDC.TokenURL,
			JWKSURL:         c.OIDC.JWKSURL,
			Audience:        c.OIDC.Audience,
			Scopes:          c.OIDC.Scopes,
			SkipIssuerCheck: c.OIDC.SkipIssuerCheck,
		}
	}

	// Copy client credentials (for simple provider, just the names)
	for name, cred := range c.Clients {
		cfg.Clients[name] = cred
	}

	// Copy OIDC-specific client credentials
	for name, cred := range c.OIDCClients {
		cfg.OIDCClients[name] = cred
	}

	// For simple provider: ensure all known services have entries
	// (just the client_id is needed, secrets are derived from signing key)
	if cfg.Provider == "simple" {
		for _, serviceName := range []string{"gateway", "portal", "billing", "license"} {
			if _, exists := cfg.Clients[serviceName]; !exists {
				cfg.Clients[serviceName] = M2MClientCredentials{
					ClientID: serviceName,
				}
			}
		}
	}

	// Migrate legacy service credentials (for backward compatibility)
	for name, legacyCred := range c.Services {
		if _, exists := cfg.OIDCClients[name]; !exists && legacyCred.SigningKey != "" {
			cfg.OIDCClients[name] = M2MClientCredentials{
				ClientID:     name,
				ClientSecret: legacyCred.SigningKey,
			}
		}
	}

	// Migrate bootstrap credentials (for gateway, legacy)
	if c.Bootstrap.SigningKey != "" {
		if _, exists := cfg.OIDCClients["gateway"]; !exists {
			cfg.OIDCClients["gateway"] = M2MClientCredentials{
				ClientID:     c.Bootstrap.ServiceID,
				ClientSecret: c.Bootstrap.SigningKey,
			}
		}
	}

	// Convert legacy endpoint scope rules to map format
	cfg.EndpointScopes = make(map[string][]string)
	for _, rule := range c.EndpointScopes {
		if rule.Endpoint != "" {
			cfg.EndpointScopes[rule.Endpoint] = rule.Scopes
		}
	}

	// Add scope policy overrides to endpoint scopes
	for _, rule := range c.ScopePolicy.Overrides {
		if rule.Endpoint != "" {
			cfg.EndpointScopes[rule.Endpoint] = rule.Scopes
		}
	}

	// Copy scope policy settings
	cfg.ScopePolicy = &M2MScopePolicyConfig{
		AutoDerive:     c.ScopePolicy.AutoDerive,
		ActionPatterns: make([]M2MActionPattern, len(c.ScopePolicy.ActionPatterns)),
	}
	for i, p := range c.ScopePolicy.ActionPatterns {
		cfg.ScopePolicy.ActionPatterns[i] = M2MActionPattern{
			Prefix: p.Prefix,
			Action: p.Action,
		}
	}

	// Copy policy settings
	cfg.AllowAllAuthenticated = c.Policy.AllowAllAuthenticated

	return cfg
}

// M2MPackageConfig is a simplified config structure for the m2m package.
// This is used to pass config to the m2m package without circular imports.
type M2MPackageConfig struct {
	Enabled               bool
	Provider              string
	SimpleConfig          *M2MSimpleConfig
	OIDCConfig            *M2MOIDCConfig
	Clients               map[string]M2MClientCredentials // For simple provider (just names + scopes)
	OIDCClients           map[string]M2MClientCredentials // For OIDC provider (actual credentials)
	PublicEndpoints       []string
	EndpointScopes        map[string][]string   // Explicit overrides: procedure suffix -> required scopes
	ScopePolicy           *M2MScopePolicyConfig // Auto-derive scopes from endpoint names
	AllowAllAuthenticated bool                  // Allow any authenticated client if no scopes defined
}

// M2MScopePolicyConfig for auto-deriving scopes from endpoint names.
type M2MScopePolicyConfig struct {
	AutoDerive     bool
	ActionPatterns []M2MActionPattern
}

// M2MActionPattern maps method prefixes to scope actions.
type M2MActionPattern struct {
	Prefix string
	Action string
}
