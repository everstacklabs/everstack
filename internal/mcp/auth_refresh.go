package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// authRefreshTransport wraps a Transport and handles automatic OAuth2 token
// refresh. Before each Send, it checks if the token is about to expire and
// refreshes if needed. On a 401 response, it tries one refresh and retries.
type authRefreshTransport struct {
	inner      Transport
	auth       *AuthConfig
	onUpdate   func(*AuthConfig)
	mu         sync.Mutex
	refreshing bool
}

// newAuthRefreshTransport wraps the given transport with automatic OAuth2 token
// refresh logic. The onUpdate callback is called whenever tokens are refreshed,
// allowing the caller to persist updated tokens.
func newAuthRefreshTransport(inner Transport, auth *AuthConfig, onUpdate func(*AuthConfig)) Transport {
	if auth == nil || auth.Type != AuthTypeOAuth2 {
		return inner
	}
	return &authRefreshTransport{
		inner:    inner,
		auth:     auth,
		onUpdate: onUpdate,
	}
}

func (t *authRefreshTransport) Send(ctx context.Context, req *JsonRpcRequest) (*JsonRpcResponse, error) {
	// Pre-flight: refresh if token is about to expire (within 30 seconds)
	if err := t.ensureFreshToken(ctx); err != nil {
		// Log but don't fail — the existing token might still work
		_ = err
	}

	resp, err := t.inner.Send(ctx, req)
	if err != nil {
		// Check if the error message indicates a 401
		if isUnauthorizedError(err) {
			if refreshErr := t.doRefresh(ctx); refreshErr == nil {
				// Retry with refreshed token
				return t.inner.Send(ctx, req)
			}
		}
		return nil, err
	}

	return resp, nil
}

func (t *authRefreshTransport) Close() error {
	return t.inner.Close()
}

// ensureFreshToken checks if the access token is about to expire and refreshes
// it proactively.
func (t *authRefreshTransport) ensureFreshToken(ctx context.Context) error {
	t.mu.Lock()
	needsRefresh := !t.auth.ExpiresAt.IsZero() && time.Until(t.auth.ExpiresAt) < 30*time.Second
	t.mu.Unlock()

	if !needsRefresh {
		return nil
	}
	return t.doRefresh(ctx)
}

// doRefresh performs the actual token refresh, ensuring only one concurrent
// refresh attempt.
func (t *authRefreshTransport) doRefresh(ctx context.Context) error {
	t.mu.Lock()
	if t.refreshing {
		t.mu.Unlock()
		return nil // Another goroutine is already refreshing
	}
	t.refreshing = true
	refreshToken := t.auth.RefreshToken
	tokenURL := t.auth.TokenURL
	clientID := t.auth.ClientID
	clientSecret := t.auth.ClientSecret
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.refreshing = false
		t.mu.Unlock()
	}()

	if refreshToken == "" || tokenURL == "" {
		return fmt.Errorf("mcp auth: no refresh token or token URL configured")
	}

	tokens, err := RefreshAccessToken(ctx, tokenURL, refreshToken, clientID, clientSecret)
	if err != nil {
		return fmt.Errorf("mcp auth: refresh failed: %w", err)
	}

	t.mu.Lock()
	t.auth.AccessToken = tokens.AccessToken
	if tokens.RefreshToken != "" {
		t.auth.RefreshToken = tokens.RefreshToken
	}
	if !tokens.ExpiresAt.IsZero() {
		t.auth.ExpiresAt = tokens.ExpiresAt
	}
	updatedAuth := *t.auth
	t.mu.Unlock()

	// Notify the callback to persist updated tokens
	if t.onUpdate != nil {
		t.onUpdate(&updatedAuth)
	}

	return nil
}

// isUnauthorizedError checks if an error message indicates a 401 response.
func isUnauthorizedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Match patterns like "mcp: server returned 401: ..."
	return len(msg) > 0 && (contains(msg, "401") || contains(msg, "Unauthorized"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
