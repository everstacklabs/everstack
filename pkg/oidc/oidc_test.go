package oidc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"
)

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newTestProvider(t *testing.T) (*Provider, *KeySet, Client) {
	t.Helper()
	ks, err := GenerateKeySet(2048)
	if err != nil {
		t.Fatal(err)
	}
	clients := NewMemClientStore()
	client := Client{
		ID:           "inst_acme_prod",
		RedirectURIs: []string{"https://acme-prod.everstack.ai/auth/callback"},
		OrgID:        "org-acme",
		InstanceID:   "inst-1",
	}
	clients.Register(client)
	p := NewProvider(ProviderConfig{Issuer: "https://app.everstack.ai"}, ks, NewMemCodeStore(), clients)
	return p, ks, client
}

func TestAuthCodeFlowRoundTrip(t *testing.T) {
	ctx := context.Background()
	p, ks, client := newTestProvider(t)

	verifier := "a-high-entropy-code-verifier-value-1234567890"
	nonce := "nonce-xyz"

	redirect, err := p.Authorize(ctx, AuthorizeRequest{
		ClientID:            client.ID,
		RedirectURI:         client.RedirectURIs[0],
		State:               "state-123",
		Scope:               "openid profile email org",
		Nonce:               nonce,
		CodeChallenge:       s256(verifier),
		CodeChallengeMethod: "S256",
		UserID:              "user-alice",
		Email:               "alice@acme.com",
		EmailVerified:       true,
		Name:                "Alice",
		OrgID:               "org-acme",
		OrgSlug:             "acme",
		InstanceID:          "inst-1",
	})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	u, _ := url.Parse(redirect)
	if got := u.Query().Get("state"); got != "state-123" {
		t.Errorf("state not echoed: %q", got)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}

	tok, err := p.Token(ctx, TokenRequest{
		GrantType:    "authorization_code",
		Code:         code,
		RedirectURI:  client.RedirectURIs[0],
		ClientID:     client.ID,
		CodeVerifier: verifier,
	})
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.IDToken == "" || tok.AccessToken == "" || tok.TokenType != "Bearer" {
		t.Fatalf("bad token response: %+v", tok)
	}

	// RP verifies the id_token using the OP's public keys.
	v := NewVerifier("https://app.everstack.ai", client.ID, NewStaticJWKSSource(ks))
	claims, err := v.VerifyIDToken(ctx, tok.IDToken, nonce)
	if err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}
	if claims.Subject != "user-alice" || claims.OrgID != "org-acme" || claims.InstanceID != "inst-1" {
		t.Errorf("unexpected claims: %+v", claims)
	}
	if claims.Email != "alice@acme.com" || !claims.EmailVerified {
		t.Errorf("email claims wrong: %+v", claims)
	}
}

func TestTokenSingleUseAndPKCE(t *testing.T) {
	ctx := context.Background()
	p, _, client := newTestProvider(t)
	verifier := "verifier-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	mint := func() string {
		redirect, err := p.Authorize(ctx, AuthorizeRequest{
			ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
			CodeChallenge: s256(verifier), CodeChallengeMethod: "S256",
			UserID: "user-alice", OrgID: "org-acme",
		})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(redirect)
		return u.Query().Get("code")
	}

	// Wrong PKCE verifier rejected.
	code := mint()
	if _, err := p.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code, RedirectURI: client.RedirectURIs[0], ClientID: client.ID, CodeVerifier: "wrong"}); err != ErrPKCEFailed {
		t.Fatalf("expected ErrPKCEFailed, got %v", err)
	}
	// That code is now consumed (single-use) even though it failed PKCE.
	if _, err := p.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code, RedirectURI: client.RedirectURIs[0], ClientID: client.ID, CodeVerifier: verifier}); err != ErrInvalidGrant {
		t.Fatalf("expected ErrInvalidGrant on reuse, got %v", err)
	}

	// Fresh code used twice: second use rejected.
	code2 := mint()
	if _, err := p.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code2, RedirectURI: client.RedirectURIs[0], ClientID: client.ID, CodeVerifier: verifier}); err != nil {
		t.Fatalf("first use should succeed: %v", err)
	}
	if _, err := p.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code2, RedirectURI: client.RedirectURIs[0], ClientID: client.ID, CodeVerifier: verifier}); err != ErrInvalidGrant {
		t.Fatalf("reuse must fail, got %v", err)
	}
}

func TestAuthorizeValidation(t *testing.T) {
	ctx := context.Background()
	p, _, client := newTestProvider(t)

	// Unknown client.
	if _, err := p.Authorize(ctx, AuthorizeRequest{ClientID: "nope", RedirectURI: client.RedirectURIs[0], CodeChallenge: "x", UserID: "u"}); err != ErrUnknownClient {
		t.Errorf("expected ErrUnknownClient, got %v", err)
	}
	// Bad redirect.
	if _, err := p.Authorize(ctx, AuthorizeRequest{ClientID: client.ID, RedirectURI: "https://evil.com/cb", CodeChallenge: "x", UserID: "u"}); err != ErrInvalidRedirect {
		t.Errorf("expected ErrInvalidRedirect, got %v", err)
	}
	// Missing PKCE.
	if _, err := p.Authorize(ctx, AuthorizeRequest{ClientID: client.ID, RedirectURI: client.RedirectURIs[0], UserID: "u"}); err != ErrPKCERequired {
		t.Errorf("expected ErrPKCERequired, got %v", err)
	}
}

func TestVerifierRejections(t *testing.T) {
	ctx := context.Background()
	p, ks, client := newTestProvider(t)
	verifier := "verifier-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	redirect, _ := p.Authorize(ctx, AuthorizeRequest{
		ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: s256(verifier), CodeChallengeMethod: "S256",
		Nonce: "n1", UserID: "user-alice", OrgID: "org-acme",
	})
	u, _ := url.Parse(redirect)
	tok, err := p.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: u.Query().Get("code"), RedirectURI: client.RedirectURIs[0], ClientID: client.ID, CodeVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}

	// Wrong nonce.
	v := NewVerifier("https://app.everstack.ai", client.ID, NewStaticJWKSSource(ks))
	if _, err := v.VerifyIDToken(ctx, tok.IDToken, "wrong-nonce"); err != ErrNonceMismatch {
		t.Errorf("expected ErrNonceMismatch, got %v", err)
	}
	// Wrong audience (different client verifier).
	vWrong := NewVerifier("https://app.everstack.ai", "some-other-client", NewStaticJWKSSource(ks))
	if _, err := vWrong.VerifyIDToken(ctx, tok.IDToken, "n1"); err == nil {
		t.Error("expected audience mismatch to fail")
	}
	// Wrong issuer.
	vIss := NewVerifier("https://evil.example", client.ID, NewStaticJWKSSource(ks))
	if _, err := vIss.VerifyIDToken(ctx, tok.IDToken, "n1"); err == nil {
		t.Error("expected issuer mismatch to fail")
	}
}

func TestJWKSAndDiscovery(t *testing.T) {
	p, ks, _ := newTestProvider(t)
	raw, err := p.JWKS()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := parseJWKS(raw)
	if err != nil {
		t.Fatalf("parseJWKS: %v", err)
	}
	if _, ok := keys[ks.ActiveKID()]; !ok {
		t.Error("published JWKS missing active kid")
	}

	d := p.Discovery()
	if d.Issuer != "https://app.everstack.ai" {
		t.Errorf("bad issuer %q", d.Issuer)
	}
	if d.JWKSURI != "https://app.everstack.ai/oauth/jwks" {
		t.Errorf("bad jwks_uri %q", d.JWKSURI)
	}
	if len(d.CodeChallengeMethodsSupported) == 0 || d.CodeChallengeMethodsSupported[0] != "S256" {
		t.Error("S256 must be advertised")
	}
}

func TestKeyRotationPreservesVerification(t *testing.T) {
	ctx := context.Background()
	p, ks, client := newTestProvider(t)
	verifier := "verifier-cccccccccccccccccccccccccccccc"
	redirect, _ := p.Authorize(ctx, AuthorizeRequest{
		ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: s256(verifier), CodeChallengeMethod: "S256",
		UserID: "user-alice", OrgID: "org-acme",
	})
	u, _ := url.Parse(redirect)
	tok, err := p.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: u.Query().Get("code"), RedirectURI: client.RedirectURIs[0], ClientID: client.ID, CodeVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}

	// Rotate AFTER minting; the old key is retained for verification.
	if err := ks.Rotate(2048); err != nil {
		t.Fatal(err)
	}
	v := NewVerifier("https://app.everstack.ai", client.ID, NewStaticJWKSSource(ks))
	if _, err := v.VerifyIDToken(ctx, tok.IDToken, ""); err != nil {
		t.Fatalf("token signed before rotation must still verify: %v", err)
	}
}
