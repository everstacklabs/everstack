package middleware

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/everstacklabs/everstack/internal/cqrs"
	licpkg "github.com/everstacklabs/everstack/internal/license"
	querypkg "github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/query/handlers/license"
)

// The embedded key must parse and must be the key the prod license service
// publishes (kid = raw base64 of the first 8 bytes of its SHA-256).
func TestVendorLicensePublicKeyParses(t *testing.T) {
	if _, err := licpkg.NewVerifier(vendorLicensePublicKey); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(vendorLicensePublicKey)
	sum := sha256.Sum256(raw)
	if kid := base64.RawStdEncoding.EncodeToString(sum[:8]); kid != "PsY8uqqhVh8" {
		t.Fatalf("kid = %s, want PsY8uqqhVh8 (license.everstack.ai JWKS)", kid)
	}
	if buildPinnedVerifier("") == nil || buildPinnedVerifier("not base64!") == nil {
		t.Fatal("pinned verifier must never be nil: an unset or invalid override falls back to the vendor key")
	}
}

type storedPayloadBus struct{ payload []byte }

func (b storedPayloadBus) Execute(context.Context, querypkg.Query) (interface{}, error) {
	return &license.InstanceCredentials{InstanceID: "inst-1", RefreshToken: "rt", SignedPayload: b.payload}, nil
}
func (storedPayloadBus) RegisterHandler(querypkg.QueryHandler) {}

// forgedLicense is what an operator with write access to their own gateway
// database could store: their own public key plus an enterprise license JWT
// they signed with it.
func forgedLicense(t *testing.T) (payload []byte, pub ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claims := licpkg.Claims{
		Tier: "enterprise",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "everstack-license",
			Audience:  jwt.ClaimStrings{"everstack-gateway"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * 365 * 24 * time.Hour)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(storedJWTPayload{LicenseJWT: signed, LicensePublicKey: base64.StdEncoding.EncodeToString(pub)})
	return payload, pub
}

func TestForgedLicenseInDatabaseIsNotTrusted(t *testing.T) {
	payload, _ := forgedLicense(t)
	l := NewLicenseEnforcer(&cqrs.System{QueryBus: storedPayloadBus{payload: payload}})
	l.loadJWTFromDB()

	if st := l.GetCached(); st != nil {
		t.Fatalf("forged license accepted: %+v", st)
	}
	if !l.IsKeyPinned() {
		t.Fatal("loading a stored license must pin the vendor key, not adopt the stored one")
	}
}

// Control: the same stored payload IS accepted by a verifier for the forger's
// key, so the test above fails for the right reason (the trust anchor), not
// because the payload is malformed.
func TestForgedLicenseControl(t *testing.T) {
	payload, pub := forgedLicense(t)
	l := NewLicenseEnforcer(&cqrs.System{QueryBus: storedPayloadBus{payload: payload}})
	v, err := licpkg.NewVerifier(base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	l.verifier, l.pinned = v, true
	l.mu.Unlock()
	l.loadJWTFromDB()
	if st := l.GetCached(); st == nil || st.Tier != "enterprise" {
		t.Fatalf("control: state = %+v, want the forged enterprise license under the forger's key", st)
	}
}
