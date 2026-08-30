package deviceauth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

const (
	// TokenAudience identifies tokens accepted by Everstack CLI API middleware.
	TokenAudience = "everstack-cli-token"
	// TokenIssuer identifies Everstack as the CLI access-token issuer.
	TokenIssuer = "everstack"
)

// ErrInvalidSigningKey is returned when the shared secret is too weak.
var ErrInvalidSigningKey = errors.New("device token signing key must be at least 32 bytes")

// Identity contains the user, organization, and client bound into a CLI token.
type Identity struct {
	UserID           string
	Email            string
	OrganizationID   string
	OrganizationSlug string
	InstanceID       string
	ClientID         string
}

type tokenClaims struct {
	Email            string `json:"email"`
	OrganizationID   string `json:"organization_id"`
	OrganizationSlug string `json:"organization_slug"`
	InstanceID       string `json:"instance_id,omitempty"`
	ClientID         string `json:"client_id"`
	jwt.RegisteredClaims
}

// TokenManager signs and verifies CLI bearer tokens.
type TokenManager struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewTokenManager creates a CLI token manager with a default token lifetime.
func NewTokenManager(masterKey []byte, ttl time.Duration) (*TokenManager, error) {
	if len(masterKey) < 32 {
		return nil, ErrInvalidSigningKey
	}
	if ttl <= 0 {
		return nil, errors.New("device token TTL must be positive")
	}
	return &TokenManager{
		key: deriveTokenKey(masterKey),
		ttl: ttl,
		now: time.Now,
	}, nil
}

// Issue signs a CLI token using the manager's default lifetime.
func (m *TokenManager) Issue(identity Identity) (string, error) {
	return m.IssueWithTTL(identity, m.ttl)
}

// IssueWithTTL signs a CLI access token with a grant-specific lifetime.
// Device authorization keeps using Issue and its compatibility TTL; browser
// PKCE grants use this method for short-lived access tokens.
func (m *TokenManager) IssueWithTTL(identity Identity, ttl time.Duration) (string, error) {
	if identity.UserID == "" || identity.OrganizationID == "" {
		return "", errors.New("device token identity requires user and organization IDs")
	}
	if ttl <= 0 {
		return "", errors.New("device token TTL must be positive")
	}
	now := m.now().UTC()
	claims := tokenClaims{
		Email:            identity.Email,
		OrganizationID:   identity.OrganizationID,
		OrganizationSlug: identity.OrganizationSlug,
		InstanceID:       identity.InstanceID,
		ClientID:         identity.ClientID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    TokenIssuer,
			Subject:   identity.UserID,
			Audience:  jwt.ClaimStrings{TokenAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.key)
}

// Verify validates a CLI token and returns its bound identity.
func (m *TokenManager) Verify(tokenString string) (*Identity, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}
			return m.key, nil
		},
		jwt.WithAudience(TokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(m.now),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, fmt.Errorf("verify device token: %w", err)
	}
	if claims.Issuer != "" && claims.Issuer != TokenIssuer {
		return nil, errors.New("verify device token: invalid issuer")
	}
	if !token.Valid || claims.Subject == "" || claims.OrganizationID == "" {
		return nil, errors.New("verify device token: required claims are missing")
	}
	return &Identity{
		UserID:           claims.Subject,
		Email:            claims.Email,
		OrganizationID:   claims.OrganizationID,
		OrganizationSlug: claims.OrganizationSlug,
		InstanceID:       claims.InstanceID,
		ClientID:         claims.ClientID,
	}, nil
}

func deriveTokenKey(masterKey []byte) []byte {
	reader := hkdf.New(sha256.New, masterKey, nil, []byte("everstack-m2m-cli-token"))
	derivedKey := make([]byte, 32)
	if _, err := io.ReadFull(reader, derivedKey); err != nil {
		panic(fmt.Sprintf("derive device token key: %v", err))
	}
	return derivedKey
}
