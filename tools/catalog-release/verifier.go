package main

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

const publicVerificationInterval = 2 * time.Second

func verifyPublicRelease(
	ctx context.Context,
	baseURL string,
	channel string,
	version string,
	publicKey ed25519.PublicKey,
	timeout time.Duration,
) error {
	client, err := catalogdistribution.NewClient(catalogdistribution.ClientConfig{
		BaseURL:    baseURL,
		Channel:    channel,
		PublicKeys: map[string]ed25519.PublicKey{catalogdistribution.PublicKeyID(publicKey): publicKey},
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: releaseHeaderTransport{
				base: http.DefaultTransport,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("configure public catalog verification: %w", err)
	}

	verificationContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		bundle, fetchErr := client.Fetch(verificationContext)
		if fetchErr == nil && bundle.Version == version {
			return nil
		}
		if fetchErr != nil {
			lastErr = fetchErr
		} else {
			lastErr = fmt.Errorf("public channel serves catalog version %q, want %q", bundle.Version, version)
		}

		timer := time.NewTimer(publicVerificationInterval)
		select {
		case <-verificationContext.Done():
			timer.Stop()
			return fmt.Errorf("verify public catalog release: %w", lastErr)
		case <-timer.C:
		}
	}
}

type releaseHeaderTransport struct {
	base http.RoundTripper
}

func (transport releaseHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return response, nil
	}
	if strings.TrimSpace(response.Header.Get("ETag")) == "" {
		response.Body.Close()
		return nil, fmt.Errorf("public catalog response %s has no ETag", request.URL.Path)
	}

	cacheControl := response.Header.Get("Cache-Control")
	if strings.Contains(request.URL.Path, "/channels/") {
		if !strings.Contains(cacheControl, "max-age=30") || !strings.Contains(cacheControl, "stale-if-error") {
			response.Body.Close()
			return nil, fmt.Errorf("public catalog channel has invalid Cache-Control %q", cacheControl)
		}
	} else if strings.Contains(request.URL.Path, "/releases/") {
		if !strings.Contains(cacheControl, "immutable") || !strings.Contains(cacheControl, "max-age=31536000") {
			response.Body.Close()
			return nil, fmt.Errorf("public catalog bundle has invalid Cache-Control %q", cacheControl)
		}
	}
	return response, nil
}
