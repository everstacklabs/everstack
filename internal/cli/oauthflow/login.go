package oauthflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// ClientID is the registered public client identifier for evs.
	ClientID = "evs-cli"
	// Scope is the API permission requested by evs.
	Scope = "cli:full"
)

// ErrUnavailable means the server or environment cannot perform browser PKCE.
var ErrUnavailable = errors.New("OAuth PKCE is unavailable")

// Options configure an interactive browser PKCE login.
type Options struct {
	APIURL      string
	OpenBrowser func(string) error
	HTTPClient  *http.Client
}

// Tokens contains the credentials returned by the OAuth token endpoint.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
}

// RefreshOptions configure a refresh-token grant.
type RefreshOptions struct {
	APIURL       string
	RefreshToken string
	HTTPClient   *http.Client
}

type metadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// Login runs Authorization Code with PKCE using a loopback callback.
func Login(ctx context.Context, opts Options) (*Tokens, error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	discovery, err := discover(ctx, client, opts.APIURL)
	if err != nil {
		return nil, err
	}
	if opts.OpenBrowser == nil {
		return nil, fmt.Errorf("%w: browser opener is not configured", ErrUnavailable)
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start OAuth loopback listener: %w", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := "http://127.0.0.1:" + strconv.Itoa(port) + "/oauth/callback"

	state, err := randomValue(32)
	if err != nil {
		return nil, fmt.Errorf("generate OAuth state: %w", err)
	}
	verifier, err := randomValue(64)
	if err != nil {
		return nil, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authorizationURL, err := url.Parse(discovery.AuthorizationEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse authorization endpoint: %w", err)
	}
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", Scope)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	authorizationURL.RawQuery = query.Encode()

	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)
	callbackMux := http.NewServeMux()
	callbackMux.HandleFunc("GET /oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "OAuth state mismatch", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: errors.New("OAuth callback state mismatch")}:
			default:
			}
			return
		}
		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			http.Error(w, "Authorization was not completed", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("OAuth authorization failed: %s", oauthErr)}:
			default:
			}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Authorization code is missing", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: errors.New("OAuth callback did not include a code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "<!doctype html><title>Everstack login complete</title><p>Login complete. You can close this window and return to the terminal.</p>")
		select {
		case resultCh <- callbackResult{code: code}:
		default:
		}
	})
	callbackServer := &http.Server{
		Handler:           callbackMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		if err := callbackServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	if err := opts.OpenBrowser(authorizationURL.String()); err != nil {
		shutdownLoopback(callbackServer)
		return nil, fmt.Errorf("%w: open browser: %v", ErrUnavailable, err)
	}

	var result callbackResult
	select {
	case <-ctx.Done():
		shutdownLoopback(callbackServer)
		return nil, ctx.Err()
	case err := <-serverErr:
		return nil, fmt.Errorf("OAuth loopback server: %w", err)
	case result = <-resultCh:
		shutdownLoopback(callbackServer)
	}
	if result.err != nil {
		return nil, result.err
	}
	return exchangeCode(ctx, client, discovery.TokenEndpoint, result.code, redirectURI, verifier)
}

func discover(ctx context.Context, client *http.Client, apiURL string) (*metadata, error) {
	base, err := url.Parse(strings.TrimRight(apiURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("invalid API URL %q", apiURL)
	}
	discoveryURL := *base
	discoveryURL.Path = "/.well-known/oauth-authorization-server"
	discoveryURL.RawQuery = ""
	discoveryURL.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover OAuth server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		return nil, ErrUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discover OAuth server: HTTP %d", resp.StatusCode)
	}
	var got metadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&got); err != nil {
		return nil, fmt.Errorf("%w: decode metadata: %v", ErrUnavailable, err)
	}
	if !contains(got.GrantTypesSupported, "authorization_code") ||
		!contains(got.GrantTypesSupported, "refresh_token") ||
		!contains(got.CodeChallengeMethodsSupported, "S256") {
		return nil, ErrUnavailable
	}
	if err := validateSameOriginEndpoint(base, got.AuthorizationEndpoint, "/oauth/authorize"); err != nil {
		return nil, fmt.Errorf("invalid authorization endpoint: %w", err)
	}
	if err := validateSameOriginEndpoint(base, got.TokenEndpoint, "/oauth/token"); err != nil {
		return nil, fmt.Errorf("invalid token endpoint: %w", err)
	}
	return &got, nil
}

func validateSameOriginEndpoint(base *url.URL, raw, path string) error {
	endpoint, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if endpoint.Scheme != base.Scheme || endpoint.Host != base.Host ||
		endpoint.User != nil || endpoint.Path != path ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("endpoint must use the API origin and canonical path")
	}
	return nil
}

func exchangeCode(ctx context.Context, client *http.Client, endpoint, code, redirectURI, verifier string) (*Tokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	return exchangeTokenForm(ctx, client, endpoint, form)
}

// Refresh rotates an opaque refresh token and returns a new token set.
func Refresh(ctx context.Context, opts RefreshOptions) (*Tokens, error) {
	if strings.TrimSpace(opts.RefreshToken) == "" {
		return nil, errors.New("refresh token is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	discovery, err := discover(ctx, client, opts.APIURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ClientID},
		"refresh_token": {opts.RefreshToken},
	}
	return exchangeTokenForm(ctx, client, discovery.TokenEndpoint, form)
}

// Revoke invalidates the refresh-token family at the API origin.
func Revoke(ctx context.Context, apiURL, refreshToken string, httpClient *http.Client) error {
	if strings.TrimSpace(refreshToken) == "" {
		return errors.New("refresh token is required")
	}
	base, err := url.Parse(strings.TrimRight(apiURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return fmt.Errorf("invalid API URL %q", apiURL)
	}
	endpoint := *base
	endpoint.Path = "/oauth/revoke"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	form := url.Values{
		"client_id":       {ClientID},
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint.String(),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	revokeClient := *client
	revokeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := revokeClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke OAuth refresh token: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("revoke OAuth refresh token: HTTP %d", resp.StatusCode)
	}
	return nil
}

func exchangeTokenForm(ctx context.Context, client *http.Client, endpoint string, form url.Values) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	tokenClient := *client
	tokenClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := tokenClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange OAuth code: %w", err)
	}
	defer resp.Body.Close()
	var payload struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int64  `json:"expires_in"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode OAuth token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if payload.Error != "" {
			return nil, fmt.Errorf("OAuth token exchange failed: %s", payload.Error)
		}
		return nil, fmt.Errorf("OAuth token exchange failed: HTTP %d", resp.StatusCode)
	}
	if payload.AccessToken == "" || payload.RefreshToken == "" ||
		!strings.EqualFold(payload.TokenType, "Bearer") || payload.ExpiresIn <= 0 {
		return nil, errors.New("OAuth token response is incomplete")
	}
	return &Tokens{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresAt:    time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second),
		Scope:        payload.Scope,
	}, nil
}

func shutdownLoopback(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func randomValue(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
