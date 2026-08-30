package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/mcp"
	"google.golang.org/protobuf/types/known/structpb"
)

// HandleOAuthInitiate returns the HTTP handler for POST /mcp/oauth/initiate.
func (s *Server) HandleOAuthInitiate() http.HandlerFunc {
	return s.handleOAuthInitiate
}

// HandleOAuthCallback returns the HTTP handler for GET /mcp/oauth/callback.
func (s *Server) HandleOAuthCallback() http.HandlerFunc {
	return s.handleOAuthCallback
}

// oauthInitiateRequest is the request body for the OAuth initiate endpoint.
type oauthInitiateRequest struct {
	TenantID string `json:"tenant_id"`
	ServerID string `json:"server_id"`
	// CallbackBaseURL is the base URL where the callback endpoint is reachable
	// (e.g., "https://admin.example.com").
	CallbackBaseURL string `json:"callback_base_url"`
}

// oauthInitiateResponse is returned with the authorization URL for the browser.
type oauthInitiateResponse struct {
	AuthorizationURL string `json:"authorization_url"`
}

func (s *Server) handleOAuthInitiate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req oauthInitiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID, err := s.resolveTenantID(ctx, req.TenantID)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "tenant resolution failed")
		return
	}

	serverID := mustParseInt64(req.ServerID)
	if serverID == 0 {
		writeJSONError(w, http.StatusBadRequest, "server_id is required")
		return
	}

	// Load server URL from DB. Tenant predicate prevents this OAuth
	// init endpoint from leaking the URL of another tenant's MCP
	// server (and from triggering an OAuth round-trip whose tokens
	// would land scoped to the wrong tenant on callback).
	var serverURL string
	err = s.db.QueryRowContext(ctx,
		`SELECT url FROM mcp_servers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		serverID, tenantID,
	).Scan(&serverURL)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}

	// Discover OAuth endpoints
	discovery, err := mcp.DiscoverOAuth(ctx, serverURL)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("OAuth discovery failed: %v", err))
		return
	}

	// Generate PKCE pair
	verifier, challenge, err := mcp.GeneratePKCE()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate PKCE")
		return
	}

	// Generate state
	state, err := mcp.GenerateState()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate state")
		return
	}

	// Build redirect URI
	callbackBase := req.CallbackBaseURL
	if callbackBase == "" {
		// Fall back to the request's own origin
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		callbackBase = fmt.Sprintf("%s://%s", scheme, r.Host)
	}
	redirectURI := callbackBase + "/mcp/oauth/callback"

	// Dynamic client registration (RFC 7591) — required by servers like Supabase
	// that don't accept pre-configured client IDs.
	clientID := ""
	clientSecret := ""
	if discovery.RegistrationEndpoint != "" {
		reg, err := mcp.RegisterClient(ctx, discovery.RegistrationEndpoint, []string{redirectURI}, "Everstack")
		if err != nil {
			logger.Warn("dynamic client registration failed, falling back to default client_id",
				"error", err, "registration_endpoint", discovery.RegistrationEndpoint)
			clientID = "everstack"
		} else {
			clientID = reg.ClientID
			clientSecret = reg.ClientSecret
		}
	}
	if clientID == "" {
		clientID = "everstack"
	}

	// Store state in DB
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO mcp_oauth_states (tenant_id, server_id, state, code_verifier, redirect_uri, token_endpoint, client_id, client_secret)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tenantID, serverID, state, verifier, redirectURI, discovery.TokenEndpoint, clientID, clientSecret,
	)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store OAuth state: %v", err))
		return
	}

	// Build authorization URL
	authURL, err := url.Parse(discovery.AuthorizationEndpoint)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "invalid authorization endpoint")
		return
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	writeJSON(w, http.StatusOK, oauthInitiateResponse{
		AuthorizationURL: authURL.String(),
	})
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		writeJSONError(w, http.StatusBadRequest, "missing code or state parameter")
		return
	}

	// Look up state from DB
	var (
		serverID      int64
		tenantID      string
		codeVerifier  string
		redirectURI   string
		tokenEndpoint string
		clientID      string
		clientSecret  string
		expiresAt     time.Time
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT server_id, tenant_id, code_verifier, redirect_uri, token_endpoint, client_id, client_secret, expires_at
		 FROM mcp_oauth_states WHERE state = $1`, state,
	).Scan(&serverID, &tenantID, &codeVerifier, &redirectURI, &tokenEndpoint, &clientID, &clientSecret, &expiresAt)
	if err != nil {
		writeOAuthCallbackError(w, "Invalid or expired OAuth state")
		return
	}

	// Check expiry
	if time.Now().After(expiresAt) {
		writeOAuthCallbackError(w, "OAuth state has expired")
		return
	}

	// Delete used state
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM mcp_oauth_states WHERE state = $1`, state)

	// Exchange code for tokens
	tokens, err := mcp.ExchangeCode(ctx, tokenEndpoint, code, codeVerifier, redirectURI, clientID, clientSecret)
	if err != nil {
		logger.Error("OAuth code exchange failed", "error", err, "server_id", serverID)
		writeOAuthCallbackError(w, fmt.Sprintf("Token exchange failed: %v", err))
		return
	}

	// Update server's auth config in DB
	authConfig := map[string]interface{}{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"token_url":     tokenEndpoint,
		"client_id":     clientID,
	}
	if clientSecret != "" {
		authConfig["client_secret"] = clientSecret
	}
	if !tokens.ExpiresAt.IsZero() {
		authConfig["expires_at"] = tokens.ExpiresAt.Format(time.RFC3339)
	}
	authConfigJSON, _ := json.Marshal(authConfig)

	_, err = s.db.ExecContext(ctx,
		`UPDATE mcp_servers SET auth_type = 'oauth2', auth_config = $1, updated_at = NOW()
		 WHERE id = $2 AND deleted_at IS NULL`,
		string(authConfigJSON), serverID,
	)
	if err != nil {
		logger.Error("failed to update server auth config", "error", err, "server_id", serverID)
		writeOAuthCallbackError(w, "Failed to save tokens")
		return
	}

	// Re-register server in the live registry with new auth
	s.reRegisterServerWithAuth(ctx, serverID, tenantID)

	// Emit CQRS event
	s.publishEvent(ctx, "mcp.server.oauth.connected", map[string]interface{}{
		"server_id": serverID,
		"tenant_id": tenantID,
	})

	// Return HTML that notifies the opener and closes the popup
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>OAuth Complete</title></head>
<body>
<p>Authorization successful. This window will close automatically.</p>
<script>
  if (window.opener) {
    window.opener.postMessage({ type: 'mcp-oauth-success', serverId: '`+strconv.FormatInt(serverID, 10)+`' }, '*');
  }
  window.close();
</script>
</body></html>`)
}

// reRegisterServerWithAuth loads the server from DB and re-registers it in the
// live registry with updated auth configuration.
func (s *Server) reRegisterServerWithAuth(ctx context.Context, serverID int64, tenantID string) {
	var (
		name, serverURL, transportType, authType, authConfigJSON, headersJSON, stdioConfigJSON string
		enabled                                                                                bool
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT name, url, transport_type, auth_type, auth_config, headers, stdio_config, enabled
		 FROM mcp_servers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		serverID, tenantID,
	).Scan(&name, &serverURL, &transportType, &authType, &authConfigJSON, &headersJSON, &stdioConfigJSON, &enabled)
	if err != nil {
		logger.Error("failed to load server for re-registration", "error", err, "server_id", serverID)
		return
	}

	id := strconv.FormatInt(serverID, 10)

	// Unregister existing connection
	s.registry.Unregister(id)

	if !enabled {
		return
	}

	cfg := buildServerConfig(id, tenantID, name, serverURL, transportType, authType, authConfigJSON, headersJSON, stdioConfigJSON)

	// Wire token update callback for OAuth2
	if cfg.AuthConfig != nil && cfg.AuthConfig.Type == mcp.AuthTypeOAuth2 {
		cfg.OnTokenUpdate = s.makeTokenUpdateCallback(serverID, tenantID)
	}

	if _, err := s.registry.Register(ctx, cfg); err != nil {
		logger.Warn("re-registration failed after OAuth", "error", err, "server_id", serverID)
	}
}

// makeTokenUpdateCallback returns a callback that persists refreshed OAuth tokens to DB.
func (s *Server) makeTokenUpdateCallback(serverID int64, tenantID string) func(*mcp.AuthConfig) {
	return func(auth *mcp.AuthConfig) {
		authConfig := map[string]interface{}{
			"access_token":  auth.AccessToken,
			"refresh_token": auth.RefreshToken,
			"token_url":     auth.TokenURL,
			"client_id":     auth.ClientID,
		}
		if auth.ClientSecret != "" {
			authConfig["client_secret"] = auth.ClientSecret
		}
		if !auth.ExpiresAt.IsZero() {
			authConfig["expires_at"] = auth.ExpiresAt.Format(time.RFC3339)
		}
		authConfigJSON, _ := json.Marshal(authConfig)

		_, err := s.db.ExecContext(s.ctx,
			`UPDATE mcp_servers SET auth_config = $1, updated_at = NOW()
			 WHERE id = $2 AND deleted_at IS NULL`,
			string(authConfigJSON), serverID,
		)
		if err != nil {
			logger.Error("failed to persist refreshed tokens", "error", err, "server_id", serverID)
		}
	}
}

// ─── JSON helpers ─────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeOAuthCallbackError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>OAuth Error</title></head>
<body>
<p>Authorization failed: %s</p>
<script>
  if (window.opener) {
    window.opener.postMessage({ type: 'mcp-oauth-error', error: %q }, '*');
  }
  setTimeout(function() { window.close(); }, 3000);
</script>
</body></html>`, msg, msg)
}

// reRegisterServerContext is a minimal interface to pass either context.Context
// or any type with Done() to reRegisterServerWithAuth.
func (s *Server) reRegisterServerFromDB(serverID int64, tenantID string) {
	s.reRegisterServerWithAuth(s.ctx, serverID, tenantID)
}

// buildAuthConfigFromJSON parses auth config from JSON and auth type string,
// used when restoring servers from DB.
func buildAuthConfigFromJSON(authType, authConfigJSON string) *mcp.AuthConfig {
	if authType == "none" || authType == "" {
		return nil
	}

	var authMap map[string]interface{}
	if authConfigJSON != "" && authConfigJSON != "{}" {
		_ = json.Unmarshal([]byte(authConfigJSON), &authMap)
	}

	var authStruct *structpb.Struct
	if len(authMap) > 0 {
		authStruct, _ = structpb.NewStruct(authMap)
	}

	return buildAuthConfig(authType, authStruct)
}
