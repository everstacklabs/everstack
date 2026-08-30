package catalogdistribution

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultChannelDocumentLimit = 64 * 1024
	MaxBundleDocumentSize       = 8 * 1024 * 1024
)

type ClientConfig struct {
	BaseURL      string
	Channel      string
	PublicKeys   map[string]ed25519.PublicKey
	HTTPClient   *http.Client
	MaxBundleLen int64
}

// Client fetches and verifies immutable catalog releases through a small
// signed channel interface.
type Client struct {
	baseURL      *url.URL
	channel      string
	publicKeys   map[string]ed25519.PublicKey
	httpClient   *http.Client
	maxBundleLen int64
}

func NewClient(config ClientConfig) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimRight(config.BaseURL, "/") + "/")
	if err != nil {
		return nil, fmt.Errorf("parse catalog distribution URL: %w", err)
	}
	if (baseURL.Scheme != "https" && baseURL.Scheme != "http") || baseURL.Host == "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("catalog distribution URL must be an HTTP origin with an optional path")
	}
	if err := validateReleaseToken("channel", config.Channel); err != nil {
		return nil, err
	}
	if len(config.PublicKeys) == 0 {
		return nil, fmt.Errorf("catalog distribution requires at least one verification key")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.MaxBundleLen <= 0 {
		config.MaxBundleLen = MaxBundleDocumentSize
	}

	return &Client{
		baseURL:      baseURL,
		channel:      config.Channel,
		publicKeys:   config.PublicKeys,
		httpClient:   config.HTTPClient,
		maxBundleLen: config.MaxBundleLen,
	}, nil
}

func (c *Client) FetchVersion(ctx context.Context) (string, error) {
	channel, err := c.fetchChannel(ctx)
	if err != nil {
		return "", err
	}
	return channel.Version, nil
}

func (c *Client) Fetch(ctx context.Context) (*Bundle, error) {
	channel, err := c.fetchChannel(ctx)
	if err != nil {
		return nil, err
	}
	if channel.BundleSize > c.maxBundleLen {
		return nil, fmt.Errorf("catalog bundle size %d exceeds limit %d", channel.BundleSize, c.maxBundleLen)
	}

	bundleURL := c.baseURL.ResolveReference(&url.URL{Path: channel.BundlePath})
	bundleData, err := c.fetchDocument(ctx, bundleURL, c.maxBundleLen)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog bundle: %w", err)
	}
	if int64(len(bundleData)) != channel.BundleSize {
		return nil, fmt.Errorf("catalog bundle size is %d, want %d", len(bundleData), channel.BundleSize)
	}
	digest := sha256.Sum256(bundleData)
	if got := hex.EncodeToString(digest[:]); !strings.EqualFold(got, channel.BundleSHA256) {
		return nil, fmt.Errorf("catalog bundle SHA-256 mismatch")
	}

	var bundle Bundle
	if err := json.Unmarshal(bundleData, &bundle); err != nil {
		return nil, fmt.Errorf("decode catalog bundle: %w", err)
	}
	if bundle.SchemaVersion != ProtocolVersion {
		return nil, fmt.Errorf("unsupported catalog bundle schema version %d", bundle.SchemaVersion)
	}
	if bundle.Version != channel.Version {
		return nil, fmt.Errorf("catalog bundle version is %q, channel points to %q", bundle.Version, channel.Version)
	}
	if len(bundle.Models) == 0 || len(bundle.Providers) == 0 {
		return nil, fmt.Errorf("catalog bundle is incomplete")
	}
	if err := ValidateCatalogDocuments(bundle.Models, bundle.Providers); err != nil {
		return nil, fmt.Errorf("validate catalog bundle: %w", err)
	}
	return &bundle, nil
}

func (c *Client) fetchChannel(ctx context.Context) (*Channel, error) {
	channelURL := c.baseURL.ResolveReference(&url.URL{Path: "channels/" + c.channel + ".json"})
	data, err := c.fetchDocument(ctx, channelURL, defaultChannelDocumentLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog channel: %w", err)
	}
	return VerifyChannel(c.publicKeys, data, c.channel)
}

func (c *Client) fetchDocument(ctx context.Context, documentURL *url.URL, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, documentURL.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Everstack-Gateway/1.0")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog distribution returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("catalog document length %d exceeds limit %d", response.ContentLength, limit)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("catalog document exceeds limit %d", limit)
	}
	return data, nil
}
