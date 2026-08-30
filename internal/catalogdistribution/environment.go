package catalogdistribution

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const requireSignatureEnvironmentVariable = "EVS_CATALOG_REQUIRE_SIGNATURE"

// TrustConfig is the gateway-side trust policy for a catalog release channel.
// PublicKeys supports a comma-separated rotation set; PublicKey is the
// convenient singular form.
type TrustConfig struct {
	Channel          string
	PublicKey        string
	PublicKeys       string
	RequireSignature bool
}

// NewClientFromEnvironment creates a signed distribution client. Signature
// verification is secure by default; legacy unsigned mode must be selected
// explicitly with EVS_CATALOG_REQUIRE_SIGNATURE=false.
func NewClientFromEnvironment(baseURL string, httpClient *http.Client) (*Client, error) {
	requireSignature := true
	if value := strings.TrimSpace(os.Getenv(requireSignatureEnvironmentVariable)); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", requireSignatureEnvironmentVariable, err)
		}
		requireSignature = parsed
	}
	return NewClientFromTrustConfig(baseURL, httpClient, TrustConfig{
		Channel:          ChannelFromEnvironment(),
		PublicKey:        os.Getenv("EVS_CATALOG_PUBLIC_KEY"),
		PublicKeys:       os.Getenv("EVS_CATALOG_PUBLIC_KEYS"),
		RequireSignature: requireSignature,
	})
}

// NewClientFromTrustConfig creates the signed distribution client described
// by the fully merged gateway configuration. A nil client means the caller
// explicitly allows the legacy unsigned file protocol.
func NewClientFromTrustConfig(baseURL string, httpClient *http.Client, config TrustConfig) (*Client, error) {
	publicKeys, err := ParsePublicKeys(config.PublicKeys, config.PublicKey)
	if err != nil {
		return nil, err
	}
	if len(publicKeys) == 0 {
		if config.RequireSignature {
			return nil, fmt.Errorf("%s is true but no catalog public key is configured", requireSignatureEnvironmentVariable)
		}
		return nil, nil
	}
	return NewClient(ClientConfig{
		BaseURL:    baseURL,
		Channel:    channelOrDefault(config.Channel),
		PublicKeys: publicKeys,
		HTTPClient: httpClient,
	})
}

func channelOrDefault(channel string) string {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return "stable"
	}
	return channel
}
