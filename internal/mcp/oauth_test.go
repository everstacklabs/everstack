package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestBuildWellKnownCandidates_PathAware(t *testing.T) {
	t.Parallel()

	got := buildWellKnownCandidates("https://mcp.supabase.com/mcp/", "oauth-protected-resource")
	want := []string{
		"https://mcp.supabase.com/.well-known/oauth-protected-resource/mcp",
		"https://mcp.supabase.com/.well-known/oauth-protected-resource",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildWellKnownCandidates() = %#v, want %#v", got, want)
	}
}

func TestBuildWellKnownCandidates_IssuerPathAware(t *testing.T) {
	t.Parallel()

	got := buildWellKnownCandidates("https://api.supabase.com/auth/v1", "oauth-authorization-server")
	want := []string{
		"https://api.supabase.com/.well-known/oauth-authorization-server/auth/v1",
		"https://api.supabase.com/.well-known/oauth-authorization-server",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildWellKnownCandidates() = %#v, want %#v", got, want)
	}
}

func TestDiscoverOAuth_UsesAuthorizationServerPathAwareMetadata(t *testing.T) {
	t.Parallel()

	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]interface{}{
			"resource":              serverURL + "/mcp",
			"authorization_servers": []string{serverURL + "/auth/v1"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth/v1", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]interface{}{
			"authorization_endpoint": serverURL + "/auth/v1/authorize",
			"token_endpoint":         serverURL + "/auth/v1/token",
			"registration_endpoint":  serverURL + "/auth/v1/register",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	serverURL = server.URL

	discovery, err := DiscoverOAuth(context.Background(), serverURL+"/mcp")
	if err != nil {
		t.Fatalf("DiscoverOAuth() error = %v", err)
	}
	if discovery.AuthorizationEndpoint != serverURL+"/auth/v1/authorize" {
		t.Fatalf("authorization endpoint = %q, want %q", discovery.AuthorizationEndpoint, serverURL+"/auth/v1/authorize")
	}
	if discovery.TokenEndpoint != serverURL+"/auth/v1/token" {
		t.Fatalf("token endpoint = %q, want %q", discovery.TokenEndpoint, serverURL+"/auth/v1/token")
	}
}

func TestDiscoverOAuth_FallsBackToOriginWellKnownMetadata(t *testing.T) {
	t.Parallel()

	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]interface{}{
			"resource":              serverURL + "/mcp",
			"authorization_servers": []string{serverURL},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]interface{}{
			"authorization_endpoint": serverURL + "/oauth/authorize",
			"token_endpoint":         serverURL + "/oauth/token",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	serverURL = server.URL

	discovery, err := DiscoverOAuth(context.Background(), serverURL+"/mcp")
	if err != nil {
		t.Fatalf("DiscoverOAuth() error = %v", err)
	}
	if discovery.AuthorizationEndpoint != serverURL+"/oauth/authorize" {
		t.Fatalf("authorization endpoint = %q, want %q", discovery.AuthorizationEndpoint, serverURL+"/oauth/authorize")
	}
	if discovery.TokenEndpoint != serverURL+"/oauth/token" {
		t.Fatalf("token endpoint = %q, want %q", discovery.TokenEndpoint, serverURL+"/oauth/token")
	}
}

func TestExchangeCode_IncludesClientSecretWhenProvided(t *testing.T) {
	t.Parallel()

	var (
		gotGrantType    string
		gotClientSecret string
		gotParseErr     error
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotParseErr = r.ParseForm()
		gotGrantType = r.FormValue("grant_type")
		gotClientSecret = r.FormValue("client_secret")
		writeTestJSON(w, map[string]interface{}{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	tokens, err := ExchangeCode(context.Background(), server.URL, "code-123", "verifier-123", "https://callback", "client-123", "secret-123")
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}
	if gotParseErr != nil {
		t.Fatalf("ParseForm() error = %v", gotParseErr)
	}
	if gotGrantType != "authorization_code" {
		t.Fatalf("grant_type = %q, want %q", gotGrantType, "authorization_code")
	}
	if gotClientSecret != "secret-123" {
		t.Fatalf("client_secret = %q, want %q", gotClientSecret, "secret-123")
	}
	if tokens.AccessToken != "access-token" {
		t.Fatalf("access_token = %q, want %q", tokens.AccessToken, "access-token")
	}
	if tokens.RefreshToken != "refresh-token" {
		t.Fatalf("refresh_token = %q, want %q", tokens.RefreshToken, "refresh-token")
	}
}

func TestRefreshAccessToken_OmitsClientSecretWhenEmpty(t *testing.T) {
	t.Parallel()

	var (
		gotGrantType    string
		gotClientSecret string
		gotParseErr     error
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotParseErr = r.ParseForm()
		gotGrantType = r.FormValue("grant_type")
		gotClientSecret = r.FormValue("client_secret")
		writeTestJSON(w, map[string]interface{}{
			"access_token":  "access-token-2",
			"refresh_token": "refresh-token-2",
			"token_type":    "Bearer",
		})
	}))
	defer server.Close()

	tokens, err := RefreshAccessToken(context.Background(), server.URL, "refresh-123", "client-123", "")
	if err != nil {
		t.Fatalf("RefreshAccessToken() error = %v", err)
	}
	if gotParseErr != nil {
		t.Fatalf("ParseForm() error = %v", gotParseErr)
	}
	if gotGrantType != "refresh_token" {
		t.Fatalf("grant_type = %q, want %q", gotGrantType, "refresh_token")
	}
	if gotClientSecret != "" {
		t.Fatalf("client_secret = %q, want empty", gotClientSecret)
	}
	if tokens.AccessToken != "access-token-2" {
		t.Fatalf("access_token = %q, want %q", tokens.AccessToken, "access-token-2")
	}
}

func TestExchangeCode_AcceptsHTTP201(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"access_token":"access-201","refresh_token":"refresh-201","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()

	tokens, err := ExchangeCode(context.Background(), server.URL, "code-201", "verifier-201", "https://callback", "client-201", "secret-201")
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}
	if tokens.AccessToken != "access-201" {
		t.Fatalf("access_token = %q, want %q", tokens.AccessToken, "access-201")
	}
	if tokens.RefreshToken != "refresh-201" {
		t.Fatalf("refresh_token = %q, want %q", tokens.RefreshToken, "refresh-201")
	}
}

func writeTestJSON(w http.ResponseWriter, payload map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}
