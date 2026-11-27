package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// serverCredentials builds gRPC server credentials from the FC_TLS_* env vars.
// Returns (creds, nil) when mTLS is enabled, (nil, nil) when disabled.
func serverCredentials() (grpc.ServerOption, error) {
	cert := os.Getenv("FC_TLS_CERT")
	key := os.Getenv("FC_TLS_KEY")
	clientCA := os.Getenv("FC_TLS_CLIENT_CA")

	if cert == "" && key == "" && clientCA == "" {
		return nil, nil
	}
	if cert == "" || key == "" {
		return nil, errors.New("tls: FC_TLS_CERT and FC_TLS_KEY must both be set")
	}

	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("tls: load server keypair: %w", err)
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
	}

	if clientCA != "" {
		caPEM, err := os.ReadFile(clientCA)
		if err != nil {
			return nil, fmt.Errorf("tls: read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("tls: no valid certs in client CA bundle")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return grpc.Creds(credentials.NewTLS(cfg)), nil
}
