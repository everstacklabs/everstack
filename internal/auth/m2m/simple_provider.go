package m2m

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

// SimpleTokenProvider implements TokenProvider using self-contained JWTs.
// This is useful for development and low-scale production without external dependencies.
//
// Client credentials are derived internally from the signing key - no need to configure
// individual client_id/client_secret pairs. Just provide the service name (e.g., "gateway").
type SimpleTokenProvider struct {
	config     *SimpleConfig
	clientName string   // e.g., "gateway", "portal", "billing"
	scopes     []string // Scopes to include in the token

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// NewSimpleTokenProvider creates a new simple JWT token provider.
// The clientName is the service identifier (e.g., "gateway", "portal", "billing").
// Client credentials are derived from the signing key - no explicit credentials needed.
// Scopes from credentials are embedded in the JWT token.
func NewSimpleTokenProvider(config *SimpleConfig, credentials *ClientCredentials) (*SimpleTokenProvider, error) {
	if config == nil {
		return nil, fmt.Errorf("simple config is required")
	}
	if len(config.SigningKey) < 32 {
		return nil, fmt.Errorf("signing key must be at least 32 bytes")
	}

	// For simple provider, we only need the client ID (service name)
	// The secret is derived from the signing key
	clientName := ""
	var scopes []string
	if credentials != nil {
		clientName = credentials.ClientID
		scopes = credentials.Scopes
	}
	if clientName == "" {
		return nil, fmt.Errorf("client name is required")
	}

	if config.TokenTTL == 0 {
		config.TokenTTL = 5 * time.Minute
	}
	if config.Issuer == "" {
		config.Issuer = "everstack"
	}
	if config.Audience == "" {
		config.Audience = "everstack-services"
	}

	return &SimpleTokenProvider{
		config:     config,
		clientName: clientName,
		scopes:     scopes,
	}, nil
}

// NewSimpleTokenProviderForClient creates a provider for a specific service.
// This is the preferred constructor - just specify the service name.
func NewSimpleTokenProviderForClient(config *SimpleConfig, clientName string) (*SimpleTokenProvider, error) {
	return NewSimpleTokenProvider(config, &ClientCredentials{ClientID: clientName})
}

// NewSimpleTokenProviderWithScopes creates a provider with specific scopes.
func NewSimpleTokenProviderWithScopes(config *SimpleConfig, clientName string, scopes []string) (*SimpleTokenProvider, error) {
	return NewSimpleTokenProvider(config, &ClientCredentials{ClientID: clientName, Scopes: scopes})
}

// GetToken returns a valid access token, generating a new one if needed.
func (p *SimpleTokenProvider) GetToken(ctx context.Context) (string, error) {
	// Check if we have a valid cached token (with 30s buffer)
	p.mu.RLock()
	if p.token != "" && time.Now().Add(30*time.Second).Before(p.expiresAt) {
		token := p.token
		p.mu.RUnlock()
		return token, nil
	}
	p.mu.RUnlock()

	// Generate new token
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if p.token != "" && time.Now().Add(30*time.Second).Before(p.expiresAt) {
		return p.token, nil
	}

	now := time.Now()
	expiresAt := now.Add(p.config.TokenTTL)

	token, err := p.generateToken(now, expiresAt)
	if err != nil {
		return "", err
	}

	p.token = token
	p.expiresAt = expiresAt

	return token, nil
}

// TokenType returns "Bearer".
func (p *SimpleTokenProvider) TokenType() string {
	return "Bearer"
}

// Close releases resources (no-op for simple provider).
func (p *SimpleTokenProvider) Close() error {
	return nil
}

// generateToken creates a new JWT token.
func (p *SimpleTokenProvider) generateToken(now, expiresAt time.Time) (string, error) {
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}

	claims := map[string]interface{}{
		"iss":       p.config.Issuer,
		"sub":       p.clientName,
		"aud":       p.config.Audience,
		"client_id": p.clientName,
		"iat":       now.Unix(),
		"exp":       expiresAt.Unix(),
		"nbf":       now.Unix(),
		"jti":       uuid.New().String(),
	}

	// Add scopes to the token if configured
	if len(p.scopes) > 0 {
		claims["scope"] = strings.Join(p.scopes, " ")
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64
	signature := p.sign([]byte(signingInput))
	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + signatureB64, nil
}

// sign computes HMAC-SHA256 signature using a client-specific derived key.
// This ensures each service has a unique signing key derived from the master key.
func (p *SimpleTokenProvider) sign(data []byte) []byte {
	// Derive a client-specific key from the master signing key
	clientKey := deriveClientKey(p.config.SigningKey, p.clientName)
	h := hmac.New(sha256.New, clientKey)
	h.Write(data)
	return h.Sum(nil)
}

// deriveClientKey derives a client-specific signing key from the master key.
// Uses HKDF to create unique keys for each client while only requiring
// one master key to be configured.
func deriveClientKey(masterKey []byte, clientName string) []byte {
	info := []byte("everstack-m2m-client-" + clientName)
	reader := hkdf.New(sha256.New, masterKey, nil, info)
	derivedKey := make([]byte, 32)
	if _, err := io.ReadFull(reader, derivedKey); err != nil {
		// Fallback to master key if derivation fails
		return masterKey
	}
	return derivedKey
}

// SimpleTokenValidator implements TokenValidator for simple JWTs.
type SimpleTokenValidator struct {
	config *SimpleConfig
}

// NewSimpleTokenValidator creates a new simple JWT token validator.
// Either SigningKey must be at least 32 bytes, or KeyLookup must be configured.
func NewSimpleTokenValidator(config *SimpleConfig) (*SimpleTokenValidator, error) {
	if config == nil {
		return nil, fmt.Errorf("simple config is required")
	}
	// Allow empty SigningKey if KeyLookup is configured (for per-device key lookup)
	if len(config.SigningKey) < 32 && config.KeyLookup == nil {
		return nil, fmt.Errorf("signing key must be at least 32 bytes (or KeyLookup must be configured)")
	}
	if config.Issuer == "" {
		config.Issuer = "everstack"
	}
	if config.Audience == "" {
		config.Audience = "everstack-services"
	}

	return &SimpleTokenValidator{config: config}, nil
}

// ValidateToken validates the JWT and returns claims.
func (v *SimpleTokenValidator) ValidateToken(ctx context.Context, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerB64, claimsB64, signatureB64 := parts[0], parts[1], parts[2]

	// Decode claims first to get the client_id for key derivation
	claimsJSON, err := base64.RawURLEncoding.DecodeString(claimsB64)
	if err != nil {
		return nil, ErrInvalidToken
	}

	var rawClaims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &rawClaims); err != nil {
		return nil, ErrInvalidToken
	}

	// Get client_id to derive the correct signing key
	clientID := ""
	if cid, ok := rawClaims["client_id"].(string); ok {
		clientID = cid
	} else if sub, ok := rawClaims["sub"].(string); ok {
		clientID = sub
	}
	if clientID == "" {
		return nil, ErrInvalidToken
	}

	// Verify signature using the client-specific derived key
	signingInput := headerB64 + "." + claimsB64
	actualSig, err := base64.RawURLEncoding.DecodeString(signatureB64)
	if err != nil {
		return nil, ErrInvalidToken
	}

	if !v.verifySignature(ctx, signingInput, actualSig, clientID) {
		return nil, ErrInvalidToken
	}

	// Parse claims
	claims := &Claims{Raw: rawClaims}

	if iss, ok := rawClaims["iss"].(string); ok {
		claims.Issuer = iss
	}
	if sub, ok := rawClaims["sub"].(string); ok {
		claims.Subject = sub
	}
	claims.ClientID = clientID
	if cid, ok := rawClaims["client_id"].(string); ok {
		claims.ClientID = cid
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

	// Parse scopes (space-separated string)
	if scope, ok := rawClaims["scope"].(string); ok && scope != "" {
		claims.Scopes = strings.Split(scope, " ")
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

	// Parse optional claims
	if instanceID, ok := rawClaims["instance_id"].(string); ok {
		claims.InstanceID = instanceID
	}
	if orgID, ok := rawClaims["org_id"].(string); ok {
		claims.OrganizationID = orgID
	}

	// Validate issuer
	if v.config.Issuer != "" && claims.Issuer != v.config.Issuer {
		return nil, ErrInvalidIssuer
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

// Close releases resources (no-op for simple validator).
func (v *SimpleTokenValidator) Close() error {
	return nil
}

// verifySignature verifies the token signature using the client-specific derived key.
// If KeyLookup is configured, it attempts to look up the master key for this client.
// This enables the License Service to validate tokens from self-hosted gateways
// that have synced their signing keys.
func (v *SimpleTokenValidator) verifySignature(ctx context.Context, signingInput string, actualSig []byte, clientID string) bool {
	var masterKey []byte

	// Try KeyLookup first if configured (for per-device/per-instance keys)
	if v.config.KeyLookup != nil {
		lookedUpKey, err := v.config.KeyLookup(ctx, clientID)
		if err == nil && len(lookedUpKey) >= 32 {
			masterKey = lookedUpKey
		}
		// If lookup fails, fall through to static key
	}

	// Fall back to static signing key if lookup didn't provide a key
	if len(masterKey) == 0 {
		masterKey = v.config.SigningKey
	}

	// Still no key? Can't verify
	if len(masterKey) < 32 {
		return false
	}

	// Derive the client-specific key from the master key
	clientKey := deriveClientKey(masterKey, clientID)
	h := hmac.New(sha256.New, clientKey)
	h.Write([]byte(signingInput))
	expectedSig := h.Sum(nil)
	return hmac.Equal(expectedSig, actualSig)
}
