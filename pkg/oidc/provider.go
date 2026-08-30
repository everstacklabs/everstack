package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Common OP errors. These map to OAuth error codes at the HTTP layer.
var (
	ErrUnknownClient    = errors.New("oidc: unknown client")
	ErrInvalidRedirect  = errors.New("oidc: redirect_uri not registered for client")
	ErrInvalidGrant     = errors.New("oidc: invalid or expired authorization code")
	ErrPKCERequired     = errors.New("oidc: code_challenge required")
	ErrPKCEFailed       = errors.New("oidc: code_verifier does not match challenge")
	ErrUnsupportedGrant = errors.New("oidc: unsupported grant_type")
)

// ProviderConfig configures token lifetimes and the issuer identity.
type ProviderConfig struct {
	Issuer         string
	IDTokenTTL     time.Duration
	AccessTokenTTL time.Duration
	CodeTTL        time.Duration
}

func (c *ProviderConfig) withDefaults() {
	if c.IDTokenTTL == 0 {
		c.IDTokenTTL = 10 * time.Minute
	}
	if c.AccessTokenTTL == 0 {
		c.AccessTokenTTL = 15 * time.Minute
	}
	if c.CodeTTL == 0 {
		c.CodeTTL = 60 * time.Second
	}
}

// Provider is the OpenID Provider. The cloud constructs one; it brokers WorkOS
// (the caller authenticates the user before calling Authorize) and federates
// identity to instance relying parties.
type Provider struct {
	cfg     ProviderConfig
	keys    *KeySet
	codes   CodeStore
	clients ClientStore
	now     func() time.Time
}

// NewProvider builds an OP. keys/codes/clients are required.
func NewProvider(cfg ProviderConfig, keys *KeySet, codes CodeStore, clients ClientStore) *Provider {
	cfg.withDefaults()
	return &Provider{cfg: cfg, keys: keys, codes: codes, clients: clients, now: time.Now}
}

// JWKS returns the published key set bytes for the jwks_uri endpoint.
func (p *Provider) JWKS() ([]byte, error) { return p.keys.JWKS() }

// AuthorizeRequest carries the /authorize parameters plus the identity the OP
// has already authenticated and authorized (org membership checked by the
// caller before issuing a code).
type AuthorizeRequest struct {
	ClientID            string
	RedirectURI         string
	State               string
	Scope               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string

	UserID        string
	Email         string
	EmailVerified bool
	Name          string
	OrgID         string
	OrgSlug       string
	InstanceID    string
	AuthTime      time.Time
}

// Authorize validates the client + redirect_uri + PKCE, mints a single-use
// code bound to the supplied identity, and returns the redirect URL
// (redirect_uri?code=...&state=...). PKCE is mandatory.
func (p *Provider) Authorize(ctx context.Context, req AuthorizeRequest) (string, error) {
	client, ok, err := p.clients.Get(ctx, req.ClientID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrUnknownClient
	}
	if !client.ValidRedirect(req.RedirectURI) {
		return "", ErrInvalidRedirect
	}
	if req.CodeChallenge == "" {
		return "", ErrPKCERequired
	}
	if req.UserID == "" {
		return "", fmt.Errorf("oidc: Authorize requires an authenticated UserID")
	}

	code, err := randToken(32)
	if err != nil {
		return "", err
	}
	authTime := req.AuthTime
	if authTime.IsZero() {
		authTime = p.now()
	}
	if err := p.codes.Save(ctx, AuthCode{
		Code:                code,
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		Scope:               req.Scope,
		Nonce:               req.Nonce,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		UserID:              req.UserID,
		Email:               req.Email,
		EmailVerified:       req.EmailVerified,
		Name:                req.Name,
		OrgID:               req.OrgID,
		OrgSlug:             req.OrgSlug,
		InstanceID:          req.InstanceID,
		AuthTime:            authTime,
		ExpiresAt:           p.now().Add(p.cfg.CodeTTL),
	}); err != nil {
		return "", err
	}

	u, err := url.Parse(req.RedirectURI)
	if err != nil {
		return "", ErrInvalidRedirect
	}
	q := u.Query()
	q.Set("code", code)
	if req.State != "" {
		q.Set("state", req.State)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// TokenRequest carries /token parameters for the authorization_code grant.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	CodeVerifier string
}

// TokenResponse is the /token success body.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope,omitempty"`
}

// Token exchanges an authorization code for an ID token + access token,
// enforcing single-use, client/redirect match, and PKCE.
func (p *Provider) Token(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	if req.GrantType != "authorization_code" {
		return nil, ErrUnsupportedGrant
	}
	ac, ok, err := p.codes.Consume(ctx, req.Code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidGrant
	}
	if ac.ClientID != req.ClientID || ac.RedirectURI != req.RedirectURI {
		return nil, ErrInvalidGrant
	}
	if !VerifyPKCE(ac.CodeChallengeMethod, ac.CodeChallenge, req.CodeVerifier) {
		return nil, ErrPKCEFailed
	}

	now := p.now()
	idTok, err := p.keys.Sign(&IDClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    p.cfg.Issuer,
			Subject:   ac.UserID,
			Audience:  jwt.ClaimStrings{ac.ClientID},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(p.cfg.IDTokenTTL)),
			ID:        mustToken(),
		},
		Email:         ac.Email,
		EmailVerified: ac.EmailVerified,
		Name:          ac.Name,
		OrgID:         ac.OrgID,
		OrgSlug:       ac.OrgSlug,
		InstanceID:    ac.InstanceID,
		Nonce:         ac.Nonce,
		AuthTime:      ac.AuthTime.Unix(),
	})
	if err != nil {
		return nil, err
	}

	accessTok, err := p.keys.Sign(&AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    p.cfg.Issuer,
			Subject:   ac.UserID,
			Audience:  jwt.ClaimStrings{p.cfg.Issuer}, // access token is for the OP's own APIs
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(p.cfg.AccessTokenTTL)),
			ID:        mustToken(),
		},
		Scope: ac.Scope,
		OrgID: ac.OrgID,
	})
	if err != nil {
		return nil, err
	}

	return &TokenResponse{
		AccessToken: accessTok,
		IDToken:     idTok,
		TokenType:   "Bearer",
		ExpiresIn:   int64(p.cfg.IDTokenTTL.Seconds()),
		Scope:       ac.Scope,
	}, nil
}

// mustToken returns a random jti; on the vanishingly rare RNG failure it returns
// an empty id (the token is still valid, just without a jti).
func mustToken() string {
	t, err := randToken(16)
	if err != nil {
		return ""
	}
	return t
}
