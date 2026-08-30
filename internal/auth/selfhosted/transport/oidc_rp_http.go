package transport

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/mux"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/oidc"
	"github.com/everstacklabs/everstack/pkg/oidc/rphttp"
)

// oidcSessionMinter creates an instance-local session from a verified id_token,
// mirroring the launch-code cloud-callback path but driven by standard OIDC. The
// instance owns its session (its own table + cookie), which is the whole point
// of the relying-party model: no shared cookie, no cross-DB session reads.
type oidcSessionMinter struct {
	h *SelfHostedAuthHandler
}

func (m *oidcSessionMinter) Mint(w http.ResponseWriter, r *http.Request, claims *oidc.IDClaims) error {
	var name *string
	if claims.Name != "" {
		n := claims.Name
		name = &n
	}
	// GetOrCreateUser keys on external_id (the cloud user id / id_token sub),
	// so repeat sign-ins resolve to the same local user.
	user, _, err := m.h.selfHostedSvc.GetOrCreateUser(r.Context(), claims.Subject, claims.Email, name, nil)
	if err != nil {
		return err
	}
	ip := getClientIP(r.Header)
	ua := r.UserAgent()
	session, err := m.h.selfHostedSvc.CreateSession(r.Context(), user.ID, &ip, &ua)
	if err != nil {
		return err
	}
	if claims.OrgID != "" {
		_ = m.h.selfHostedSvc.SetCloudManaged(r.Context(), claims.OrgID, claims.OrgSlug, "", "", "")
		_, _ = m.h.selfHostedSvc.EnsureUserHasConfiguredCloudOrganization(r.Context(), user.ID, nil)
	}
	m.h.selfHostedSvc.SetSessionCookieForRequest(w, r, session)
	logger.WithFields(
		"audit_event", true,
		"event_type", "session.oidc_callback.login",
		"user_id", claims.Subject,
		"email", user.Email,
		"org_id", claims.OrgID,
	).Info("auth: OIDC relying-party login")
	return nil
}

// The cloud OP issuer is shared across tenants, so its JWKS source is cached
// once per process.
var (
	jwksOnce sync.Once
	jwksSrc  oidc.JWKSSource
)

func oidcIssuer() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("EVS_OIDC_ISSUER")), "/")
}

// secureIssuer reports whether the OIDC issuer is safe to fetch signing keys
// from. https is always allowed; plaintext http is allowed only for loopback
// (local dev). A non-loopback http issuer lets a network attacker serve forged
// JWKS and thus mint id_tokens the relying party would accept, so it is refused.
func secureIssuer(issuer string) bool {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	default:
		return false
	}
}

func sharedJWKSSource(issuer string) oidc.JWKSSource {
	jwksOnce.Do(func() { jwksSrc = oidc.NewHTTPJWKSSource(issuer + oidc.PathJWKS) })
	return jwksSrc
}

// rpFor builds the per-request relying party. In shared mode the client_id is
// the tenant's instance_id (resolved from the tenant middleware context) and the
// redirect URI is the current host's /auth/oidc/callback.
func (h *SelfHostedAuthHandler) rpFor(r *http.Request) (*rphttp.Handler, bool) {
	issuer := oidcIssuer()
	if issuer == "" || !secureIssuer(issuer) {
		return nil, false
	}
	clientID := contextkeys.GetTenantID(r.Context())
	if clientID == "" {
		return nil, false
	}
	scheme := "https"
	if r.TLS == nil && (strings.HasPrefix(r.Host, "localhost") || strings.Contains(r.Host, "127.0.0.1")) {
		scheme = "http"
	}
	redirect := scheme + "://" + r.Host + "/auth/oidc/callback"
	opClient := rphttp.NewOPClient(issuer, clientID, redirect, "openid profile email org", http.DefaultClient)
	verifier := oidc.NewVerifier(issuer, clientID, sharedJWKSSource(issuer))
	return rphttp.New(opClient, verifier, &oidcSessionMinter{h: h}, "/", scheme == "https"), true
}

func (h *SelfHostedAuthHandler) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	rp, ok := h.rpFor(r)
	if !ok {
		http.Error(w, "oidc relying party not configured", http.StatusServiceUnavailable)
		return
	}
	rp.Login(w, r)
}

func (h *SelfHostedAuthHandler) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	rp, ok := h.rpFor(r)
	if !ok {
		http.Error(w, "oidc relying party not configured", http.StatusServiceUnavailable)
		return
	}
	rp.Callback(w, r)
}

// registerOIDCRelyingParty mounts the RP routes, gated by EVS_OIDC_ENABLED. Runs
// alongside the legacy cloud-callback/exchange flow for a dual-run rollout.
func (h *SelfHostedAuthHandler) registerOIDCRelyingParty(router *mux.Router) {
	if v := os.Getenv("EVS_OIDC_ENABLED"); v != "true" && v != "1" {
		return
	}
	if issuer := oidcIssuer(); issuer != "" && !secureIssuer(issuer) {
		logger.WithFields("issuer", issuer).Error("auth: EVS_OIDC_ISSUER must be https (http allowed only for loopback); refusing to mount OIDC relying-party routes")
		return
	}
	router.HandleFunc("/auth/oidc/login", h.handleOIDCLogin).Methods("GET")
	router.HandleFunc("/auth/oidc/callback", h.handleOIDCCallback).Methods("GET")
	logger.Info("auth: OIDC relying-party routes mounted (/auth/oidc/login, /auth/oidc/callback)")
}
