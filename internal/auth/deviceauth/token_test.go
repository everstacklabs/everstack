package deviceauth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestTokenManagerRoundTrip(t *testing.T) {
	t.Parallel()

	manager, err := NewTokenManager([]byte(strings.Repeat("a", 32)), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	want := Identity{
		UserID:           "user-1",
		Email:            "owner@example.com",
		OrganizationID:   "org-1",
		OrganizationSlug: "example-org",
		InstanceID:       "instance-a",
		ClientID:         "evs-cli",
	}
	token, err := manager.Issue(want)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	got, err := manager.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if *got != want {
		t.Fatalf("Verify() = %#v, want %#v", *got, want)
	}
}

func TestTokenManagerRejectsWrongKeyAndExpiredToken(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	issuer, err := NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager(trusted) error = %v", err)
	}
	issuer.now = func() time.Time { return issuedAt }
	token, err := issuer.Issue(Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		ClientID:       "evs-cli",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	wrongKey, err := NewTokenManager([]byte(strings.Repeat("b", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager(untrusted) error = %v", err)
	}
	wrongKey.now = func() time.Time { return issuedAt.Add(30 * time.Minute) }
	if _, err := wrongKey.Verify(token); err == nil {
		t.Fatal("Verify() with wrong key succeeded")
	}

	issuer.now = func() time.Time { return issuedAt.Add(2 * time.Hour) }
	if _, err := issuer.Verify(token); err == nil {
		t.Fatal("Verify() with expired token succeeded")
	}
}

func TestNewTokenManagerRejectsShortKey(t *testing.T) {
	t.Parallel()

	if _, err := NewTokenManager([]byte("short"), time.Hour); err == nil {
		t.Fatal("NewTokenManager() accepted a short key")
	}
}

func TestTokenManagerAcceptsLegacyCloudTokenWithoutIssuer(t *testing.T) {
	t.Parallel()

	manager, err := NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	now := time.Now().UTC()
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":               "user-1",
		"organization_id":   "org-1",
		"organization_slug": "example-org",
		"client_id":         "ewt",
		"aud":               TokenAudience,
		"iat":               now.Unix(),
		"exp":               now.Add(time.Hour).Unix(),
	})
	token, err := legacy.SignedString(manager.key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}

	identity, err := manager.Verify(token)
	if err != nil {
		t.Fatalf("Verify(legacy token) error = %v", err)
	}
	if identity.UserID != "user-1" || identity.OrganizationID != "org-1" {
		t.Fatalf("Verify(legacy token) = %#v", identity)
	}
}

func TestTokenManagerRejectsWrongIssuer(t *testing.T) {
	t.Parallel()

	manager, err := NewTokenManager([]byte(strings.Repeat("a", 32)), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	now := time.Now().UTC()
	tokenWithWrongIssuer := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":             "another-issuer",
		"sub":             "user-1",
		"organization_id": "org-1",
		"aud":             TokenAudience,
		"iat":             now.Unix(),
		"exp":             now.Add(time.Hour).Unix(),
	})
	token, err := tokenWithWrongIssuer.SignedString(manager.key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	if _, err := manager.Verify(token); err == nil {
		t.Fatal("Verify() accepted a token with the wrong issuer")
	}
}

func TestTokenManagerIssueWithTTLKeepsLegacyDefault(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, time.July, 23, 22, 0, 0, 0, time.UTC)
	manager, err := NewTokenManager([]byte(strings.Repeat("a", 32)), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	manager.now = func() time.Time { return issuedAt }
	identity := Identity{
		UserID:         "user-1",
		OrganizationID: "org-1",
		ClientID:       "evs-cli",
	}

	shortToken, err := manager.IssueWithTTL(identity, 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueWithTTL() error = %v", err)
	}
	legacyToken, err := manager.Issue(identity)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	manager.now = func() time.Time { return issuedAt.Add(16 * time.Minute) }
	if _, err := manager.Verify(shortToken); err == nil {
		t.Fatal("Verify(short token) succeeded after the requested TTL")
	}
	if _, err := manager.Verify(legacyToken); err != nil {
		t.Fatalf("Verify(legacy token) error after 16m = %v", err)
	}
}
