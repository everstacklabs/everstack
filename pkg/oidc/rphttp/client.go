// Package rphttp is the relying-party (instance) side of OIDC federation: the
// login redirect to the cloud OP and the callback that verifies the id_token and
// mints an instance-local session. This is what replaces the shared
// .everstack.ai cookie + the token-exchange/launch-code handoffs with one
// standard authorization-code flow.
package rphttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/everstacklabs/everstack/pkg/oidc"
)

// OPClient talks to the cloud OP's authorize/token endpoints.
type OPClient struct {
	issuer            string
	authorizeEndpoint string
	tokenEndpoint     string
	clientID          string
	redirectURI       string
	scope             string
	http              *http.Client
}

// NewOPClient derives the standard endpoints from the issuer.
func NewOPClient(issuer, clientID, redirectURI, scope string, httpClient *http.Client) *OPClient {
	base := strings.TrimRight(issuer, "/")
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if scope == "" {
		scope = "openid profile email org"
	}
	return &OPClient{
		issuer:            base,
		authorizeEndpoint: base + oidc.PathAuthorize,
		tokenEndpoint:     base + oidc.PathToken,
		clientID:          clientID,
		redirectURI:       redirectURI,
		scope:             scope,
		http:              httpClient,
	}
}

// Issuer returns the OP issuer (for constructing the Verifier).
func (c *OPClient) Issuer() string { return c.issuer }

// ClientID returns the RP's client id (expected token audience).
func (c *OPClient) ClientID() string { return c.clientID }

// AuthorizeURL builds the /authorize redirect URL with PKCE.
func (c *OPClient) AuthorizeURL(state, nonce, codeChallenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("scope", c.scope)
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return c.authorizeEndpoint + "?" + q.Encode()
}

// Exchange swaps an authorization code for tokens at the OP token endpoint.
func (c *OPClient) Exchange(ctx context.Context, code, codeVerifier string) (*oidc.TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirectURI)
	form.Set("client_id", c.clientID)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc rp: token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc rp: token endpoint %d: %s", resp.StatusCode, string(body))
	}
	var tr oidc.TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("oidc rp: decode token response: %w", err)
	}
	return &tr, nil
}
