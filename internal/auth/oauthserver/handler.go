package oauthserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// CLIClientID is the fixed public OAuth client identifier used by evs.
	CLIClientID = "evs-cli"
	// CLIScope grants the CLI the same API permissions as an interactive user.
	CLIScope = "cli:full"

	authorizationCodeTTL = 5 * time.Minute
	// AccessTokenTTL is the lifetime of browser-login access tokens.
	AccessTokenTTL = 15 * time.Minute
)

var (
	// ErrUnauthenticated tells the authorization endpoint to start browser login.
	ErrUnauthenticated = errors.New("oauth: unauthenticated")
	// ErrInvalidGrant collapses invalid, expired, consumed, and mismatched grants.
	ErrInvalidGrant = errors.New("oauth: invalid grant")
	// ErrAccessDenied indicates that a refresh identity is no longer authorized.
	ErrAccessDenied = errors.New("oauth: access denied")
)

// Identity is the user and organization bound to an OAuth grant.
type Identity struct {
	UserID           string
	Email            string
	OrganizationID   string
	OrganizationSlug string
	InstanceID       string
}

// AuthorizationGrant is the validated state persisted for a one-time code.
type AuthorizationGrant struct {
	Identity
	ClientID      string
	RedirectURI   string
	Scope         string
	CodeChallenge string
}

// AccessToken is a signed bearer token and its expiration time.
type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// TokenSet is returned by authorization-code exchange and refresh rotation.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
}

// IssueAccessToken signs an access token for a grant identity and client.
type IssueAccessToken func(Identity, string) (AccessToken, error)

// AuthorizeRefresh revalidates an identity before refresh-token rotation.
type AuthorizeRefresh func(context.Context, Identity) error

// Store persists one-time authorization codes and rotating refresh tokens.
type Store interface {
	CreateAuthorizationCode(context.Context, AuthorizationGrant, time.Duration) (string, error)
	RedeemAuthorizationCode(context.Context, string, string, string, string, string, IssueAccessToken) (*TokenSet, error)
	RotateRefreshToken(context.Context, string, string, string, AuthorizeRefresh, IssueAccessToken) (*TokenSet, error)
	RevokeRefreshToken(context.Context, string, string, string) error
}

// Config supplies the storage, browser-session, and token-signing seams.
type Config struct {
	Store            Store
	ResolveIdentity  func(*http.Request) (*Identity, error)
	ResolveInstance  func(*http.Request) (string, error)
	AuthorizeRefresh AuthorizeRefresh
	IssueAccessToken IssueAccessToken
	LoginPath        string
}

type handler struct {
	store            Store
	resolveIdentity  func(*http.Request) (*Identity, error)
	resolveInstance  func(*http.Request) (string, error)
	authorizeRefresh AuthorizeRefresh
	issueAccessToken IssueAccessToken
	loginPath        string
}

// NewHandler returns the CLI OAuth authorization server HTTP handler.
func NewHandler(cfg Config) http.Handler {
	loginPath := strings.TrimSpace(cfg.LoginPath)
	if loginPath == "" {
		loginPath = "/login"
	}
	h := &handler{
		store:            cfg.Store,
		resolveIdentity:  cfg.ResolveIdentity,
		resolveInstance:  cfg.ResolveInstance,
		authorizeRefresh: cfg.AuthorizeRefresh,
		issueAccessToken: cfg.IssueAccessToken,
		loginPath:        loginPath,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/authorize", h.handleAuthorize)
	mux.HandleFunc("POST /oauth/token", h.handleToken)
	mux.HandleFunc("POST /oauth/revoke", h.handleRevoke)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.handleMetadata)
	return mux
}

func (h *handler) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	request, err := parseAuthorizationRequest(r.URL.Query())
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if h.store == nil || h.resolveIdentity == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "OAuth authorization is not configured")
		return
	}

	identity, err := h.resolveIdentity(r)
	if errors.Is(err, ErrUnauthenticated) || (err == nil && identity == nil) {
		returnURL := r.URL.RequestURI()
		loginURL := h.loginPath + "?returnUrl=" + url.QueryEscape(returnURL)
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}
	if errors.Is(err, ErrAccessDenied) {
		redirectAuthorizationError(w, r, request, "access_denied", "access to this instance is not allowed")
		return
	}
	if err != nil {
		redirectAuthorizationError(w, r, request, "server_error", "could not resolve the authenticated user")
		return
	}
	if identity.UserID == "" || identity.OrganizationID == "" {
		redirectAuthorizationError(w, r, request, "access_denied", "an organization membership is required")
		return
	}
	instanceID, err := h.currentInstance(r)
	if errors.Is(err, ErrAccessDenied) {
		redirectAuthorizationError(w, r, request, "access_denied", "the target instance is not available")
		return
	}
	if err != nil {
		redirectAuthorizationError(w, r, request, "server_error", "could not resolve the target instance")
		return
	}
	if identity.InstanceID == "" {
		identity.InstanceID = instanceID
	} else if identity.InstanceID != instanceID {
		redirectAuthorizationError(w, r, request, "access_denied", "the authenticated identity does not match the target instance")
		return
	}

	code, err := h.store.CreateAuthorizationCode(r.Context(), AuthorizationGrant{
		Identity:      *identity,
		ClientID:      request.clientID,
		RedirectURI:   request.redirectURI,
		Scope:         request.scope,
		CodeChallenge: request.codeChallenge,
	}, authorizationCodeTTL)
	if err != nil {
		redirectAuthorizationError(w, r, request, "server_error", "could not create an authorization code")
		return
	}

	redirect, _ := url.Parse(request.redirectURI)
	query := redirect.Query()
	query.Set("code", code)
	query.Set("state", request.state)
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

type authorizationRequest struct {
	clientID      string
	redirectURI   string
	scope         string
	state         string
	codeChallenge string
}

func parseAuthorizationRequest(values url.Values) (authorizationRequest, error) {
	request := authorizationRequest{
		clientID:      values.Get("client_id"),
		redirectURI:   values.Get("redirect_uri"),
		scope:         values.Get("scope"),
		state:         values.Get("state"),
		codeChallenge: values.Get("code_challenge"),
	}
	if values.Get("response_type") != "code" {
		return authorizationRequest{}, errors.New("response_type must be code")
	}
	if request.clientID != CLIClientID {
		return authorizationRequest{}, errors.New("unknown client_id")
	}
	if err := validateLoopbackRedirectURI(request.redirectURI); err != nil {
		return authorizationRequest{}, err
	}
	if request.scope != CLIScope {
		return authorizationRequest{}, errors.New("unsupported scope")
	}
	if request.state == "" {
		return authorizationRequest{}, errors.New("state is required")
	}
	if values.Get("code_challenge_method") != "S256" {
		return authorizationRequest{}, errors.New("code_challenge_method must be S256")
	}
	if !isBase64URLValue(request.codeChallenge, 43, 128) {
		return authorizationRequest{}, errors.New("invalid code_challenge")
	}
	return request, nil
}

func validateLoopbackRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid redirect_uri")
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.Fragment != "" ||
		parsed.RawQuery != "" || parsed.Path != "/oauth/callback" {
		return errors.New("redirect_uri must be an HTTP loopback callback")
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || host != "127.0.0.1" {
		return errors.New("redirect_uri must use 127.0.0.1 with an ephemeral port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("redirect_uri has an invalid port")
	}
	return nil
}

func isBase64URLValue(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func redirectAuthorizationError(w http.ResponseWriter, r *http.Request, request authorizationRequest, code, description string) {
	redirect, err := url.Parse(request.redirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, code, description)
		return
	}
	query := redirect.Query()
	query.Set("error", code)
	query.Set("error_description", description)
	query.Set("state", request.state)
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func (h *handler) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	if h.store == nil || h.issueAccessToken == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "OAuth token exchange is not configured")
		return
	}
	if r.Form.Get("client_id") != CLIClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		h.exchangeAuthorizationCode(w, r)
	case "refresh_token":
		h.exchangeRefreshToken(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	}
}

func (h *handler) exchangeAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	redirectURI := r.Form.Get("redirect_uri")
	verifier := r.Form.Get("code_verifier")
	if code == "" || verifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code and code_verifier are required")
		return
	}
	if err := validateLoopbackRedirectURI(redirectURI); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !isPKCEVerifier(verifier) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid code_verifier")
		return
	}

	instanceID, err := h.currentInstance(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization grant is invalid or expired")
		return
	}
	tokens, err := h.store.RedeemAuthorizationCode(
		r.Context(),
		code,
		CLIClientID,
		redirectURI,
		verifier,
		instanceID,
		h.issueAccessToken,
	)
	if errors.Is(err, ErrInvalidGrant) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization grant is invalid or expired")
		return
	}
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not exchange authorization code")
		return
	}
	writeTokenResponse(w, tokens)
}

func (h *handler) exchangeRefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.Form.Get("refresh_token")
	if refreshToken == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	if h.authorizeRefresh == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "OAuth refresh authorization is not configured")
		return
	}
	instanceID, err := h.currentInstance(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	tokens, err := h.store.RotateRefreshToken(
		r.Context(),
		refreshToken,
		CLIClientID,
		instanceID,
		h.authorizeRefresh,
		h.issueAccessToken,
	)
	if errors.Is(err, ErrInvalidGrant) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not refresh access token")
		return
	}
	writeTokenResponse(w, tokens)
}

func (h *handler) handleRevoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	if h.store == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "OAuth revocation is not configured")
		return
	}
	if r.Form.Get("client_id") != CLIClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}
	refreshToken := r.Form.Get("token")
	if refreshToken == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	instanceID, err := h.currentInstance(r)
	if err == nil {
		err = h.store.RevokeRefreshToken(r.Context(), refreshToken, CLIClientID, instanceID)
	}
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not revoke refresh token")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) currentInstance(r *http.Request) (string, error) {
	if h.resolveInstance == nil {
		return "", nil
	}
	return h.resolveInstance(r)
}

func isPKCEVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' || r == '~' {
			continue
		}
		return false
	}
	return true
}

func writeTokenResponse(w http.ResponseWriter, tokens *TokenSet) {
	if tokens == nil || tokens.AccessToken == "" || tokens.RefreshToken == "" ||
		tokens.ExpiresAt.IsZero() || !tokens.ExpiresAt.After(time.Now()) {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "token exchange returned no tokens")
		return
	}
	expiresIn := int64(time.Until(tokens.ExpiresAt).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    expiresIn,
		"scope":         tokens.Scope,
	})
}

func (h *handler) handleMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := requestOrigin(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"revocation_endpoint":                   issuer + "/oauth/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{CLIScope},
	})
}

func requestOrigin(r *http.Request) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme != "http" && scheme != "https" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}
