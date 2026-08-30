package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents the parsed JWT claims from a signed license.
type Claims struct {
	TenantID              string   `json:"tid"`
	Tier                  string   `json:"tier"`
	Status                string   `json:"status"`
	Features              []string `json:"features,omitempty"`
	UsageLimits           []string `json:"usage_limits,omitempty"`
	SpendLimitAmount      float64  `json:"spend_limit_amount,omitempty"`
	SpendLimitAction      string   `json:"spend_limit_action,omitempty"`
	SpendLimitEnabled     bool     `json:"spend_limit_enabled,omitempty"`
	SandboxBillingEnabled bool     `json:"sandbox_billing_enabled,omitempty"`
	jwt.RegisteredClaims
}

// NearExpiry returns true if the license JWT expires within the given threshold.
func (c *Claims) NearExpiry(threshold time.Duration) bool {
	if c.ExpiresAt == nil {
		return false
	}
	return time.Until(c.ExpiresAt.Time) < threshold
}

// IsExpired returns true if the license JWT has expired.
func (c *Claims) IsExpired() bool {
	if c.ExpiresAt == nil {
		return false
	}
	return c.ExpiresAt.Time.Before(time.Now())
}

// Verifier verifies Ed25519-signed license JWTs.
type Verifier struct {
	publicKey ed25519.PublicKey
}

// NewVerifier creates a verifier from a base64-encoded Ed25519 public key.
func NewVerifier(publicKeyB64 string) (*Verifier, error) {
	if publicKeyB64 == "" {
		return nil, errors.New("empty public key")
	}

	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key: %w", err)
	}

	if len(publicKeyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: got %d, expected %d", len(publicKeyBytes), ed25519.PublicKeySize)
	}

	return &Verifier{
		publicKey: ed25519.PublicKey(publicKeyBytes),
	}, nil
}

// Verify parses and validates an Ed25519-signed license JWT.
// It checks the signature, issuer, and audience.
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	if v == nil {
		return nil, errors.New("verifier is nil")
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return v.publicKey, nil
	},
		jwt.WithIssuer("everstack-license"),
		jwt.WithAudience("everstack-gateway"),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to verify license JWT: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid license JWT claims")
	}

	return claims, nil
}

// VerifyAllowExpired parses and validates an Ed25519-signed license JWT but
// tolerates an expired `exp` claim. Signature, issuer, and audience are still
// verified; expiry is the caller's to evaluate (grace-period handling needs an
// authentic-but-expired token to reconstruct last-known-good entitlements
// after a restart — see docs/design/editions-and-billing.md section 5).
func (v *Verifier) VerifyAllowExpired(tokenString string) (*Claims, error) {
	if v == nil {
		return nil, errors.New("verifier is nil")
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, err := parser.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return v.publicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to verify license JWT: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid license JWT claims")
	}

	// Claims validation was skipped above; enforce issuer/audience manually so
	// only expiry is relaxed.
	if claims.Issuer != "everstack-license" {
		return nil, fmt.Errorf("unexpected issuer: %s", claims.Issuer)
	}
	audOK := false
	for _, aud := range claims.Audience {
		if aud == "everstack-gateway" {
			audOK = true
			break
		}
	}
	if !audOK {
		return nil, errors.New("unexpected audience")
	}

	return claims, nil
}
