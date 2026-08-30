// Package m2m provides a provider-agnostic machine-to-machine authentication layer.
// It supports multiple OAuth2/OIDC providers (Auth0, Keycloak, Zitadel, Okta, etc.)
// as well as a simple self-contained JWT provider for development/low-scale production.
package m2m

import (
	"context"
	"time"
)

// TokenProvider obtains M2M access tokens (client-side).
// Implementations handle token caching and automatic refresh.
type TokenProvider interface {
	// GetToken returns a valid access token, refreshing if needed.
	// The returned token should be used in the Authorization header.
	GetToken(ctx context.Context) (string, error)

	// TokenType returns the token type (typically "Bearer").
	TokenType() string

	// Close releases any resources held by the provider.
	Close() error
}

// TokenValidator validates M2M access tokens (server-side).
// Implementations handle JWKS caching and key rotation.
type TokenValidator interface {
	// ValidateToken validates the token and returns the claims.
	// Returns an error if the token is invalid, expired, or has wrong audience.
	ValidateToken(ctx context.Context, token string) (*Claims, error)

	// Close releases any resources held by the validator.
	Close() error
}

// Claims represents the standard claims extracted from an M2M token.
// These are the claims that all providers should populate.
type Claims struct {
	// Issuer is the token issuer (iss claim)
	Issuer string

	// Subject is the token subject (sub claim) - typically the client_id for M2M
	Subject string

	// Audience is the intended audience (aud claim)
	Audience []string

	// ClientID is the OAuth2 client identifier
	// For most providers, this equals Subject for M2M tokens
	ClientID string

	// Scopes are the granted scopes/permissions
	Scopes []string

	// ExpiresAt is when the token expires (exp claim)
	ExpiresAt time.Time

	// IssuedAt is when the token was issued (iat claim)
	IssuedAt time.Time

	// NotBefore is the earliest time the token is valid (nbf claim)
	NotBefore time.Time

	// TokenID is the unique token identifier (jti claim)
	TokenID string

	// InstanceID is an optional claim for gateway instances
	// This allows tracking which specific gateway instance is making the call
	InstanceID string

	// OrganizationID is an optional claim for multi-tenant scenarios
	OrganizationID string

	// Raw contains the raw token claims for provider-specific extensions
	Raw map[string]interface{}
}

// HasScope checks if the claims include a specific scope.
func (c *Claims) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// HasAudience checks if the claims include a specific audience.
func (c *Claims) HasAudience(aud string) bool {
	for _, a := range c.Audience {
		if a == aud {
			return true
		}
	}
	return false
}

// IsExpired checks if the token has expired.
func (c *Claims) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}

// ProviderType identifies the M2M provider implementation.
type ProviderType string

const (
	// ProviderSimple is a self-contained JWT provider (no external deps)
	ProviderSimple ProviderType = "simple"

	// ProviderOIDC is a generic OIDC provider (Auth0, Keycloak, Zitadel, Okta, etc.)
	ProviderOIDC ProviderType = "oidc"
)

// Config holds the M2M configuration.
type Config struct {
	// Enabled controls whether M2M authentication is active
	Enabled bool

	// Provider specifies which provider to use
	Provider ProviderType

	// SimpleConfig is used when Provider is "simple"
	SimpleConfig *SimpleConfig

	// OIDCConfig is used when Provider is "oidc"
	OIDCConfig *OIDCConfig

	// Clients contains basic client config (for simple provider, just names + scopes)
	// For simple provider: only ClientID is needed (secrets are derived from SigningKey)
	Clients map[string]ClientCredentials

	// OIDCClients contains OIDC-specific credentials
	// These are actual OAuth2 credentials from your IdP, required for OIDC provider
	OIDCClients map[string]ClientCredentials

	// EndpointScopes contains explicit scope overrides for specific endpoints
	// Only used for exceptions - most scopes are auto-derived
	EndpointScopes map[string][]string

	// ScopePolicy configures automatic scope derivation from endpoint names
	ScopePolicy *ScopePolicyConfig

	// AllowAllAuthenticated allows any authenticated client to access endpoints
	// that don't have explicit scope requirements
	AllowAllAuthenticated bool
}

// ScopePolicyConfig configures automatic scope derivation.
type ScopePolicyConfig struct {
	// AutoDerive enables automatic scope derivation from endpoint names
	AutoDerive bool

	// ActionPatterns maps method name prefixes to scope actions
	ActionPatterns []ActionPattern
}

// ActionPattern maps a method prefix to a scope action.
type ActionPattern struct {
	Prefix string // e.g., "Get", "Create", "Delete"
	Action string // e.g., "read", "write", "admin"
}

// ClientCredentials holds OAuth2 client credentials.
type ClientCredentials struct {
	// ClientID is the OAuth2 client identifier
	ClientID string

	// ClientSecret is the OAuth2 client secret
	ClientSecret string

	// Scopes are the scopes to request (optional)
	Scopes []string
}

// KeyLookupFunc looks up a signing key by client identifier.
// Used by the License Service to look up per-device/per-instance signing keys.
// The clientID is extracted from the JWT claims (typically device fingerprint or instance ID).
// Returns the signing key bytes or an error if not found.
type KeyLookupFunc func(ctx context.Context, clientID string) ([]byte, error)

// SimpleConfig configures the simple JWT provider.
type SimpleConfig struct {
	// SigningKey is the HMAC-SHA256 signing key (must be 32+ bytes)
	// This is the master key used to derive per-client keys.
	// If KeyLookup is set, it takes precedence for token validation.
	SigningKey []byte

	// KeyLookup is an optional function to look up signing keys dynamically.
	// When set, the validator will use this to look up the master key for a client
	// instead of using the static SigningKey. This is used by the License Service
	// to validate tokens from self-hosted gateways using their synced signing keys.
	// The lookup is done using the client_id claim from the token.
	KeyLookup KeyLookupFunc

	// Issuer is the token issuer claim
	Issuer string

	// Audience is the expected audience claim
	Audience string

	// TokenTTL is how long tokens are valid
	TokenTTL time.Duration
}

// OIDCConfig configures the generic OIDC provider.
type OIDCConfig struct {
	// IssuerURL is the OIDC issuer URL (used for discovery)
	// e.g., "https://your-tenant.auth0.com" or "https://keycloak.example.com/realms/myrealm"
	IssuerURL string

	// TokenURL is the OAuth2 token endpoint (optional, discovered from issuer if not set)
	TokenURL string

	// JWKSURL is the JWKS endpoint for token validation (optional, discovered from issuer if not set)
	JWKSURL string

	// Audience is the expected audience claim for validation
	Audience string

	// Scopes are additional scopes to request (client_credentials is implicit)
	Scopes []string

	// SkipIssuerCheck skips issuer validation (not recommended for production)
	SkipIssuerCheck bool
}
