// Package oidc implements the federation primitives for Everstack's identity
// model: the cloud is an OpenID Provider (OP) that brokers WorkOS, and each
// instance is a relying party (RP). This package provides the asymmetric
// signing/JWKS layer, the authorization-code + PKCE + token flow on the OP
// side, and ID-token verification on the RP side.
//
// Asymmetric (RS256) signing is mandatory for federation: instances must verify
// tokens via the OP's published JWKS without ever holding a signing secret.
// This replaces the prior model where every JWT derived from one shared HS256
// master key.
package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"github.com/golang-jwt/jwt/v5"
)

// signingKey is one RSA keypair identified by a key id (kid).
type signingKey struct {
	kid  string
	priv *rsa.PrivateKey
}

// KeySet holds the OP's active signing key plus recently-rotated keys, retained
// so tokens signed just before a rotation still verify. Safe for concurrent use.
type KeySet struct {
	mu          sync.RWMutex
	active      *signingKey
	previous    []*signingKey
	maxPrevious int
}

// GenerateKeySet creates a KeySet with a fresh active RSA key. bits<2048 is
// rejected; pass 2048 for a sensible default.
func GenerateKeySet(bits int) (*KeySet, error) {
	if bits < 2048 {
		return nil, fmt.Errorf("oidc: RSA key size %d too small (min 2048)", bits)
	}
	k, err := newSigningKey(bits)
	if err != nil {
		return nil, err
	}
	return &KeySet{active: k, maxPrevious: 2}, nil
}

// NewKeySetFromPrivateKey wraps an existing RSA private key (e.g. loaded from a
// sealed secret) as the active key.
func NewKeySetFromPrivateKey(priv *rsa.PrivateKey) *KeySet {
	return &KeySet{active: &signingKey{kid: thumbprint(&priv.PublicKey), priv: priv}, maxPrevious: 2}
}

func newSigningKey(bits int) (*signingKey, error) {
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, err
	}
	return &signingKey{kid: thumbprint(&priv.PublicKey), priv: priv}, nil
}

// thumbprint derives a stable kid from the public key (sha256 over modulus and
// exponent, base64url, truncated). Deterministic so the same key always gets the
// same kid across restarts.
func thumbprint(pub *rsa.PublicKey) string {
	h := sha256.New()
	h.Write(pub.N.Bytes())
	h.Write(big.NewInt(int64(pub.E)).Bytes())
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))[:16]
}

// ActiveKID returns the current signing key id.
func (ks *KeySet) ActiveKID() string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.active.kid
}

// Sign signs claims with the active key using RS256 and stamps the kid header.
func (ks *KeySet) Sign(claims jwt.Claims) (string, error) {
	ks.mu.RLock()
	k := ks.active
	ks.mu.RUnlock()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = k.kid
	return tok.SignedString(k.priv)
}

// PublicKeyByKID returns the RSA public key for a kid (active or recently
// rotated), for local verification.
func (ks *KeySet) PublicKeyByKID(kid string) (*rsa.PublicKey, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	if ks.active != nil && ks.active.kid == kid {
		return &ks.active.priv.PublicKey, true
	}
	for _, k := range ks.previous {
		if k.kid == kid {
			return &k.priv.PublicKey, true
		}
	}
	return nil, false
}

// Rotate generates a new active key and demotes the current one to the
// verify-only previous set (capped). Call on a schedule; publish the new JWKS
// before relying parties need it.
func (ks *KeySet) Rotate(bits int) error {
	nk, err := newSigningKey(bits)
	if err != nil {
		return err
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.active != nil {
		ks.previous = append([]*signingKey{ks.active}, ks.previous...)
		if len(ks.previous) > ks.maxPrevious {
			ks.previous = ks.previous[:ks.maxPrevious]
		}
	}
	ks.active = nk
	return nil
}

// jwk / jwks are the JSON Web Key (Set) shapes published at /oauth/jwks.
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

func rsaToJWK(kid string, pub *rsa.PublicKey) jwk {
	return jwk{
		Kty: "RSA", Use: "sig", Alg: "RS256", Kid: kid,
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// JWKS returns the JSON Web Key Set (active + previous public keys) to publish
// at the discovery jwks_uri.
func (ks *KeySet) JWKS() ([]byte, error) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	doc := jwksDoc{}
	if ks.active != nil {
		doc.Keys = append(doc.Keys, rsaToJWK(ks.active.kid, &ks.active.priv.PublicKey))
	}
	for _, k := range ks.previous {
		doc.Keys = append(doc.Keys, rsaToJWK(k.kid, &k.priv.PublicKey))
	}
	return json.Marshal(doc)
}

// parseJWKS parses a JWKS document into kid -> RSA public key. Used by the RP.
func parseJWKS(data []byte) (map[string]*rsa.PublicKey, error) {
	var doc jwksDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("oidc: parse jwks: %w", err)
	}
	out := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("oidc: jwk %q bad modulus: %w", k.Kid, err)
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("oidc: jwk %q bad exponent: %w", k.Kid, err)
		}
		out[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: int(new(big.Int).SetBytes(eb).Int64()),
		}
	}
	return out, nil
}
