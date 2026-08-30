package apikey

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strings"
	"sync"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/spf13/viper"
)

var (
	secretOnce sync.Once
	secretVal  string
)

func loadSecret() {
	// Prefer config (server.yaml): server.security.api_key_hash_secret
	secretVal = strings.TrimSpace(viper.GetString("server.security.api_key_hash_secret"))
	if secretVal == "" {
		secretVal = os.Getenv("EVS_API_KEY_HASH_SECRET")
	}
}

// SecretPresent reports whether a stable HMAC secret is configured.
func SecretPresent() bool {
	secretOnce.Do(loadSecret)
	return secretVal != ""
}

// GetSecret returns the configured API key hash secret
func GetSecret() string {
	secretOnce.Do(loadSecret)
	return secretVal
}

// Hash computes HMAC-SHA256(apiKey) using the configured global secret and returns base64url (no padding).
// The second return value is false if no secret is configured; callers may fallback to legacy hashing.
func Hash(apiKey string) (string, bool) {
	secretOnce.Do(loadSecret)
	if secretVal == "" || apiKey == "" {
		return "", false
	}
	return HashWithSecret(apiKey, secretVal)
}

// HashWithSecret computes HMAC-SHA256(apiKey) using the given secret.
// Returns base64url (no padding). Returns ("", false) if either argument is empty.
func HashWithSecret(apiKey, secret string) (string, bool) {
	if secret == "" || apiKey == "" {
		return "", false
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(apiKey))
	sum := h.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sum), true
}

// HashFromContext checks the context for a per-tenant HMAC secret and uses it
// if present; otherwise falls back to the global secret.
// This allows multi-tenant deployments to use per-instance API key secrets
// while single-tenant deployments continue using the global config.
func HashFromContext(ctx context.Context, apiKey string) (string, bool) {
	if secret := contextkeys.APIKeyHashSecretFromContext(ctx); secret != "" {
		return HashWithSecret(apiKey, secret)
	}
	return Hash(apiKey)
}
