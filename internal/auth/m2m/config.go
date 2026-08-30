package m2m

import (
	"encoding/base64"
	"time"
)

// ConfigFromServicesConfig creates an M2M Config from the services config format.
// This is a convenience function to bridge the config package and m2m package.
type ServicesM2MConfig struct {
	Enabled               bool
	Provider              string
	SimpleConfig          *ServicesSimpleConfig
	OIDCConfig            *ServicesOIDCConfig
	Clients               map[string]ServicesClientCredentials // For simple provider (just names + scopes)
	OIDCClients           map[string]ServicesClientCredentials // For OIDC provider (actual credentials)
	PublicEndpoints       []string
	EndpointScopes        map[string][]string        // Explicit overrides: procedure -> required scopes
	ScopePolicy           *ServicesScopePolicyConfig // Auto-derive scopes from endpoint names
	AllowAllAuthenticated bool                       // Allow any authenticated client if no scopes defined
}

// ServicesScopePolicyConfig for auto-deriving scopes.
type ServicesScopePolicyConfig struct {
	AutoDerive     bool
	ActionPatterns []ServicesActionPattern
}

// ServicesActionPattern maps method prefixes to actions.
type ServicesActionPattern struct {
	Prefix string
	Action string
}

type ServicesSimpleConfig struct {
	SigningKey string
	Issuer     string
	Audience   string
	TokenTTL   string
}

type ServicesOIDCConfig struct {
	IssuerURL       string
	TokenURL        string
	JWKSURL         string
	Audience        string
	Scopes          []string
	SkipIssuerCheck bool
}

type ServicesClientCredentials struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// ConfigFromServices converts a ServicesM2MConfig to an m2m.Config.
func ConfigFromServices(svc *ServicesM2MConfig) (*Config, error) {
	if svc == nil {
		return nil, ErrProviderNotConfigured
	}

	cfg := &Config{
		Enabled:     svc.Enabled,
		Clients:     make(map[string]ClientCredentials),
		OIDCClients: make(map[string]ClientCredentials),
	}

	// Determine provider type
	switch svc.Provider {
	case "oidc":
		cfg.Provider = ProviderOIDC
	default:
		cfg.Provider = ProviderSimple
	}

	// Configure simple provider
	if svc.SimpleConfig != nil && svc.SimpleConfig.SigningKey != "" {
		signingKey, err := base64.StdEncoding.DecodeString(svc.SimpleConfig.SigningKey)
		if err != nil {
			// Try raw string if not base64
			signingKey = []byte(svc.SimpleConfig.SigningKey)
		}

		tokenTTL := 5 * time.Minute
		if svc.SimpleConfig.TokenTTL != "" {
			if d, err := time.ParseDuration(svc.SimpleConfig.TokenTTL); err == nil {
				tokenTTL = d
			}
		}

		issuer := svc.SimpleConfig.Issuer
		if issuer == "" {
			issuer = "everstack"
		}

		audience := svc.SimpleConfig.Audience
		if audience == "" {
			audience = "everstack-services"
		}

		cfg.SimpleConfig = &SimpleConfig{
			SigningKey: signingKey,
			Issuer:     issuer,
			Audience:   audience,
			TokenTTL:   tokenTTL,
		}
	}

	// Configure OIDC provider
	if svc.OIDCConfig != nil && svc.OIDCConfig.IssuerURL != "" {
		cfg.OIDCConfig = &OIDCConfig{
			IssuerURL:       svc.OIDCConfig.IssuerURL,
			TokenURL:        svc.OIDCConfig.TokenURL,
			JWKSURL:         svc.OIDCConfig.JWKSURL,
			Audience:        svc.OIDCConfig.Audience,
			Scopes:          svc.OIDCConfig.Scopes,
			SkipIssuerCheck: svc.OIDCConfig.SkipIssuerCheck,
		}
	}

	// Convert basic client credentials (for simple provider, just names)
	for name, cred := range svc.Clients {
		cfg.Clients[name] = ClientCredentials{
			ClientID:     cred.ClientID,
			ClientSecret: cred.ClientSecret,
			Scopes:       cred.Scopes,
		}
	}

	// Convert OIDC client credentials
	for name, cred := range svc.OIDCClients {
		cfg.OIDCClients[name] = ClientCredentials{
			ClientID:     cred.ClientID,
			ClientSecret: cred.ClientSecret,
			Scopes:       cred.Scopes,
		}
	}

	// For simple provider: ensure all known services have entries
	if cfg.Provider == ProviderSimple {
		for _, serviceName := range []string{"gateway", "portal", "billing", "license"} {
			if _, exists := cfg.Clients[serviceName]; !exists {
				cfg.Clients[serviceName] = ClientCredentials{
					ClientID: serviceName,
				}
			}
		}
	}

	// Copy endpoint scope mappings (explicit overrides)
	cfg.EndpointScopes = svc.EndpointScopes
	cfg.AllowAllAuthenticated = svc.AllowAllAuthenticated

	// Copy scope policy for auto-derivation
	if svc.ScopePolicy != nil {
		cfg.ScopePolicy = &ScopePolicyConfig{
			AutoDerive:     svc.ScopePolicy.AutoDerive,
			ActionPatterns: make([]ActionPattern, len(svc.ScopePolicy.ActionPatterns)),
		}
		for i, p := range svc.ScopePolicy.ActionPatterns {
			cfg.ScopePolicy.ActionPatterns[i] = ActionPattern{
				Prefix: p.Prefix,
				Action: p.Action,
			}
		}
	}

	return cfg, nil
}
