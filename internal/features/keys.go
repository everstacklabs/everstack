package features

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Baked-in edge distribution defaults.
// Users do not configure these — the poller auto-starts when EVS_MANIFEST_PUBLIC_KEY is set.
const (
	defaultEdgeURL      = "https://features.everstack.com"
	defaultPollInterval = 60 * time.Second
)

// DefaultEdgeURL returns the baked-in edge URL, overridable via EVS_FEATURES_EDGE_URL.
func DefaultEdgeURL() string {
	if v := os.Getenv("EVS_FEATURES_EDGE_URL"); v != "" {
		return v
	}
	return defaultEdgeURL
}

// DefaultPollInterval returns the baked-in poll interval, overridable via EVS_FEATURES_POLL_INTERVAL.
func DefaultPollInterval() time.Duration {
	if v := os.Getenv("EVS_FEATURES_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultPollInterval
}

// DefaultCacheDir returns the baked-in cache directory, overridable via EVS_FEATURES_CACHE_DIR.
func DefaultCacheDir() string {
	if v := os.Getenv("EVS_FEATURES_CACHE_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".everstack")
}

// LoadPublicKeysFromEnv loads manifest verification public keys from environment.
// Looks for EVS_MANIFEST_PUBLIC_KEY (base64-encoded Ed25519 public key).
// Returns a map of key ID -> public key for key rotation support.
func LoadPublicKeysFromEnv() map[string]ed25519.PublicKey {
	keys := make(map[string]ed25519.PublicKey)

	pubKeyB64 := os.Getenv("EVS_MANIFEST_PUBLIC_KEY")
	if pubKeyB64 == "" {
		return keys
	}

	pubBytes, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		logger.WithError(err).Warn("features: failed to decode EVS_MANIFEST_PUBLIC_KEY")
		return keys
	}

	if len(pubBytes) != ed25519.PublicKeySize {
		logger.Warnf("features: invalid EVS_MANIFEST_PUBLIC_KEY size: got %d, expected %d", len(pubBytes), ed25519.PublicKeySize)
		return keys
	}

	pub := ed25519.PublicKey(pubBytes)
	kid := publicKeyID(pub)
	keys[kid] = pub
	logger.WithFields("key_id", kid).Info("features: loaded manifest verification public key")

	return keys
}

// publicKeyID computes the key ID from a public key (matches signer logic)
func publicKeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawStdEncoding.EncodeToString(sum[:8])
}
