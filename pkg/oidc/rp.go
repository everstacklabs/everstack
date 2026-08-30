package oidc

import (
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWKSSource provides RSA public keys by kid for RP-side verification.
type JWKSSource interface {
	KeyByKID(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// StaticJWKSSource serves keys directly from a local KeySet. Useful for tests
// and for an embedded OP (air-gapped self-hosted) where OP and RP share a
// process.
type StaticJWKSSource struct{ ks *KeySet }

// NewStaticJWKSSource wraps a KeySet as a JWKSSource.
func NewStaticJWKSSource(ks *KeySet) StaticJWKSSource { return StaticJWKSSource{ks: ks} }

// KeyByKID implements JWKSSource.
func (s StaticJWKSSource) KeyByKID(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if pk, ok := s.ks.PublicKeyByKID(kid); ok {
		return pk, nil
	}
	return nil, fmt.Errorf("oidc: unknown kid %q", kid)
}

// HTTPJWKSSource fetches and caches a JWKS document from the OP's jwks_uri,
// refreshing when it encounters an unknown kid (to follow key rotation).
type HTTPJWKSSource struct {
	url        string
	client     *http.Client
	minRefresh time.Duration
	now        func() time.Time

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewHTTPJWKSSource builds an HTTP-backed JWKS source for the given jwks_uri.
func NewHTTPJWKSSource(jwksURL string) *HTTPJWKSSource {
	return &HTTPJWKSSource{
		url:        jwksURL,
		client:     &http.Client{Timeout: 5 * time.Second},
		minRefresh: 1 * time.Minute,
		now:        time.Now,
	}
}

// KeyByKID returns the public key for kid, refreshing the cache once (rate
// limited) if the kid is unknown.
func (s *HTTPJWKSSource) KeyByKID(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	s.mu.RLock()
	pk, ok := s.keys[kid]
	stale := s.now().Sub(s.fetchedAt) > s.minRefresh
	s.mu.RUnlock()
	if ok {
		return pk, nil
	}
	if !stale && s.fetchedAt.IsZero() == false {
		// Recently refreshed and still missing — treat as unknown to avoid
		// hammering the OP on every request with a bogus kid.
		return nil, fmt.Errorf("oidc: unknown kid %q", kid)
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if pk, ok := s.keys[kid]; ok {
		return pk, nil
	}
	return nil, fmt.Errorf("oidc: unknown kid %q after refresh", kid)
}

func (s *HTTPJWKSSource) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("oidc: fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: jwks endpoint returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.keys = keys
	s.fetchedAt = s.now()
	s.mu.Unlock()
	return nil
}

// Verifier is the relying-party ID-token verifier. An instance constructs one
// with the cloud OP's issuer, its own client_id (expected audience), and a
// JWKS source.
type Verifier struct {
	issuer   string
	clientID string
	source   JWKSSource
}

// NewVerifier builds an RP verifier.
func NewVerifier(issuer, clientID string, source JWKSSource) *Verifier {
	return &Verifier{issuer: issuer, clientID: clientID, source: source}
}

// ErrNonceMismatch is returned when the id_token nonce does not match the one
// the RP generated for the request (replay protection).
var ErrNonceMismatch = errors.New("oidc: id_token nonce mismatch")

// VerifyIDToken validates the signature (via JWKS by kid), issuer, audience,
// and expiry, then checks the nonce when expectedNonce is non-empty. Returns
// the validated claims.
func (v *Verifier) VerifyIDToken(ctx context.Context, raw, expectedNonce string) (*IDClaims, error) {
	claims := &IDClaims{}
	keyfunc := func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("oidc: unexpected signing method %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("oidc: id_token missing kid")
		}
		return v.source.KeyByKID(ctx, kid)
	}
	_, err := jwt.ParseWithClaims(raw, claims, keyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.clientID),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("oidc: id_token verification failed: %w", err)
	}
	if expectedNonce != "" {
		if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectedNonce)) != 1 {
			return nil, ErrNonceMismatch
		}
	}
	return claims, nil
}
