package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthDiscovery holds the discovered OAuth endpoints for an MCP server.
type OAuthDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint,omitempty"`
	ResourceServer        string `json:"resource_server,omitempty"`
}

// OAuthTokens holds the tokens returned from a token exchange or refresh.
type OAuthTokens struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"-"`
}

// DiscoverOAuth probes an MCP server URL for OAuth 2.1 protected resource
// metadata. It follows the MCP spec:
//  1. Probe the URL — expect a 401 with WWW-Authenticate header
//  2. Fetch /.well-known/oauth-protected-resource from the resource server
//  3. Fetch /.well-known/oauth-authorization-server from the auth server
func DiscoverOAuth(ctx context.Context, mcpServerURL string) (*OAuthDiscovery, error) {
	if _, err := url.Parse(mcpServerURL); err != nil {
		return nil, fmt.Errorf("mcp oauth: invalid server URL: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Probe the MCP server URL for 401
	probeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, mcpServerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: create probe request: %w", err)
	}
	probeReq.Header.Set("Accept", "application/json")

	probeResp, err := client.Do(probeReq)
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: probe request: %w", err)
	}
	probeResp.Body.Close()

	// If we don't get a 401, the server may not require OAuth
	if probeResp.StatusCode != http.StatusUnauthorized {
		return nil, fmt.Errorf("mcp oauth: server returned %d (expected 401), may not require OAuth", probeResp.StatusCode)
	}

	// Build protected resource metadata URL candidates per RFC 8414.
	// For a URL like https://mcp.supabase.com/mcp:
	//   https://mcp.supabase.com/.well-known/oauth-protected-resource/mcp
	// Then fall back to:
	//   https://mcp.supabase.com/.well-known/oauth-protected-resource

	resourceMetaCandidates := buildWellKnownCandidates(mcpServerURL, "oauth-protected-resource")

	// Step 2: Fetch OAuth protected resource metadata (try path-aware first, then origin)
	var discovery *OAuthDiscovery
	for _, metaURL := range resourceMetaCandidates {
		d, fetchErr := fetchProtectedResourceMeta(ctx, client, metaURL)
		if fetchErr == nil {
			discovery = d
			break
		}
	}
	if discovery == nil {
		discovery = &OAuthDiscovery{}
	}

	// Step 3: Fetch authorization server metadata
	authServerURL := mcpServerURL
	if discovery.ResourceServer != "" {
		authServerURL = discovery.ResourceServer
	}

	// Build auth server metadata URL candidates from the auth server URL itself.
	// This is critical when the issuer has a path (e.g. https://host/auth/v1),
	// where the RFC 8414 path-aware URL is:
	//   https://host/.well-known/oauth-authorization-server/auth/v1
	authMetaCandidates := buildWellKnownCandidates(authServerURL, "oauth-authorization-server")
	if len(authMetaCandidates) == 0 {
		return nil, fmt.Errorf("mcp oauth: unable to build auth server metadata URL candidates from %q", authServerURL)
	}

	var authMeta *OAuthDiscovery
	var lastErr error
	for _, metaURL := range authMetaCandidates {
		m, fetchErr := fetchAuthServerMeta(ctx, client, metaURL)
		if fetchErr == nil {
			authMeta = m
			break
		}
		lastErr = fmt.Errorf("mcp oauth: fetch auth server metadata from %s: %w", metaURL, fetchErr)
	}
	if authMeta == nil {
		return nil, lastErr
	}

	if authMeta.AuthorizationEndpoint != "" {
		discovery.AuthorizationEndpoint = authMeta.AuthorizationEndpoint
	}
	if authMeta.TokenEndpoint != "" {
		discovery.TokenEndpoint = authMeta.TokenEndpoint
	}
	if authMeta.RegistrationEndpoint != "" {
		discovery.RegistrationEndpoint = authMeta.RegistrationEndpoint
	}

	if discovery.AuthorizationEndpoint == "" || discovery.TokenEndpoint == "" {
		return nil, fmt.Errorf("mcp oauth: discovery incomplete: authorization_endpoint=%q, token_endpoint=%q",
			discovery.AuthorizationEndpoint, discovery.TokenEndpoint)
	}

	return discovery, nil
}

func buildWellKnownCandidates(rawURL string, wellKnownName string) []string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		base := strings.TrimRight(rawURL, "/")
		if base == "" {
			return nil
		}
		return []string{base + "/.well-known/" + wellKnownName}
	}

	origin := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	pathSuffix := normalizePathSuffix(parsed.Path)

	candidates := make([]string, 0, 2)
	if pathSuffix != "" {
		candidates = append(candidates, origin+"/.well-known/"+wellKnownName+pathSuffix)
	}
	candidates = append(candidates, origin+"/.well-known/"+wellKnownName)
	return candidates
}

func normalizePathSuffix(path string) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func fetchProtectedResourceMeta(ctx context.Context, client *http.Client, url string) (*OAuthDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var meta struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}

	d := &OAuthDiscovery{}
	if len(meta.AuthorizationServers) > 0 {
		d.ResourceServer = meta.AuthorizationServers[0]
	}
	return d, nil
}

func fetchAuthServerMeta(ctx context.Context, client *http.Client, metaURL string) (*OAuthDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var meta OAuthDiscovery
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// ClientRegistration holds the result of dynamic client registration (RFC 7591).
type ClientRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	// RedirectURIs echoed back by the server.
	RedirectURIs []string `json:"redirect_uris,omitempty"`
}

// RegisterClient performs OAuth 2.0 Dynamic Client Registration (RFC 7591)
// at the given registration endpoint. This is required by MCP servers like
// Supabase that don't accept pre-configured client IDs.
func RegisterClient(ctx context.Context, registrationEndpoint string, redirectURIs []string, clientName string) (*ClientRegistration, error) {
	body := map[string]interface{}{
		"client_name":                clientName,
		"redirect_uris":              redirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none", // public client, PKCE-only
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: marshal registration request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: create registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: registration request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: read registration response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("mcp oauth: registration endpoint returned %d: %s", resp.StatusCode, string(respBody))
	}

	var reg ClientRegistration
	if err := json.Unmarshal(respBody, &reg); err != nil {
		return nil, fmt.Errorf("mcp oauth: unmarshal registration response: %w", err)
	}

	if reg.ClientID == "" {
		return nil, fmt.Errorf("mcp oauth: registration response missing client_id")
	}

	return &reg, nil
}

// GeneratePKCE generates a PKCE code verifier and S256 challenge pair.
func GeneratePKCE() (verifier string, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("mcp oauth: generate PKCE verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)

	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])

	return verifier, challenge, nil
}

// GenerateState generates a cryptographically random state parameter.
func GenerateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mcp oauth: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ExchangeCode exchanges an authorization code for tokens using the token endpoint.
func ExchangeCode(ctx context.Context, tokenEndpoint, code, verifier, redirectURI, clientID, clientSecret string) (*OAuthTokens, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	return doTokenRequest(ctx, tokenEndpoint, data)
}

// RefreshAccessToken uses a refresh token to obtain new access/refresh tokens.
func RefreshAccessToken(ctx context.Context, tokenEndpoint, refreshToken, clientID, clientSecret string) (*OAuthTokens, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	return doTokenRequest(ctx, tokenEndpoint, data)
}

func doTokenRequest(ctx context.Context, tokenEndpoint string, data url.Values) (*OAuthTokens, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("mcp oauth: read token response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("mcp oauth: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("mcp oauth: unmarshal token response: %w", err)
	}

	if tokens.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}

	return &tokens, nil
}
