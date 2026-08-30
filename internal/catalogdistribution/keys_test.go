package catalogdistribution

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func TestPublicKeysFromEnvironmentSupportsRotation(t *testing.T) {
	firstPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secondPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", base64.StdEncoding.EncodeToString(firstPublic)+","+base64.StdEncoding.EncodeToString(secondPublic))
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "")

	keys, err := PublicKeysFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("len(keys) = %d, want 2", len(keys))
	}
}

func TestNewClientFromEnvironmentRequiresTrustAnchorWhenConfigured(t *testing.T) {
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", "")
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "")
	t.Setenv(requireSignatureEnvironmentVariable, "true")

	client, err := NewClientFromEnvironment("https://catalog.example.test/v1", nil)
	if client != nil || err == nil || !strings.Contains(err.Error(), "no catalog public key") {
		t.Fatalf("NewClientFromEnvironment() = (%v, %v)", client, err)
	}
}

func TestNewClientFromEnvironmentAllowsLegacyMigrationMode(t *testing.T) {
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", "")
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "")
	t.Setenv(requireSignatureEnvironmentVariable, "false")

	client, err := NewClientFromEnvironment("https://legacy.example.test", nil)
	if client != nil || err != nil {
		t.Fatalf("NewClientFromEnvironment() = (%v, %v)", client, err)
	}
}

func TestOfficialDistributionRejectsMissingTrustAnchor(t *testing.T) {
	client, err := NewClientFromTrustConfig("https://catalog.everstack.ai/v1", nil, TrustConfig{RequireSignature: true})
	if client != nil || err == nil || !strings.Contains(err.Error(), "no catalog public key") {
		t.Fatalf("NewClientFromTrustConfig() = (%v, %v), want missing trust anchor", client, err)
	}
}

func TestNewClientFromEnvironmentRejectsInvalidSignatureRequirement(t *testing.T) {
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", "")
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "")
	t.Setenv(requireSignatureEnvironmentVariable, "tru")

	client, err := NewClientFromEnvironment("https://legacy.example.test", nil)
	if client != nil || err == nil || !strings.Contains(err.Error(), requireSignatureEnvironmentVariable) {
		t.Fatalf("NewClientFromEnvironment() = (%v, %v)", client, err)
	}
}
