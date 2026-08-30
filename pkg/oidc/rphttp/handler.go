package rphttp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/everstacklabs/everstack/pkg/oidc"
)

// flowCookieName holds the in-flight state/nonce/verifier between login and
// callback. HttpOnly + short-lived; cleared on callback.
const flowCookieName = "evs_oidc_flow"

// SessionMinter creates the instance-local session once the id_token is
// verified. The instance owns this: it sets its OWN session cookie (instance
// domain, instance-bound), decoupled from the cloud session and DB.
type SessionMinter interface {
	Mint(w http.ResponseWriter, r *http.Request, claims *oidc.IDClaims) error
}

// Handler implements the RP login + callback endpoints.
type Handler struct {
	client       *OPClient
	verifier     *oidc.Verifier
	minter       SessionMinter
	postLogin    string
	cookieSecure bool
}

// New builds the RP handler. postLoginRedirect is where the user lands after a
// successful login (default "/").
func New(client *OPClient, verifier *oidc.Verifier, minter SessionMinter, postLoginRedirect string, cookieSecure bool) *Handler {
	if postLoginRedirect == "" {
		postLoginRedirect = "/"
	}
	return &Handler{client: client, verifier: verifier, minter: minter, postLogin: postLoginRedirect, cookieSecure: cookieSecure}
}

type flowState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
}

// Login starts the authorization-code flow: generate state/nonce/PKCE, stash
// them in an HttpOnly cookie, and redirect to the OP /authorize endpoint.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	state := randToken(24)
	nonce := randToken(24)
	verifier := randToken(48)
	challenge := s256(verifier)

	fs, _ := json.Marshal(flowState{State: state, Nonce: nonce, Verifier: verifier})
	http.SetCookie(w, &http.Cookie{
		Name:     flowCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(fs),
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((10 * time.Minute).Seconds()),
	})
	http.Redirect(w, r, h.client.AuthorizeURL(state, nonce, challenge), http.StatusFound)
}

// Callback completes the flow: validate state, exchange the code, verify the
// id_token (signature/iss/aud/exp/nonce), then mint the instance session.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if oauthErr := q.Get("error"); oauthErr != "" {
		http.Error(w, "sign-in failed: "+oauthErr, http.StatusForbidden)
		return
	}
	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	fs, ok := h.readFlow(r)
	if !ok {
		http.Error(w, "sign-in session expired; retry", http.StatusBadRequest)
		return
	}
	h.clearFlow(w)
	if subtleEqual(state, fs.State) != true {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	tok, err := h.client.Exchange(r.Context(), code, fs.Verifier)
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	claims, err := h.verifier.VerifyIDToken(r.Context(), tok.IDToken, fs.Nonce)
	if err != nil {
		http.Error(w, "id_token verification failed", http.StatusUnauthorized)
		return
	}
	if err := h.minter.Mint(w, r, claims); err != nil {
		http.Error(w, "session creation failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.postLogin, http.StatusFound)
}

func (h *Handler) readFlow(r *http.Request) (flowState, bool) {
	c, err := r.Cookie(flowCookieName)
	if err != nil || c.Value == "" {
		return flowState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return flowState{}, false
	}
	var fs flowState
	if err := json.Unmarshal(raw, &fs); err != nil {
		return flowState{}, false
	}
	return fs, true
}

func (h *Handler) clearFlow(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: flowCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// subtleEqual is a constant-time string compare for the state value.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
