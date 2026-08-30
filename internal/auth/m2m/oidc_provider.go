package m2m

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCTokenProvider implements TokenProvider using OAuth2 client credentials flow.
// Works with any OIDC-compliant provider (Auth0, Keycloak, Zitadel, Okta, etc.)
type OIDCTokenProvider struct {
	config      *OIDCConfig
	credentials *ClientCredentials
	httpClient  *http.Client

	mu        sync.RWMutex
	token     string
	expiresAt time.Time

	// Discovered endpoints
	tokenURL string
}

// NewOIDCTokenProvider creates a new OIDC token provider.
func NewOIDCTokenProvider(config *OIDCConfig, credentials *ClientCredentials) (*OIDCTokenProvider, error) {
	if config == nil {
		return nil, fmt.Errorf("OIDC config is required")
	}
	if credentials == nil || credentials.ClientID == "" || credentials.ClientSecret == "" {
		return nil, fmt.Errorf("client credentials are required")
	}

	provider := &OIDCTokenProvider{
		config:      config,
		credentials: credentials,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Use explicit token URL if provided, otherwise discover
	if config.TokenURL != "" {
		provider.tokenURL = config.TokenURL
	}

	return provider, nil
}

// GetToken returns a valid access token, refreshing if needed.
func (p *OIDCTokenProvider) GetToken(ctx context.Context) (string, error) {
	// Check if we have a valid cached token (with 30s buffer)
	p.mu.RLock()
	if p.token != "" && time.Now().Add(30*time.Second).Before(p.expiresAt) {
		token := p.token
		p.mu.RUnlock()
		return token, nil
	}
	p.mu.RUnlock()

	// Fetch new token
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if p.token != "" && time.Now().Add(30*time.Second).Before(p.expiresAt) {
		return p.token, nil
	}

	// Discover token endpoint if not set
	if p.tokenURL == "" {
		if err := p.discover(ctx); err != nil {
			return "", err
		}
	}

	// Fetch token using client credentials flow
	token, expiresIn, err := p.fetchToken(ctx)
	if err != nil {
		return "", err
	}

	p.token = token
	p.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)

	return token, nil
}

// TokenType returns "Bearer".
func (p *OIDCTokenProvider) TokenType() string {
	return "Bearer"
}

// Close releases resources.
func (p *OIDCTokenProvider) Close() error {
	return nil
}

// discover performs OIDC discovery to find the token endpoint.
func (p *OIDCTokenProvider) discover(ctx context.Context) error {
	if p.config.IssuerURL == "" {
		return ErrProviderNotConfigured
	}

	discoveryURL := strings.TrimSuffix(p.config.IssuerURL, "/") + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDiscoveryFailed, err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDiscoveryFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrDiscoveryFailed, resp.StatusCode)
	}

	var discovery struct {
		TokenEndpoint string `json:"token_endpoint"`
		JwksURI       string `json:"jwks_uri"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return fmt.Errorf("%w: %v", ErrDiscoveryFailed, err)
	}

	p.tokenURL = discovery.TokenEndpoint
	return nil
}

// fetchToken fetches a new token using client credentials flow.
func (p *OIDCTokenProvider) fetchToken(ctx context.Context) (token string, expiresIn int, err error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", p.credentials.ClientID)
	data.Set("client_secret", p.credentials.ClientSecret)

	if p.config.Audience != "" {
		data.Set("audience", p.config.Audience)
	}

	scopes := p.credentials.Scopes
	if len(scopes) == 0 {
		scopes = p.config.Scopes
	}
	if len(scopes) > 0 {
		data.Set("scope", strings.Join(scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrTokenRefreshFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrTokenRefreshFailed, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("%w: status %d: %s", ErrInvalidClientCredentials, resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrTokenRefreshFailed, err)
	}

	if tokenResp.ExpiresIn == 0 {
		tokenResp.ExpiresIn = 3600 // Default 1 hour
	}

	return tokenResp.AccessToken, tokenResp.ExpiresIn, nil
}

// OIDCTokenValidator implements TokenValidator using JWKS.
// Works with any OIDC-compliant provider.
type OIDCTokenValidator struct {
	config     *OIDCConfig
	httpClient *http.Client

	mu       sync.RWMutex
	jwks     *JWKS
	jwksTime time.Time
}

// JWKS represents a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK represents a JSON Web Key.
type JWK struct {
	Kty string `json:"kty"` // Key type (RSA, EC)
	Use string `json:"use"` // sig or enc
	Kid string `json:"kid"` // Key ID
	Alg string `json:"alg"` // Algorithm
	N   string `json:"n"`   // RSA modulus
	E   string `json:"e"`   // RSA exponent
}

// NewOIDCTokenValidator creates a new OIDC token validator.
func NewOIDCTokenValidator(config *OIDCConfig) (*OIDCTokenValidator, error) {
	if config == nil {
		return nil, fmt.Errorf("OIDC config is required")
	}

	return &OIDCTokenValidator{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// ValidateToken validates the JWT and returns claims.
func (v *OIDCTokenValidator) ValidateToken(ctx context.Context, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerB64, claimsB64, signatureB64 := parts[0], parts[1], parts[2]

	// Decode header to get kid
	headerJSON, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return nil, ErrInvalidToken
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, ErrInvalidToken
	}

	// Get the signing key from JWKS
	key, err := v.getSigningKey(ctx, header.Kid)
	if err != nil {
		return nil, err
	}

	// Verify signature based on algorithm
	signingInput := headerB64 + "." + claimsB64
	signature, err := base64.RawURLEncoding.DecodeString(signatureB64)
	if err != nil {
		return nil, ErrInvalidToken
	}

	if err := v.verifySignature(header.Alg, []byte(signingInput), signature, key); err != nil {
		return nil, err
	}

	// Decode claims
	claimsJSON, err := base64.RawURLEncoding.DecodeString(claimsB64)
	if err != nil {
		return nil, ErrInvalidToken
	}

	var rawClaims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &rawClaims); err != nil {
		return nil, ErrInvalidToken
	}

	// Parse claims
	claims := v.parseClaims(rawClaims)

	// Validate issuer
	if !v.config.SkipIssuerCheck && v.config.IssuerURL != "" {
		expectedIssuer := strings.TrimSuffix(v.config.IssuerURL, "/")
		actualIssuer := strings.TrimSuffix(claims.Issuer, "/")
		if actualIssuer != expectedIssuer {
			return nil, ErrInvalidIssuer
		}
	}

	// Validate audience
	if v.config.Audience != "" && !claims.HasAudience(v.config.Audience) {
		return nil, ErrInvalidAudience
	}

	// Validate expiration
	now := time.Now()
	if !claims.ExpiresAt.IsZero() && now.After(claims.ExpiresAt) {
		return nil, ErrTokenExpired
	}

	// Validate not before
	if !claims.NotBefore.IsZero() && now.Before(claims.NotBefore) {
		return nil, ErrTokenNotYetValid
	}

	return claims, nil
}

// Close releases resources.
func (v *OIDCTokenValidator) Close() error {
	return nil
}

// getSigningKey fetches the signing key from JWKS.
func (v *OIDCTokenValidator) getSigningKey(ctx context.Context, kid string) (*JWK, error) {
	// Refresh JWKS if needed (cache for 1 hour)
	v.mu.RLock()
	if v.jwks != nil && time.Since(v.jwksTime) < time.Hour {
		for i := range v.jwks.Keys {
			if v.jwks.Keys[i].Kid == kid {
				key := v.jwks.Keys[i]
				v.mu.RUnlock()
				return &key, nil
			}
		}
	}
	v.mu.RUnlock()

	// Fetch JWKS
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.fetchJWKS(ctx); err != nil {
		return nil, err
	}

	for i := range v.jwks.Keys {
		if v.jwks.Keys[i].Kid == kid {
			return &v.jwks.Keys[i], nil
		}
	}

	return nil, ErrKeyNotFound
}

// fetchJWKS fetches the JWKS from the provider.
func (v *OIDCTokenValidator) fetchJWKS(ctx context.Context) error {
	jwksURL := v.config.JWKSURL
	if jwksURL == "" {
		// Discover JWKS URL
		discoveryURL := strings.TrimSuffix(v.config.IssuerURL, "/") + "/.well-known/openid-configuration"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrJWKSFetchFailed, err)
		}

		resp, err := v.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrJWKSFetchFailed, err)
		}
		defer resp.Body.Close()

		var discovery struct {
			JwksURI string `json:"jwks_uri"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
			return fmt.Errorf("%w: %v", ErrJWKSFetchFailed, err)
		}
		jwksURL = discovery.JwksURI
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrJWKSFetchFailed, err)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrJWKSFetchFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrJWKSFetchFailed, resp.StatusCode)
	}

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("%w: %v", ErrJWKSFetchFailed, err)
	}

	v.jwks = &jwks
	v.jwksTime = time.Now()
	return nil
}

// verifySignature verifies the JWT signature.
func (v *OIDCTokenValidator) verifySignature(alg string, signingInput, signature []byte, key *JWK) error {
	switch alg {
	case "RS256", "RS384", "RS512":
		return v.verifyRSA(alg, signingInput, signature, key)
	case "HS256", "HS384", "HS512":
		// HMAC algorithms not typically used with OIDC providers
		return fmt.Errorf("%w: HMAC algorithms not supported for OIDC", ErrInvalidToken)
	default:
		return fmt.Errorf("%w: unsupported algorithm %s", ErrInvalidToken, alg)
	}
}

// verifyRSA verifies an RSA signature.
func (v *OIDCTokenValidator) verifyRSA(alg string, signingInput, signature []byte, key *JWK) error {
	if key.Kty != "RSA" {
		return fmt.Errorf("%w: expected RSA key", ErrInvalidToken)
	}

	// Decode modulus and exponent
	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return ErrInvalidToken
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return ErrInvalidToken
	}

	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}

	pubKey := &rsa.PublicKey{N: n, E: e}

	// Select hash based on algorithm
	hash := cryptoHashForAlg(alg)
	if hash == 0 {
		return fmt.Errorf("%w: unsupported algorithm %s", ErrInvalidToken, alg)
	}

	hasher := hash.New()
	hasher.Write(signingInput)
	hashed := hasher.Sum(nil)

	if err := rsa.VerifyPKCS1v15(pubKey, hash, hashed, signature); err != nil {
		return ErrInvalidToken
	}

	return nil
}

// parseClaims parses raw claims into structured Claims.
func (v *OIDCTokenValidator) parseClaims(rawClaims map[string]interface{}) *Claims {
	claims := &Claims{Raw: rawClaims}

	if iss, ok := rawClaims["iss"].(string); ok {
		claims.Issuer = iss
	}
	if sub, ok := rawClaims["sub"].(string); ok {
		claims.Subject = sub
	}
	if clientID, ok := rawClaims["client_id"].(string); ok {
		claims.ClientID = clientID
	} else if azp, ok := rawClaims["azp"].(string); ok {
		// Auth0 uses azp for client_id
		claims.ClientID = azp
	} else {
		claims.ClientID = claims.Subject
	}
	if jti, ok := rawClaims["jti"].(string); ok {
		claims.TokenID = jti
	}

	// Parse audience (can be string or array)
	switch aud := rawClaims["aud"].(type) {
	case string:
		claims.Audience = []string{aud}
	case []interface{}:
		for _, a := range aud {
			if s, ok := a.(string); ok {
				claims.Audience = append(claims.Audience, s)
			}
		}
	}

	// Parse scopes (space-separated string or array)
	switch scope := rawClaims["scope"].(type) {
	case string:
		if scope != "" {
			claims.Scopes = strings.Split(scope, " ")
		}
	case []interface{}:
		for _, s := range scope {
			if str, ok := s.(string); ok {
				claims.Scopes = append(claims.Scopes, str)
			}
		}
	}

	// Parse timestamps
	if exp, ok := rawClaims["exp"].(float64); ok {
		claims.ExpiresAt = time.Unix(int64(exp), 0)
	}
	if iat, ok := rawClaims["iat"].(float64); ok {
		claims.IssuedAt = time.Unix(int64(iat), 0)
	}
	if nbf, ok := rawClaims["nbf"].(float64); ok {
		claims.NotBefore = time.Unix(int64(nbf), 0)
	}

	// Parse optional claims (provider-specific)
	if instanceID, ok := rawClaims["instance_id"].(string); ok {
		claims.InstanceID = instanceID
	}
	if orgID, ok := rawClaims["org_id"].(string); ok {
		claims.OrganizationID = orgID
	}

	return claims
}
