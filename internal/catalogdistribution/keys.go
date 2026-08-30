package catalogdistribution

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// PublicKeysFromEnvironment loads catalog verification keys. The plural form
// supports comma-separated key rotation, while the singular form remains a
// convenient first-deployment setting.
func PublicKeysFromEnvironment() (map[string]ed25519.PublicKey, error) {
	return ParsePublicKeys(
		os.Getenv("EVS_CATALOG_PUBLIC_KEYS"),
		os.Getenv("EVS_CATALOG_PUBLIC_KEY"),
	)
}

// ParsePublicKeys parses comma-separated or singular base64 Ed25519 public
// keys from layered runtime configuration.
func ParsePublicKeys(values ...string) (map[string]ed25519.PublicKey, error) {
	publicKeys := make(map[string]ed25519.PublicKey)
	for _, value := range values {
		for _, encoded := range strings.Split(value, ",") {
			encoded = strings.TrimSpace(encoded)
			if encoded == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, fmt.Errorf("decode catalog public key: %w", err)
			}
			if len(decoded) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("invalid catalog public key size: %d", len(decoded))
			}
			publicKey := ed25519.PublicKey(decoded)
			publicKeys[PublicKeyID(publicKey)] = publicKey
		}
	}
	return publicKeys, nil
}

func ChannelFromEnvironment() string {
	if channel := strings.TrimSpace(os.Getenv("EVS_CATALOG_CHANNEL")); channel != "" {
		return channel
	}
	return "stable"
}
