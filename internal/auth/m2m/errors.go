package m2m

import "errors"

// M2M authentication errors
var (
	// ErrTokenExpired indicates the token has expired
	ErrTokenExpired = errors.New("m2m: token expired")

	// ErrTokenNotYetValid indicates the token is not yet valid (nbf claim)
	ErrTokenNotYetValid = errors.New("m2m: token not yet valid")

	// ErrInvalidToken indicates the token is malformed or has invalid signature
	ErrInvalidToken = errors.New("m2m: invalid token")

	// ErrInvalidAudience indicates the token audience doesn't match expected
	ErrInvalidAudience = errors.New("m2m: invalid audience")

	// ErrInvalidIssuer indicates the token issuer doesn't match expected
	ErrInvalidIssuer = errors.New("m2m: invalid issuer")

	// ErrMissingToken indicates no token was provided
	ErrMissingToken = errors.New("m2m: missing token")

	// ErrTokenRefreshFailed indicates token refresh failed
	ErrTokenRefreshFailed = errors.New("m2m: token refresh failed")

	// ErrProviderNotConfigured indicates the M2M provider is not configured
	ErrProviderNotConfigured = errors.New("m2m: provider not configured")

	// ErrInvalidClientCredentials indicates invalid client_id or client_secret
	ErrInvalidClientCredentials = errors.New("m2m: invalid client credentials")

	// ErrJWKSFetchFailed indicates JWKS endpoint fetch failed
	ErrJWKSFetchFailed = errors.New("m2m: failed to fetch JWKS")

	// ErrKeyNotFound indicates the signing key was not found in JWKS
	ErrKeyNotFound = errors.New("m2m: signing key not found")

	// ErrDiscoveryFailed indicates OIDC discovery failed
	ErrDiscoveryFailed = errors.New("m2m: OIDC discovery failed")

	// ErrInsufficientScope indicates the client doesn't have required scopes
	ErrInsufficientScope = errors.New("m2m: insufficient scope for this operation")
)
