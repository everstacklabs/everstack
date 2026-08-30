package oauthflow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginCompletesAuthorizationCodePKCEThroughLoopback(t *testing.T) {
	t.Parallel()

	var (
		server          *httptest.Server
		mu              sync.Mutex
		storedChallenge string
	)
	handler := http.NewServeMux()
	handler.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/oauth/authorize",
			"token_endpoint":                   server.URL + "/oauth/token",
			"grant_types_supported":            []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	handler.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client_id") != ClientID ||
			r.URL.Query().Get("code_challenge_method") != "S256" {
			t.Errorf("unexpected authorization request: %s", r.URL.RawQuery)
			http.Error(w, "unexpected authorization request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		storedChallenge = r.URL.Query().Get("code_challenge")
		mu.Unlock()
		callback, err := url.Parse(r.URL.Query().Get("redirect_uri"))
		if err != nil {
			t.Errorf("parse redirect URI: %v", err)
			http.Error(w, "invalid redirect URI", http.StatusBadRequest)
			return
		}
		query := callback.Query()
		query.Set("code", "opaque-code")
		query.Set("state", r.URL.Query().Get("state"))
		callback.RawQuery = query.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	})
	handler.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
			http.Error(w, "invalid token form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "authorization_code" ||
			r.Form.Get("client_id") != ClientID ||
			r.Form.Get("code") != "opaque-code" {
			t.Errorf("unexpected token form: %v", r.Form)
			http.Error(w, "unexpected token form", http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
		mu.Lock()
		wantChallenge := storedChallenge
		mu.Unlock()
		if gotChallenge != wantChallenge {
			t.Errorf("PKCE challenge = %q, want %q", gotChallenge, wantChallenge)
			http.Error(w, "PKCE mismatch", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "signed-access-jwt",
			"refresh_token": "opaque-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    900,
			"scope":         Scope,
		})
	})
	server = httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tokens, err := Login(context.Background(), Options{
		APIURL: server.URL,
		OpenBrowser: func(target string) error {
			resp, err := server.Client().Get(target)
			if err != nil {
				return err
			}
			return resp.Body.Close()
		},
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if tokens.AccessToken != "signed-access-jwt" || tokens.RefreshToken != "opaque-refresh-token" {
		t.Fatalf("tokens = access:%q refresh:%q", tokens.AccessToken, tokens.RefreshToken)
	}
	remaining := time.Until(tokens.ExpiresAt)
	if remaining < 14*time.Minute || remaining > 15*time.Minute {
		t.Fatalf("token lifetime = %s, want about 15m", remaining)
	}
}

func TestLoginReturnsUnavailableWhenServerHasNoOAuthMetadata(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	_, err := Login(context.Background(), Options{
		APIURL: server.URL,
		OpenBrowser: func(string) error {
			t.Fatal("browser must not open when discovery is unavailable")
			return nil
		},
		HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Login() error = %v, want ErrUnavailable", err)
	}
}

func TestLoginTreatsLegacySPACatchAllAsUnavailable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Everstack</title>"))
	}))
	t.Cleanup(server.Close)
	_, err := Login(context.Background(), Options{
		APIURL: server.URL,
		OpenBrowser: func(string) error {
			t.Fatal("browser must not open for legacy metadata response")
			return nil
		},
		HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Login() error = %v, want ErrUnavailable", err)
	}
}

func TestRefreshRotatesOpaqueToken(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/oauth/authorize",
			"token_endpoint":                   server.URL + "/oauth/token",
			"grant_types_supported":            []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	handler.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse refresh form: %v", err)
			http.Error(w, "invalid refresh form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" ||
			r.Form.Get("client_id") != ClientID ||
			r.Form.Get("refresh_token") != "old-refresh-token" {
			t.Errorf("unexpected refresh form: %v", r.Form)
			http.Error(w, "unexpected refresh form", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-jwt",
			"refresh_token": "new-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    900,
			"scope":         Scope,
		})
	})
	server = httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tokens, err := Refresh(context.Background(), RefreshOptions{
		APIURL:       server.URL,
		RefreshToken: "old-refresh-token",
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if tokens.AccessToken != "new-access-jwt" || tokens.RefreshToken != "new-refresh-token" {
		t.Fatalf("tokens = access:%q refresh:%q", tokens.AccessToken, tokens.RefreshToken)
	}
}

func TestRefreshDoesNotFollowTokenEndpointRedirect(t *testing.T) {
	t.Parallel()

	var attackerCalls atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "stolen-access",
			"refresh_token": "stolen-refresh",
			"token_type":    "Bearer",
			"expires_in":    900,
		})
	}))
	t.Cleanup(attacker.Close)

	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/oauth/authorize",
			"token_endpoint":                   server.URL + "/oauth/token",
			"grant_types_supported":            []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	handler.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/collect", http.StatusTemporaryRedirect)
	})
	server = httptest.NewServer(handler)
	t.Cleanup(server.Close)

	if _, err := Refresh(context.Background(), RefreshOptions{
		APIURL:       server.URL,
		RefreshToken: "do-not-leak",
		HTTPClient:   server.Client(),
	}); err == nil {
		t.Fatal("Refresh() followed a token endpoint redirect")
	}
	if got := attackerCalls.Load(); got != 0 {
		t.Fatalf("attacker calls = %d, refresh token was forwarded", got)
	}
}

func TestRevokeSubmitsRefreshTokenWithoutFollowingRedirects(t *testing.T) {
	t.Parallel()

	var (
		mu            sync.Mutex
		gotToken      string
		attackerCalls atomic.Int32
	)
	attacker := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		attackerCalls.Add(1)
	}))
	t.Cleanup(attacker.Close)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/revoke" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse revoke form: %v", err)
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		mu.Lock()
		gotToken = r.Form.Get("token")
		mu.Unlock()
		http.Redirect(w, r, attacker.URL+"/collect", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)

	err := Revoke(context.Background(), server.URL, "refresh-to-revoke", server.Client())
	if err == nil {
		t.Fatal("Revoke() followed a revocation endpoint redirect")
	}
	mu.Lock()
	got := gotToken
	mu.Unlock()
	if got != "refresh-to-revoke" {
		t.Fatalf("token = %q, want refresh-to-revoke", got)
	}
	if got := attackerCalls.Load(); got != 0 {
		t.Fatalf("attacker calls = %d, refresh token was forwarded", got)
	}
}

func TestRevokeAcceptsSuccessfulRevocation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/revoke" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	if err := Revoke(context.Background(), server.URL, "refresh-to-revoke", server.Client()); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
}
