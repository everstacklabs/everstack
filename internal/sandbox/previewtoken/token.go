// Package previewtoken implements HMAC-SHA256 signed preview URL tokens.
//
// A signed preview URL lets callers share a sandbox port preview with
// third parties (iframe embeds, link sharing) without requiring them to
// send custom HTTP headers -- the auth token is embedded in the URL itself
// as a query parameter.
//
// Token format: base64url(JSON claims) + "." + base64url(HMAC-SHA256 signature)
//
// The proxy validates the token on every request. Once validated, a short-lived
// cookie is set so subsequent same-session requests in a browser don't re-verify
// on every hit (avoids HMAC work on hot paths).
package previewtoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// QueryParam is the URL query parameter name for signed tokens.
	QueryParam = "_preview_token"
	// CookiePrefix is prepended to the subdomain to form the cookie name.
	// e.g. "_sbxp_xK3p9q2A-3000"
	CookiePrefix = "_sbxp_"
	// CookieMaxAge is how long the browser-side validation cookie survives.
	// Short on purpose -- the token itself carries the real expiry.
	CookieMaxAge = 10 * time.Minute
)

// Claims is the payload embedded in a signed preview token.
type Claims struct {
	SandboxID string `json:"sid"`
	Subdomain string `json:"sub"` // exact subdomain this token is valid for
	TenantID  string `json:"tid"`
	Port      int    `json:"port"`
	ExpiresAt int64  `json:"exp"` // Unix seconds
}

// Signer signs and verifies preview tokens using HMAC-SHA256.
type Signer struct {
	secret []byte
}

// ErrTokenExpired is returned when a token's exp claim is in the past.
var ErrTokenExpired = errors.New("preview token expired")

// ErrTokenInvalid is returned for any structural or signature failure.
var ErrTokenInvalid = errors.New("preview token invalid")

// NewSigner creates a Signer with the given secret. If secret is empty
// a random 32-byte key is generated. The random key does not survive
// process restarts, making tokens ephemeral -- acceptable for short-lived
// (sub-hour) preview URLs that are regenerated on demand.
func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("previewtoken: generate secret: %w", err)
		}
	}
	return &Signer{secret: secret}, nil
}

// Sign creates a signed token for the given claims and expiry duration.
func (s *Signer) Sign(claims Claims, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("previewtoken: ttl must be positive")
	}
	claims.ExpiresAt = time.Now().Add(ttl).Unix()
	return s.signClaims(claims)
}

// signClaims signs already-populated claims (ExpiresAt must be set by caller).
// Used internally and by tests.
func (s *Signer) signClaims(claims Claims) (string, error) {

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("previewtoken: marshal claims: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	sig := s.sign([]byte(encodedPayload))
	encodedSig := base64.RawURLEncoding.EncodeToString(sig)
	return encodedPayload + "." + encodedSig, nil
}

// Verify parses and validates a token string. Returns the embedded claims
// on success. Returns ErrTokenExpired or ErrTokenInvalid on failure.
func (s *Signer) Verify(token string) (Claims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return Claims{}, ErrTokenInvalid
	}
	encodedPayload, encodedSig := parts[0], parts[1]

	sig, err := base64.RawURLEncoding.DecodeString(encodedSig)
	if err != nil {
		return Claims{}, ErrTokenInvalid
	}
	expected := s.sign([]byte(encodedPayload))
	if !hmac.Equal(sig, expected) {
		return Claims{}, ErrTokenInvalid
	}

	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return Claims{}, ErrTokenInvalid
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, ErrTokenInvalid
	}
	if time.Now().Unix() > claims.ExpiresAt {
		return Claims{}, ErrTokenExpired
	}
	return claims, nil
}

func (s *Signer) sign(data []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(data)
	return mac.Sum(nil)
}
