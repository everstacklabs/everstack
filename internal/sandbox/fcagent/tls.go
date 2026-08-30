package fcagent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// loadClientTLS builds client-side mTLS credentials for dialing agents.
func loadClientTLS(cfg TLSConfig) (credentials.TransportCredentials, error) {
	if cfg.ClientCert == "" || cfg.ClientKey == "" {
		return nil, errors.New("mTLS: ClientCert and ClientKey must both be set")
	}
	if cfg.ServerCA == "" {
		return nil, errors.New("mTLS: ServerCA must be set to verify agent certs")
	}

	pair, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("mTLS: load client keypair: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.ServerCA)
	if err != nil {
		return nil, fmt.Errorf("mTLS: read server CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("mTLS: no valid certs in server CA bundle")
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{pair},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
		ServerName:   cfg.ServerName,
	}), nil
}
