// Package ophttp mounts the OpenID Provider (pkg/oidc.Provider) as HTTP
// endpoints: discovery, JWKS, /authorize, and /token. The cloud wires this with
// an Authenticator (resolves the logged-in cloud user, brokering WorkOS) and an
// AccessChecker (the org-membership gate that decides whether the user may
// federate into the requested instance) so the membership check lives at the
// authorization endpoint, where it belongs.
package ophttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/everstacklabs/everstack/pkg/oidc"
)

// Authenticator resolves the currently logged-in cloud user for an /authorize
// request. When the user is not logged in, the handler redirects them to
// LoginURL (which must bring them back to the original /authorize URL).
type Authenticator interface {
	CurrentUser(r *http.Request) (user CurrentUser, ok bool)
	LoginURL(r *http.Request) string
}

// CurrentUser is the authenticated principal from the cloud session.
type CurrentUser struct {
	UserID               string
	Email                string
	Name                 string
	EmailVerified        bool
	AuthenticationMethod string
	IdentityConnectionID string
}

// AccessChecker decides whether the authenticated session may obtain a token
// for client. The full session principal is passed so an implementation can
// enforce organization authentication assurance as well as membership.
type AccessChecker interface {
	Authorize(ctx context.Context, user CurrentUser, client oidc.Client) (AccessResult, error)
}

// AccessIdentity is the org/instance context bound into the issued tokens.
type AccessIdentity struct {
	OrgID      string
	OrgSlug    string
	InstanceID string
}

type AccessDecision string

const (
	AccessAllowed                AccessDecision = "allow"
	AccessDenied                 AccessDecision = "deny"
	AccessAuthenticationRequired AccessDecision = "authentication_required"
)

type AccessResult struct {
	Decision AccessDecision
	Identity AccessIdentity
}

// StepUpAuthenticator is implemented by deployments that can route an
// authenticated browser through stronger organization authentication and then
// return to this same authorization request.
type StepUpAuthenticator interface {
	StepUpURL(r *http.Request, identity AccessIdentity) string
}

// Handler serves the OP endpoints.
type Handler struct {
	p       *oidc.Provider
	auth    Authenticator
	access  AccessChecker
	clients oidc.ClientStore
}

// New builds the OP HTTP handler. clients is the same store the provider uses,
// needed so /authorize can resolve the client before the access check.
func New(p *oidc.Provider, clients oidc.ClientStore, auth Authenticator, access AccessChecker) *Handler {
	return &Handler{p: p, auth: auth, access: access, clients: clients}
}

// Register mounts the endpoints on mux at their standard paths.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc(oidc.PathDiscovery, h.handleDiscovery)
	mux.HandleFunc(oidc.PathJWKS, h.handleJWKS)
	mux.HandleFunc(oidc.PathAuthorize, h.handleAuthorize)
	mux.HandleFunc(oidc.PathToken, h.handleToken)
}

func (h *Handler) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.p.Discovery())
}

func (h *Handler) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	raw, err := h.p.JWKS()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "jwks unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(raw)
}

func (h *Handler) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	if clientID == "" || redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id and redirect_uri are required")
		return
	}
	// Resolve + validate the client and redirect before any redirect-based error,
	// so we never reflect an error to an unvalidated redirect_uri (open redirect).
	client, ok, err := h.clients.Get(r.Context(), clientID)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "client lookup failed")
		return
	}
	if !ok || !client.ValidRedirect(redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client or unregistered redirect_uri")
		return
	}

	// Authenticate the user (broker the cloud session). If absent, send them to
	// login; the Authenticator preserves the return-to authorize URL.
	cu, authed := h.auth.CurrentUser(r)
	if !authed {
		http.Redirect(w, r, h.auth.LoginURL(r), http.StatusFound)
		return
	}

	// Org-membership gate: may this user federate into this instance?
	access, err := h.access.Authorize(r.Context(), cu, client)
	if err != nil {
		redirectError(w, r, redirectURI, q.Get("state"), "server_error", "authorization check failed")
		return
	}
	if access.Decision == AccessAuthenticationRequired {
		if stepUp, ok := h.auth.(StepUpAuthenticator); ok {
			if target := stepUp.StepUpURL(r, access.Identity); target != "" {
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
		}
		redirectError(w, r, redirectURI, q.Get("state"), "access_denied", "organization single sign-on is required")
		return
	}
	if access.Decision != AccessAllowed {
		redirectError(w, r, redirectURI, q.Get("state"), "access_denied", "not a member of the target organization")
		return
	}
	ident := access.Identity

	redirect, err := h.p.Authorize(r.Context(), oidc.AuthorizeRequest{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		State:               q.Get("state"),
		Scope:               q.Get("scope"),
		Nonce:               q.Get("nonce"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		UserID:              cu.UserID,
		Email:               cu.Email,
		EmailVerified:       cu.EmailVerified,
		Name:                cu.Name,
		OrgID:               ident.OrgID,
		OrgSlug:             ident.OrgSlug,
		InstanceID:          ident.InstanceID,
	})
	if err != nil {
		code := "invalid_request"
		if errors.Is(err, oidc.ErrPKCERequired) {
			code = "invalid_request"
		}
		redirectError(w, r, redirectURI, q.Get("state"), code, err.Error())
		return
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (h *Handler) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form")
		return
	}
	resp, err := h.p.Token(r.Context(), oidc.TokenRequest{
		GrantType:    r.PostForm.Get("grant_type"),
		Code:         r.PostForm.Get("code"),
		RedirectURI:  r.PostForm.Get("redirect_uri"),
		ClientID:     r.PostForm.Get("client_id"),
		CodeVerifier: r.PostForm.Get("code_verifier"),
	})
	if err != nil {
		switch {
		case errors.Is(err, oidc.ErrUnsupportedGrant):
			writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", err.Error())
		case errors.Is(err, oidc.ErrInvalidGrant), errors.Is(err, oidc.ErrPKCEFailed):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		default:
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// redirectError reflects an OAuth error back to a VALIDATED redirect_uri.
func redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, code, desc)
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("error_description", desc)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}
