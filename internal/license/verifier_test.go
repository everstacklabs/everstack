package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// mintLicenseJWT signs a license JWT with the given key and expiry offset.
func mintLicenseJWT(t *testing.T, priv ed25519.PrivateKey, issuer, audience string, expiresIn time.Duration) string {
	t.Helper()
	claims := &Claims{
		TenantID: "org-1",
		Tier:     "pro",
		Status:   "active",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   "inst-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

func newTestVerifier(t *testing.T) (*Verifier, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	v, err := NewVerifier(base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return v, priv
}

func TestVerifyRejectsExpired(t *testing.T) {
	v, priv := newTestVerifier(t)
	expired := mintLicenseJWT(t, priv, "everstack-license", "everstack-gateway", -24*time.Hour)

	if _, err := v.Verify(expired); err == nil {
		t.Fatal("Verify accepted an expired JWT; strict path must reject it")
	}
}

func TestVerifyAllowExpiredAcceptsAuthenticExpired(t *testing.T) {
	v, priv := newTestVerifier(t)
	expired := mintLicenseJWT(t, priv, "everstack-license", "everstack-gateway", -24*time.Hour)

	claims, err := v.VerifyAllowExpired(expired)
	if err != nil {
		t.Fatalf("VerifyAllowExpired rejected an authentic expired JWT: %v", err)
	}
	if claims.Tier != "pro" || claims.Status != "active" {
		t.Fatalf("claims not preserved: %+v", claims)
	}
	if !claims.IsExpired() {
		t.Fatal("expected claims to report expired")
	}
}

func TestVerifyAllowExpiredStillChecksSignature(t *testing.T) {
	v, _ := newTestVerifier(t)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	forged := mintLicenseJWT(t, otherPriv, "everstack-license", "everstack-gateway", -time.Hour)

	if _, err := v.VerifyAllowExpired(forged); err == nil {
		t.Fatal("VerifyAllowExpired accepted a JWT signed by the wrong key")
	}
}

func TestVerifyAllowExpiredStillChecksIssuerAndAudience(t *testing.T) {
	v, priv := newTestVerifier(t)

	wrongIssuer := mintLicenseJWT(t, priv, "not-everstack", "everstack-gateway", -time.Hour)
	if _, err := v.VerifyAllowExpired(wrongIssuer); err == nil {
		t.Fatal("VerifyAllowExpired accepted a JWT with the wrong issuer")
	}

	wrongAudience := mintLicenseJWT(t, priv, "everstack-license", "someone-else", -time.Hour)
	if _, err := v.VerifyAllowExpired(wrongAudience); err == nil {
		t.Fatal("VerifyAllowExpired accepted a JWT with the wrong audience")
	}
}

func TestVerifyAcceptsValid(t *testing.T) {
	v, priv := newTestVerifier(t)
	valid := mintLicenseJWT(t, priv, "everstack-license", "everstack-gateway", 24*time.Hour)

	claims, err := v.Verify(valid)
	if err != nil {
		t.Fatalf("Verify rejected a valid JWT: %v", err)
	}
	if claims.IsExpired() {
		t.Fatal("valid JWT reported expired")
	}
}

// mintLicenseJWTNoExp signs an otherwise valid license JWT with no exp claim.
func mintLicenseJWTNoExp(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	claims := &Claims{
		TenantID: "org-1",
		Tier:     "enterprise",
		Status:   "active",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   "everstack-license",
			Audience: jwt.ClaimStrings{"everstack-gateway"},
			Subject:  "inst-1",
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(priv)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

// EVS-292: a license JWT without exp must not be accepted as never-expiring.
func TestVerifyRejectsMissingExp(t *testing.T) {
	v, priv := newTestVerifier(t)
	immortal := mintLicenseJWTNoExp(t, priv)

	if _, err := v.Verify(immortal); err == nil {
		t.Fatal("Verify accepted a JWT with no exp claim; it would never expire")
	}
	if _, err := v.VerifyAllowExpired(immortal); err == nil {
		t.Fatal("VerifyAllowExpired accepted a JWT with no exp claim; grace has nothing to anchor on")
	}
}

// Guard for the compatibility promise: every JWT the license signer has ever
// issued carries exp, so a well-formed token keeps validating.
func TestVerifyStillAcceptsTokenWithExp(t *testing.T) {
	v, priv := newTestVerifier(t)
	ok := mintLicenseJWT(t, priv, "everstack-license", "everstack-gateway", time.Hour)
	if _, err := v.Verify(ok); err != nil {
		t.Fatalf("Verify rejected a valid JWT with exp: %v", err)
	}
}
