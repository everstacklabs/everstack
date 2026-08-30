package catalogdistribution

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const ProtocolVersion = 1

var safeReleaseToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

// Bundle is the complete runtime catalog payload. Encoding byte fields as
// base64 keeps the release self-contained while preserving the existing YAML
// parser and schema as the runtime authority.
type Bundle struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	Models        []byte `json:"models"`
	Providers     []byte `json:"providers"`
	Changelog     []byte `json:"changelog"`
}

// Channel is the small, signed pointer promoted after an immutable bundle is
// uploaded and verified. A channel update is the catalog equivalent of a
// CodePush deployment.
type Channel struct {
	SchemaVersion int    `json:"schema_version"`
	Channel       string `json:"channel"`
	Version       string `json:"version"`
	BundlePath    string `json:"bundle_path"`
	BundleSHA256  string `json:"bundle_sha256"`
	BundleSize    int64  `json:"bundle_size"`
	PublishedAt   string `json:"published_at"`
	PublicKeyID   string `json:"public_key_id"`
	Signature     string `json:"signature"`
}

type channelPayload struct {
	SchemaVersion int    `json:"schema_version"`
	Channel       string `json:"channel"`
	Version       string `json:"version"`
	BundlePath    string `json:"bundle_path"`
	BundleSHA256  string `json:"bundle_sha256"`
	BundleSize    int64  `json:"bundle_size"`
	PublishedAt   string `json:"published_at"`
	PublicKeyID   string `json:"public_key_id"`
}

func BuildBundle(version string, models, providers, changelog []byte) ([]byte, string, error) {
	if err := validateReleaseToken("version", version); err != nil {
		return nil, "", err
	}
	if err := validateSemanticVersion(version); err != nil {
		return nil, "", err
	}
	if len(models) == 0 || len(providers) == 0 {
		return nil, "", fmt.Errorf("catalog bundle requires models and providers")
	}
	if err := ValidateCatalogDocuments(models, providers); err != nil {
		return nil, "", err
	}

	bundle := Bundle{
		SchemaVersion: ProtocolVersion,
		Version:       version,
		Models:        models,
		Providers:     providers,
		Changelog:     changelog,
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, "", fmt.Errorf("encode catalog bundle: %w", err)
	}
	if len(data) > MaxBundleDocumentSize {
		return nil, "", fmt.Errorf("catalog bundle size %d exceeds protocol limit %d", len(data), MaxBundleDocumentSize)
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), nil
}

func SignChannel(privateKey ed25519.PrivateKey, channel Channel) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid catalog signing private key size: %d", len(privateKey))
	}

	channel.SchemaVersion = ProtocolVersion
	channel.PublicKeyID = PublicKeyID(privateKey.Public().(ed25519.PublicKey))
	channel.Signature = ""
	if err := validateChannel(channel); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(channel.signedPayload())
	if err != nil {
		return nil, fmt.Errorf("encode catalog channel payload: %w", err)
	}
	channel.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))

	data, err := json.Marshal(channel)
	if err != nil {
		return nil, fmt.Errorf("encode signed catalog channel: %w", err)
	}
	return data, nil
}

func VerifyChannel(publicKeys map[string]ed25519.PublicKey, data []byte, expectedChannel string) (*Channel, error) {
	channel, err := parseChannel(data, expectedChannel)
	if err != nil {
		return nil, err
	}

	publicKey, ok := publicKeys[channel.PublicKeyID]
	if !ok {
		return nil, fmt.Errorf("unknown catalog signing key ID %q", channel.PublicKeyID)
	}
	signature, err := base64.StdEncoding.DecodeString(channel.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode catalog channel signature: %w", err)
	}
	payload, err := json.Marshal(channel.signedPayload())
	if err != nil {
		return nil, fmt.Errorf("encode catalog channel payload: %w", err)
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return nil, fmt.Errorf("invalid catalog channel signature")
	}

	return channel, nil
}

// parseChannel validates the signed document structure before its signature is
// authenticated by VerifyChannel. It is deliberately not exported so callers
// cannot accidentally treat structural validation as authenticity.
func parseChannel(data []byte, expectedChannel string) (*Channel, error) {
	var channel Channel
	if err := json.Unmarshal(data, &channel); err != nil {
		return nil, fmt.Errorf("decode catalog channel: %w", err)
	}
	if err := validateChannel(channel); err != nil {
		return nil, err
	}
	if channel.Channel != expectedChannel {
		return nil, fmt.Errorf("catalog channel is %q, want %q", channel.Channel, expectedChannel)
	}
	if strings.TrimSpace(channel.Signature) == "" {
		return nil, fmt.Errorf("catalog channel signature is required")
	}
	return &channel, nil
}

func PublicKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return base64.RawURLEncoding.EncodeToString(digest[:8])
}

func (c Channel) signedPayload() channelPayload {
	return channelPayload{
		SchemaVersion: c.SchemaVersion,
		Channel:       c.Channel,
		Version:       c.Version,
		BundlePath:    c.BundlePath,
		BundleSHA256:  c.BundleSHA256,
		BundleSize:    c.BundleSize,
		PublishedAt:   c.PublishedAt,
		PublicKeyID:   c.PublicKeyID,
	}
}

func validateChannel(channel Channel) error {
	if channel.SchemaVersion != ProtocolVersion {
		return fmt.Errorf("unsupported catalog channel schema version %d", channel.SchemaVersion)
	}
	if err := validateReleaseToken("channel", channel.Channel); err != nil {
		return err
	}
	if err := validateReleaseToken("version", channel.Version); err != nil {
		return err
	}
	if err := validateSemanticVersion(channel.Version); err != nil {
		return err
	}
	if channel.BundleSize <= 0 {
		return fmt.Errorf("catalog bundle size must be positive")
	}
	if len(channel.BundleSHA256) != sha256.Size*2 {
		return fmt.Errorf("catalog bundle SHA-256 must be 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(channel.BundleSHA256); err != nil {
		return fmt.Errorf("catalog bundle SHA-256 is invalid: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, channel.PublishedAt); err != nil {
		return fmt.Errorf("catalog published_at must be RFC3339: %w", err)
	}
	if channel.PublicKeyID == "" {
		return fmt.Errorf("catalog channel public key ID is required")
	}
	if err := validateBundlePath(channel.BundlePath, channel.Version); err != nil {
		return err
	}
	return nil
}

func validateBundlePath(bundlePath, version string) error {
	if bundlePath == "" || strings.HasPrefix(bundlePath, "/") || path.Clean(bundlePath) != bundlePath {
		return fmt.Errorf("catalog bundle path must be a clean relative path")
	}
	want := path.Join("releases", version, "catalog.bundle.json")
	if bundlePath != want {
		return fmt.Errorf("catalog bundle path %q does not match version %q", bundlePath, version)
	}
	return nil
}

func validateReleaseToken(name, value string) error {
	if !safeReleaseToken.MatchString(value) {
		return fmt.Errorf("catalog %s %q contains unsupported characters", name, value)
	}
	return nil
}
