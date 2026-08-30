package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"

	"golang.org/x/crypto/acme/autocert"

	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
)

// Config holds the unified gateway configuration.
// It backward-compatibly absorbs the old PortExposureConfig fields.
type Config struct {
	// ListenAddr is the address the gateway listens on (e.g., ":8443").
	ListenAddr string `json:"listen_addr" mapstructure:"listen_addr"`

	// BaseDomain is the wildcard base domain for subdomain routing (e.g., "evs.run").
	BaseDomain string `json:"base_domain" mapstructure:"base_domain"`

	// MaxPortsPerSandbox limits how many ports a single sandbox can expose.
	MaxPortsPerSandbox int `json:"max_ports_per_sandbox" mapstructure:"max_ports_per_sandbox"`

	// TLS configures TLS for the gateway listener.
	TLS TLSConfig `json:"tls" mapstructure:"tls"`

	// CORS configures CORS headers on proxied responses.
	CORS CORSConfig `json:"cors" mapstructure:"cors"`

	// RequestTimeoutSecs is the per-request timeout in seconds (default: 120).
	// WebSocket upgrades are exempt.
	RequestTimeoutSecs int `json:"request_timeout_seconds" mapstructure:"request_timeout_seconds"`

	// MaxRequestBodyMB is the maximum request body size in MB (default: 50).
	// WebSocket upgrades are exempt.
	MaxRequestBodyMB int `json:"max_request_body_mb" mapstructure:"max_request_body_mb"`

	// MTLS configures mutual TLS for backend connections.
	MTLS MTLSConfig `json:"mtls" mapstructure:"mtls"`

	// EnableSessionRouting enables {session}.session.{baseDomain} routing.
	EnableSessionRouting bool `json:"enable_session_routing" mapstructure:"enable_session_routing"`

	// RequirePreviewToken rejects unsigned subdomain preview access when a
	// preview signer is configured. Leave false for direct/local compatibility;
	// enable in cloud deployments that only want shareable signed preview URLs.
	RequirePreviewToken bool `json:"require_preview_token" mapstructure:"require_preview_token"`

	// PreviewSigner, when set, enables signed preview URL validation in
	// the proxy handler. Not serialized -- set programmatically at startup.
	// See internal/sandbox/previewtoken for signing/verification details.
	PreviewSigner *previewtoken.Signer `json:"-" mapstructure:"-"`
}

// TLSConfig configures TLS for the gateway.
type TLSConfig struct {
	Enabled     bool   `json:"enabled" mapstructure:"enabled"`
	CertPath    string `json:"cert_path" mapstructure:"cert_path"`
	KeyPath     string `json:"key_path" mapstructure:"key_path"`
	Autocert    bool   `json:"autocert" mapstructure:"autocert"`
	AutocertDir string `json:"autocert_dir" mapstructure:"autocert_dir"`
}

// Config builds a *tls.Config for the gateway.
//
// For file-based certs (CertPath/KeyPath), wires GetCertificate to a
// polling reloader so cert-manager Secret rotations get picked up
// without a pod restart. The reloader runs for the lifetime of `ctx`.
func (t *TLSConfig) Config(ctx context.Context, baseDomain string) (*tls.Config, *autocert.Manager, error) {
	if !t.Enabled {
		return nil, nil, nil
	}

	if t.Autocert {
		dir := t.AutocertDir
		if dir == "" {
			dir = "~/.everstack/autocert"
		}
		mgr := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(dir),
			HostPolicy: autocert.HostWhitelist(),
		}
		mgr.HostPolicy = func(_ context.Context, host string) error {
			if strings.HasSuffix(host, "."+baseDomain) || host == baseDomain {
				return nil
			}
			return fmt.Errorf("acme/autocert: host %q not allowed", host)
		}
		return mgr.TLSConfig(), mgr, nil
	}

	if t.CertPath == "" || t.KeyPath == "" {
		return nil, nil, fmt.Errorf("gateway: TLS enabled but cert_path or key_path is empty")
	}
	reloader, err := newCertReloader(ctx, t.CertPath, t.KeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("gateway: failed to load TLS cert/key: %w", err)
	}
	return &tls.Config{
		// GetCertificate beats Certificates for hot-reload — the TLS
		// stack calls it on every ClientHello, so a swapped keypair
		// is in effect on the very next handshake.
		GetCertificate: reloader.GetCertificate,
	}, nil, nil
}

// CORSConfig configures CORS for the gateway.
type CORSConfig struct {
	Enabled        *bool    `json:"enabled" mapstructure:"enabled"`
	AllowedOrigins []string `json:"allowed_origins" mapstructure:"allowed_origins"`
	AllowedMethods []string `json:"allowed_methods" mapstructure:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers" mapstructure:"allowed_headers"`
	MaxAgeSecs     int      `json:"max_age_seconds" mapstructure:"max_age_seconds"`
}

// IsEnabled returns whether CORS is enabled (default: true).
func (c *CORSConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// MTLSConfig configures mutual TLS for backend connections.
type MTLSConfig struct {
	Enabled  bool   `json:"enabled" mapstructure:"enabled"`
	CertPath string `json:"cert_path" mapstructure:"cert_path"`
	KeyPath  string `json:"key_path" mapstructure:"key_path"`
	CAPath   string `json:"ca_path" mapstructure:"ca_path"`
}

// DefaultConfig returns sensible defaults for the gateway.
func DefaultConfig() Config {
	return Config{
		ListenAddr:         ":8443",
		BaseDomain:         "everstack.localhost",
		MaxPortsPerSandbox: 5,
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			AllowedHeaders: []string{"*"},
			MaxAgeSecs:     3600,
		},
		RequestTimeoutSecs: 120,
		MaxRequestBodyMB:   50,
	}
}

// ApplyDefaults fills zero-value fields with defaults.
func (c *Config) ApplyDefaults() {
	if c.BaseDomain == "" {
		c.BaseDomain = "everstack.localhost"
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8443"
	}
	if c.RequestTimeoutSecs == 0 {
		c.RequestTimeoutSecs = 120
	}
	if c.MaxRequestBodyMB == 0 {
		c.MaxRequestBodyMB = 50
	}
	if c.CORS.MaxAgeSecs == 0 {
		c.CORS.MaxAgeSecs = 3600
	}
	if len(c.CORS.AllowedOrigins) == 0 {
		c.CORS.AllowedOrigins = []string{"*"}
	}
	if len(c.CORS.AllowedMethods) == 0 {
		c.CORS.AllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	}
}
