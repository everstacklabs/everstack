// Package redistls builds verified TLS configuration for Redis clients.
package redistls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// Config describes the trust material used for Redis and Sentinel.
type Config struct {
	Enabled    bool
	CAFile     string
	ServerName string
}

// Load returns nil when TLS is disabled and a verified TLS configuration when
// it is enabled. Production callers use an internal CA, so both the CA file
// and the stable service name are mandatory instead of falling back to the
// host trust store or discovered Sentinel addresses.
func Load(cfg Config) (*tls.Config, error) {
	if !cfg.Enabled {
		if strings.TrimSpace(cfg.CAFile) != "" || strings.TrimSpace(cfg.ServerName) != "" {
			return nil, fmt.Errorf("redis TLS CA file and server name require TLS to be enabled")
		}
		return nil, nil
	}

	caFile := strings.TrimSpace(cfg.CAFile)
	if caFile == "" {
		return nil, fmt.Errorf("redis TLS CA file is required")
	}
	serverName := strings.TrimSpace(cfg.ServerName)
	if serverName == "" {
		return nil, fmt.Errorf("redis TLS server name is required")
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read redis TLS CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("redis TLS CA file contains no certificates")
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: serverName,
	}, nil
}
