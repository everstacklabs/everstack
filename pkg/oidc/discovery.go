package oidc

import "strings"

// Standard OP endpoint paths, relative to the issuer.
const (
	PathAuthorize  = "/oauth/authorize"
	PathToken      = "/oauth/token"
	PathJWKS       = "/oauth/jwks"
	PathUserinfo   = "/oauth/userinfo"
	PathEndSession = "/oauth/logout"
	PathDiscovery  = "/.well-known/openid-configuration"
)

// Discovery is the OpenID Provider metadata document served at
// /.well-known/openid-configuration.
type Discovery struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JWKSURI                           string   `json:"jwks_uri"`
	UserinfoEndpoint                  string   `json:"userinfo_endpoint"`
	EndSessionEndpoint                string   `json:"end_session_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	SubjectTypesSupported             []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	ClaimsSupported                   []string `json:"claims_supported"`
}

// Discovery returns the metadata document for this provider.
func (p *Provider) Discovery() Discovery {
	base := strings.TrimRight(p.cfg.Issuer, "/")
	return Discovery{
		Issuer:                base,
		AuthorizationEndpoint: base + PathAuthorize,
		TokenEndpoint:         base + PathToken,
		JWKSURI:               base + PathJWKS,
		UserinfoEndpoint:      base + PathUserinfo,
		EndSessionEndpoint:    base + PathEndSession,
		ResponseTypesSupported: []string{"code"},
		GrantTypesSupported: []string{
			"authorization_code", "refresh_token", "client_credentials",
			"urn:ietf:params:oauth:grant-type:device_code",
		},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{"RS256"},
		ScopesSupported:                  []string{"openid", "profile", "email", "org"},
		// private_key_jwt is preferred for confidential instance clients;
		// client_secret_basic supported for simpler setups.
		TokenEndpointAuthMethodsSupported: []string{"private_key_jwt", "client_secret_basic", "none"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		ClaimsSupported: []string{
			"sub", "email", "email_verified", "name",
			"org_id", "org_slug", "instance_id", "nonce", "auth_time",
		},
	}
}
