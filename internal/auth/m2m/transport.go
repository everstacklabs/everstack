package m2m

import (
	"net/http"
	"time"
)

// Transport wraps an http.RoundTripper to add M2M authentication.
// It automatically obtains and refreshes tokens using the TokenProvider.
type Transport struct {
	// Base is the underlying transport. If nil, http.DefaultTransport is used.
	Base http.RoundTripper

	// Provider is the token provider for obtaining access tokens.
	Provider TokenProvider
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Get token from provider
	token, err := t.Provider.GetToken(req.Context())
	if err != nil {
		return nil, err
	}

	// Clone request to avoid mutating the original
	reqClone := req.Clone(req.Context())
	reqClone.Header.Set("Authorization", t.Provider.TokenType()+" "+token)

	// Use base transport or default
	transport := t.Base
	if transport == nil {
		transport = http.DefaultTransport
	}

	return transport.RoundTrip(reqClone)
}

// NewHTTPClient creates an HTTP client with M2M authentication.
func NewHTTPClient(provider TokenProvider, timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &Transport{
			Base:     http.DefaultTransport,
			Provider: provider,
		},
	}
}

// NewHTTPClientFromConfig creates an HTTP client with M2M authentication from config.
func NewHTTPClientFromConfig(config *Config, clientName string, timeout time.Duration) (*http.Client, error) {
	provider, err := NewTokenProvider(config, clientName)
	if err != nil {
		return nil, err
	}
	return NewHTTPClient(provider, timeout), nil
}
