package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// TransportPool manages a shared http.Transport for backend connections.
// This fixes the per-request transport creation in the old SandboxProxy.
type TransportPool struct {
	transport *http.Transport
}

// NewTransportPool creates a shared transport with connection pooling.
func NewTransportPool(mtls MTLSConfig) (*TransportPool, error) {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	if mtls.Enabled {
		tlsConfig, err := buildMTLSConfig(mtls)
		if err != nil {
			return nil, fmt.Errorf("gateway: mTLS config error: %w", err)
		}
		transport.TLSClientConfig = tlsConfig
	}

	return &TransportPool{transport: transport}, nil
}

// Transport returns the shared http.Transport.
func (p *TransportPool) Transport() *http.Transport {
	return p.transport
}

// Close closes idle connections.
func (p *TransportPool) Close() {
	p.transport.CloseIdleConnections()
}

// buildMTLSConfig creates a TLS config with client certificates for backend mTLS.
func buildMTLSConfig(cfg MTLSConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	if cfg.CAPath != "" {
		caPEM, err := os.ReadFile(cfg.CAPath)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}
		tlsConfig.RootCAs = pool
	}

	return tlsConfig, nil
}
