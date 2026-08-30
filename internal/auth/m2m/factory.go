package m2m

import (
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// NewTokenProvider creates a TokenProvider based on the configuration.
// For simple provider, clientName is the service name (e.g., "gateway").
// For OIDC provider, credentials must be configured for the clientName.
func NewTokenProvider(config *Config, clientName string) (TokenProvider, error) {
	if config == nil || !config.Enabled {
		return nil, ErrProviderNotConfigured
	}

	switch config.Provider {
	case ProviderSimple:
		if config.SimpleConfig == nil {
			return nil, fmt.Errorf("m2m: simple config is required for simple provider")
		}
		// For simple provider, get scopes from client config
		credentials, ok := config.Clients[clientName]
		if ok && len(credentials.Scopes) > 0 {
			// Use scopes from config
			return NewSimpleTokenProviderWithScopes(config.SimpleConfig, clientName, credentials.Scopes)
		}
		// No scopes configured, just use the client name
		logger.Warnf("m2m: creating token provider for %s without scopes - M2M calls may fail", clientName)
		return NewSimpleTokenProviderForClient(config.SimpleConfig, clientName)

	case ProviderOIDC:
		if config.OIDCConfig == nil {
			return nil, fmt.Errorf("m2m: OIDC config is required for OIDC provider")
		}
		// For OIDC, we need actual credentials from the IdP
		credentials, ok := config.OIDCClients[clientName]
		if !ok {
			// Fall back to regular clients if oidc_clients not set
			credentials, ok = config.Clients[clientName]
			if !ok {
				return nil, fmt.Errorf("m2m: no OIDC credentials for client %q", clientName)
			}
		}
		return NewOIDCTokenProvider(config.OIDCConfig, &credentials)

	default:
		return nil, fmt.Errorf("m2m: unknown provider type %q", config.Provider)
	}
}

// NewTokenValidator creates a TokenValidator based on the configuration.
func NewTokenValidator(config *Config) (TokenValidator, error) {
	if config == nil || !config.Enabled {
		return nil, ErrProviderNotConfigured
	}

	switch config.Provider {
	case ProviderSimple:
		if config.SimpleConfig == nil {
			return nil, fmt.Errorf("m2m: simple config is required for simple provider")
		}
		return NewSimpleTokenValidator(config.SimpleConfig)

	case ProviderOIDC:
		if config.OIDCConfig == nil {
			return nil, fmt.Errorf("m2m: OIDC config is required for OIDC provider")
		}
		return NewOIDCTokenValidator(config.OIDCConfig)

	default:
		return nil, fmt.Errorf("m2m: unknown provider type %q", config.Provider)
	}
}

// MustNewTokenProvider creates a TokenProvider or panics.
func MustNewTokenProvider(config *Config, clientName string) TokenProvider {
	provider, err := NewTokenProvider(config, clientName)
	if err != nil {
		panic(err)
	}
	return provider
}

// MustNewTokenValidator creates a TokenValidator or panics.
func MustNewTokenValidator(config *Config) TokenValidator {
	validator, err := NewTokenValidator(config)
	if err != nil {
		panic(err)
	}
	return validator
}
