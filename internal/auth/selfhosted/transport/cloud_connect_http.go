package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/jtiseen"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/returnurl"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

// instanceExchangeSeen tracks single-use of instance-exchange JWTs by jti
// within their exp window. Process-local — fine for this single-replica
// per-instance backend. Without this, an intercepted redirect URL is
// replayable inside the 60s exp window.
var instanceExchangeSeen = jtiseen.New()

func (h *SelfHostedAuthHandler) handleCloudSignIn(w http.ResponseWriter, r *http.Request) {
	cloudURL := os.Getenv("EVS_CLOUD_URL")
	if cloudURL == "" {
		logger.Error("auth: /api/auth/signin called but EVS_CLOUD_URL not set")
		http.Error(w, `{"error":"cloud URL not configured"}`, http.StatusInternalServerError)
		return
	}

	instanceOrigin := instanceOriginFromRequest(r)
	if instanceOrigin == "" {
		http.Redirect(w, r, cloudURL+"/login", http.StatusFound)
		return
	}

	target := cloudURL + "/login?returnUrl=" + url.QueryEscape(instanceOrigin)
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *SelfHostedAuthHandler) handleTokenExchange(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Redirect(w, r, "/login?error=missing_token", http.StatusFound)
		return
	}

	masterKeyB64 := os.Getenv("EVS_M2M_SIGNING_KEY")
	if masterKeyB64 == "" {
		logger.Error("auth: exchange requested but EVS_M2M_SIGNING_KEY not set")
		http.Redirect(w, r, "/login?error=config_error", http.StatusFound)
		return
	}

	masterKey, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		logger.WithError(err).Error("auth: failed to decode m2m signing key")
		http.Redirect(w, r, "/login?error=config_error", http.StatusFound)
		return
	}
	signingKey := deriveInstanceTokenKey(masterKey)

	parsed, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return signingKey, nil
	},
		jwt.WithAudience("everstack-instance-exchange"),
		// Reject tokens with no exp claim, and tokens whose exp has passed.
		// Without this, a leaked instance-exchange token would be replayable
		// indefinitely. Mint side sets exp=now+60s; this enforces it.
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		logger.WithError(err).Warn("auth: instance token validation failed")
		http.Redirect(w, r, "/login?error=invalid_token", http.StatusFound)
		return
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		http.Redirect(w, r, "/login?error=invalid_token", http.StatusFound)
		return
	}

	// Bind the token to this instance before consuming its jti, so a token
	// presented at the wrong host is refused without burning the legitimate
	// redemption. The cloud names the target in instance_url; a token is
	// redeemable only on that host (and, on a managed gateway, only when the
	// host resolves to an instance of the token's organization). Without
	// this, a token minted for instance A (or for an attacker-chosen
	// instance_url via CompleteInstanceConnect) opened a session on B.
	requestScope, hasRequestScope := contextkeys.RequestInstanceScopeFromContext(r.Context())
	if err := bindExchangeTokenToInstance(claims, r, requestScope, hasRequestScope, enterprise.ManagedGateway()); err != nil {
		logger.WithFields(
			"audit_event", true,
			"event_type", "session.exchange.rejected",
			"host", r.Host,
			"reason", err.Error(),
		).Warn("auth: instance token not bound to this instance")
		http.Redirect(w, r, "/login?error=invalid_token", http.StatusFound)
		return
	}

	// Single-use enforcement via jti. Closes the in-window-replay gap left
	// by exp alone — even within the 60s exp window, the token can only
	// redeem once. Without this, an attacker who intercepts the redirect
	// URL within the exp window can re-trigger a session.
	jti, _ := claims["jti"].(string)
	var exp time.Time
	if expF, ok := claims["exp"].(float64); ok {
		exp = time.Unix(int64(expF), 0)
	}
	if !instanceExchangeSeen.CheckAndMark(jti, exp) {
		logger.Warnf("auth: instance token replay rejected (jti=%s)", jti)
		http.Redirect(w, r, "/login?error=token_replayed", http.StatusFound)
		return
	}

	externalID, _ := claims["external_id"].(string)
	email, _ := claims["email"].(string)
	if externalID == "" || email == "" {
		http.Redirect(w, r, "/login?error=invalid_token_claims", http.StatusFound)
		return
	}

	var name *string
	if v, ok := claims["name"].(string); ok && v != "" {
		name = &v
	}
	var avatarURL *string
	if v, ok := claims["avatar_url"].(string); ok && v != "" {
		avatarURL = &v
	}

	orgID, _ := claims["organization_id"].(string)
	orgSlug, _ := claims["organization_slug"].(string)
	workspaceID, _ := claims["workspace_id"].(string)
	workspaceSlug, _ := claims["workspace_slug"].(string)
	instanceURL, _ := claims["instance_url"].(string)
	user, err := h.admitCloudSignIn(r.Context(), cloudSignIn{
		ExternalID: externalID, Email: email, Name: name, AvatarURL: avatarURL,
		OrgID: orgID, OrgSlug: orgSlug, OrgRole: cloudOrganizationRole(claims, orgID),
		WorkspaceID: workspaceID, WorkspaceSlug: workspaceSlug, InstanceURL: instanceURL,
	})
	if err != nil {
		if errors.Is(err, service.ErrNoOrganization) {
			http.Redirect(w, r, "/login?error=no_organization", http.StatusFound)
			return
		}
		logger.WithError(err).Error("auth: exchange - failed to admit user")
		http.Redirect(w, r, "/login?error=user_error", http.StatusFound)
		return
	}

	ipAddress := getClientIP(r.Header)
	userAgent := r.UserAgent()
	session, err := h.selfHostedSvc.CreateSession(r.Context(), user.ID, &ipAddress, &userAgent)
	if err != nil {
		logger.WithError(err).Error("auth: exchange - failed to create session")
		http.Redirect(w, r, "/login?error=session_error", http.StatusFound)
		return
	}

	h.selfHostedSvc.SetSessionCookieForRequest(w, r, session)
	logger.Infof("auth: exchange - user %s authenticated on instance via cloud token", user.Email)
	http.Redirect(w, r, "/", http.StatusFound)
}

// recordCloudLogin persists that this instance is cloud-managed after a
// cloud-brokered sign-in.
//
// A self-hosted gateway connected to the cloud is one instance with one
// auth_config row, so the row records the cloud org/workspace and the user is
// attached to that org.
//
// A managed gateway serves every tenant from one process and one auth_config
// row. Writing the signing-in tenant's org into that row on every login made
// it name whichever tenant logged in last, and attaching the user to "the
// configured cloud organization" then read that row back and upserted the
// user as OWNER of whatever org it named: a member was promoted to owner of
// their own org on every login, and two concurrent logins could make a user
// owner of the other tenant's org. On a managed gateway, membership already
// lives in the platform's organization_members (the cloud verified it before
// minting the token or launch code), so the only thing recorded is the
// tenant-independent fact that the gateway is in cloud mode, which keeps
// local registration and password login disabled.
//
// orgRole is the user's role in that organization as the cloud vouched for
// it in this sign-in; the local membership takes that role (never a blanket
// owner), or least privilege when the cloud named none.
func (h *SelfHostedAuthHandler) recordCloudLogin(ctx context.Context, userID uuid.UUID, orgID, orgSlug, orgRole, workspaceID, workspaceSlug, instanceURL string) {
	if enterprise.ManagedGateway() {
		if err := h.selfHostedSvc.MarkCloudManaged(ctx); err != nil {
			logger.WithError(err).Warn("auth: failed to persist cloud-managed auth mode")
		}
		return
	}
	if err := h.selfHostedSvc.SetCloudManaged(ctx, orgID, orgSlug, workspaceID, workspaceSlug, instanceURL); err != nil {
		logger.WithError(err).Warn("auth: failed to persist cloud-managed auth mode")
	}
	if _, err := h.selfHostedSvc.EnsureUserHasConfiguredCloudOrganization(ctx, userID, orgRole, nil); err != nil {
		logger.WithError(err).Warn("auth: failed to attach user to cloud organization")
	}
}

// cloudSignIn is a sign-in the cloud vouched for: the cloud identity and,
// when the cloud named one, the organization (with the user's role in it)
// the user enters this instance through.
type cloudSignIn struct {
	ExternalID, Email                       string
	Name, AvatarURL                         *string
	OrgID, OrgSlug, OrgRole                 string
	WorkspaceID, WorkspaceSlug, InstanceURL string
}

// admitCloudSignIn resolves the local user for a cloud sign-in and decides,
// before any session exists, whether they may have one. With an
// organization, the user is created or linked and attached to it with
// their cloud role (recordCloudLogin; it also persists cloud-managed mode
// and context). On a self-hosted gateway the user must then belong to an
// organization here; a sign-in that names none is checked against an
// existing account without writing anything, so a refusal never leaves an
// account behind. A managed gateway's memberships are the platform's and are
// checked per request. Every cloud sign-in path (exchange token, launch
// code, OIDC) goes through here.
func (h *SelfHostedAuthHandler) admitCloudSignIn(ctx context.Context, in cloudSignIn) (*domain.User, error) {
	managed := enterprise.ManagedGateway()
	if !managed && in.OrgID == "" {
		user, err := h.selfHostedSvc.FindCloudUser(ctx, in.ExternalID, in.Email)
		if err != nil {
			return nil, err
		}
		if user == nil {
			return nil, service.ErrNoOrganization
		}
		if err := h.requireMembership(ctx, user.ID); err != nil {
			return nil, err
		}
		return user, nil
	}
	user, _, err := h.selfHostedSvc.GetOrCreateUser(ctx, in.ExternalID, in.Email, in.Name, in.AvatarURL)
	if err != nil {
		return nil, err
	}
	h.recordCloudLogin(ctx, user.ID, in.OrgID, in.OrgSlug, in.OrgRole, in.WorkspaceID, in.WorkspaceSlug, in.InstanceURL)
	if !managed {
		if err := h.requireMembership(ctx, user.ID); err != nil {
			return nil, err
		}
	}
	return user, nil
}

// requireMembership returns service.ErrNoOrganization unless userID belongs
// to an organization here.
func (h *SelfHostedAuthHandler) requireMembership(ctx context.Context, userID uuid.UUID) error {
	withOrgs, err := h.selfHostedSvc.GetUserWithOrganizations(ctx, userID)
	if err != nil {
		return err
	}
	if withOrgs == nil || len(withOrgs.Organizations) == 0 {
		return service.ErrNoOrganization
	}
	return nil
}

// cloudOrganizationRole returns the user's role in orgID from an instance
// exchange token's "orgs" claim (a JSON list of {id, slug, name, role}), or
// "" when the token does not say.
func cloudOrganizationRole(claims jwt.MapClaims, orgID string) string {
	raw, _ := claims["orgs"].(string)
	if raw == "" || orgID == "" {
		return ""
	}
	var orgs []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(raw), &orgs); err != nil {
		return ""
	}
	for _, o := range orgs {
		if strings.EqualFold(o.ID, orgID) {
			return o.Role
		}
	}
	return ""
}

// bindExchangeTokenToInstance verifies that an instance-exchange token was
// issued for the instance serving this request. Both cloud mint paths
// (instance-token and CompleteInstanceConnect) always set instance_url and
// tokens live 60s, so a token without one is refused outright.
//
// The token's instance_url host must equal the request host. On a managed
// gateway that host must also resolve to an instance (scope, set by the
// gateway's host resolution) owned by the token's organization. A
// self-hosted gateway is a single instance and may sit behind a proxy that
// rewrites Host, so there X-Forwarded-Host is accepted too, matching how
// handleCloudSignIn built the origin the cloud was told to return to.
func bindExchangeTokenToInstance(claims jwt.MapClaims, r *http.Request, scope contextkeys.RequestInstanceScope, hasScope, managed bool) error {
	instanceURL, _ := claims["instance_url"].(string)
	instanceURL = strings.TrimSpace(instanceURL)
	if instanceURL == "" {
		return errors.New("token names no instance")
	}
	parsed, err := url.Parse(instanceURL)
	if err != nil || parsed.Host == "" {
		return errors.New("token instance_url is not an absolute URL")
	}
	tokenHost := normalizeExchangeHost(parsed.Host)
	if tokenHost == "" {
		return errors.New("token instance_url has no host")
	}

	hostMatches := tokenHost == normalizeExchangeHost(r.Host)
	if !managed && !hostMatches {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			hostMatches = tokenHost == normalizeExchangeHost(fwd)
		}
	}
	if !hostMatches {
		return errors.New("token was issued for a different instance")
	}
	if !managed {
		return nil
	}

	if !hasScope {
		return errors.New("request host is not a managed instance")
	}
	tokenOrg, _ := claims["organization_id"].(string)
	if !sameOrganization(tokenOrg, scope.OrganizationID) {
		return errors.New("token organization does not own this instance")
	}
	return nil
}

// sameOrganization reports whether a and b are the same, valid org UUID.
func sameOrganization(a, b string) bool {
	aID, err := uuid.Parse(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	bID, err := uuid.Parse(strings.TrimSpace(b))
	return err == nil && aID == bID
}

// normalizeExchangeHost canonicalises a host the same way the gateway's
// tenant resolution does: lowercase, no port, no trailing dot.
func normalizeExchangeHost(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

func instanceOriginFromRequest(r *http.Request) string {
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		proto := r.Header.Get("X-Forwarded-Proto")
		if proto == "" {
			proto = "http"
		}
		return proto + "://" + fwdHost
	}
	if r.Host == "" {
		return ""
	}
	proto := "http"
	if r.TLS != nil {
		proto = "https"
	}
	return proto + "://" + r.Host
}

func deriveInstanceTokenKey(masterKey []byte) []byte {
	h := hkdf.New(sha256.New, masterKey, nil, []byte("everstack-m2m-client-instance-token"))
	key := make([]byte, 32)
	if _, err := h.Read(key); err != nil {
		panic(fmt.Sprintf("failed to derive instance token key: %v", err))
	}
	return key
}

// ─── Cloud-callback (launch-code) flow ──────────────────────────────────────
//
// The cloud relay (services/auth/internal/transport/connect/instanceRelayHandler)
// mints a single-use launch code and 302s the browser to
//
//	https://<tenant-host>/auth/cloud-callback?code=<plaintext>&next=<path>
//
// We trade the code back-channel via POST /internal/m2m/exchange-launch-code on
// the cloud auth service (signed with an m2m bearer JWT) for the user identity,
// then mint our OWN local builtin session via SelfHostedAuthService. This is
// the Model 1 flow — tenant owns its own session table — distinct from the
// older Model 2 flow in internal/controlplane/cloud_callback.go which bridges
// cloud session tokens via TenantMiddleware.

// launchCodeExchangeAudience must match the audience required by the cloud
// auth service's /internal/m2m/exchange-launch-code handler.
const launchCodeExchangeAudience = "everstack-launch-code-exchange"

// launchCodeExchangeResponse mirrors the cloud-side response shape.
type launchCodeExchangeResponse struct {
	UserID           string `json:"user_id"`
	Email            string `json:"email"`
	Name             string `json:"name,omitempty"`
	AvatarURL        string `json:"avatar_url,omitempty"`
	OrganizationID   string `json:"organization_id,omitempty"`
	OrganizationSlug string `json:"organization_slug,omitempty"`
	// OrganizationRole is the user's role in that organization. Older
	// clouds do not send it.
	OrganizationRole string `json:"organization_role,omitempty"`
	CodeID           string `json:"code_id"`
}

func (h *SelfHostedAuthHandler) handleCloudCallback(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Redirect(w, r, "/login?error=missing_code", http.StatusFound)
		return
	}

	// `next` is the post-login destination on this tenant; must be a relative
	// path so an attacker can't smuggle in an open-redirect via the URL.
	next := returnurl.Path(r.URL.Query().Get("next"), "/")

	masterKeyB64 := os.Getenv("EVS_M2M_SIGNING_KEY")
	if masterKeyB64 == "" {
		logger.Error("auth: cloud-callback requires EVS_M2M_SIGNING_KEY but none set")
		http.Redirect(w, r, "/login?error=config_error", http.StatusFound)
		return
	}

	authSvcURL := strings.TrimSpace(os.Getenv("EVS_AUTH_SERVICE_URL"))
	if authSvcURL == "" {
		// Fall back to the in-cluster service URL. Most deployments set this
		// via Helm; the fallback keeps local dev working when nothing is set.
		authSvcURL = strings.TrimSpace(os.Getenv("EVS_SERVICES_AUTH_URL"))
	}
	if authSvcURL == "" {
		logger.Error("auth: cloud-callback requires EVS_AUTH_SERVICE_URL or EVS_SERVICES_AUTH_URL but neither is set")
		http.Redirect(w, r, "/login?error=config_error", http.StatusFound)
		return
	}

	// The cloud bound the launch code to the host it redirected to. On a
	// managed gateway, redeem it for the host this request was served and
	// resolved on (normalized r.Host, never X-Forwarded-Host), and only when
	// that host is one of our instances.
	managed := enterprise.ManagedGateway()
	callbackHost := instanceHost(r)
	requestScope, hasRequestScope := contextkeys.RequestInstanceScopeFromContext(r.Context())
	if managed {
		if !hasRequestScope {
			logger.WithFields("host", r.Host).Warn("auth: cloud-callback on a host that is not a managed instance")
			http.Redirect(w, r, "/login?error=invalid_code", http.StatusFound)
			return
		}
		callbackHost = normalizeExchangeHost(r.Host)
	}

	exch, err := exchangeLaunchCode(r.Context(), authSvcURL, masterKeyB64, code, callbackHost)
	if err != nil {
		logger.WithError(err).Warn("auth: cloud-callback launch-code exchange failed")
		http.Redirect(w, r, "/login?error=invalid_code", http.StatusFound)
		return
	}
	if managed && !sameOrganization(exch.OrganizationID, requestScope.OrganizationID) {
		logger.WithFields(
			"audit_event", true,
			"event_type", "session.cloud_callback.rejected",
			"host", r.Host,
			"reason", "launch code organization does not own this instance",
		).Warn("auth: cloud-callback launch code not bound to this instance")
		http.Redirect(w, r, "/login?error=invalid_code", http.StatusFound)
		return
	}

	var name *string
	if exch.Name != "" {
		n := exch.Name
		name = &n
	}
	var avatar *string
	if exch.AvatarURL != "" {
		a := exch.AvatarURL
		avatar = &a
	}

	// GetOrCreateUser keys on external_id (the cloud user id). Idempotent on
	// repeat sign-ins — same external_id → same local user row.
	user, err := h.admitCloudSignIn(r.Context(), cloudSignIn{
		ExternalID: exch.UserID, Email: exch.Email, Name: name, AvatarURL: avatar,
		OrgID: exch.OrganizationID, OrgSlug: exch.OrganizationSlug, OrgRole: exch.OrganizationRole,
	})
	if err != nil {
		if errors.Is(err, service.ErrNoOrganization) {
			http.Redirect(w, r, "/login?error=no_organization", http.StatusFound)
			return
		}
		logger.WithError(err).Error("auth: cloud-callback - admitting user failed")
		http.Redirect(w, r, "/login?error=user_error", http.StatusFound)
		return
	}

	ipAddress := getClientIP(r.Header)
	userAgent := r.UserAgent()
	session, err := h.selfHostedSvc.CreateSession(r.Context(), user.ID, &ipAddress, &userAgent)
	if err != nil {
		logger.WithError(err).Error("auth: cloud-callback - CreateSession failed")
		http.Redirect(w, r, "/login?error=session_error", http.StatusFound)
		return
	}

	h.selfHostedSvc.SetSessionCookieForRequest(w, r, session)
	logger.WithFields(
		"audit_event", true,
		"event_type", "session.cloud_callback.login",
		"user_id", exch.UserID,
		"email", user.Email,
		"code_id", exch.CodeID,
		"ip", ipAddress,
	).Info("auth: cloud-callback login")

	http.Redirect(w, r, next, http.StatusFound)
}

// handleInstanceSignout ends the browser's session on *this instance only*.
// It deletes the instance-local session row minted by /auth/cloud-callback and
// expires the instance session cookie. The cloud session is deliberately left
// alone: instance and cloud lifecycles are decoupled (signing out of an app
// does not sign you out of the IdP), and the cloud cookie lives at the parent
// domain where an instance has no business issuing deletes. "Sign out
// everywhere" is the cloud's own sign-out button, which cascades.
//
// The response carries `redirect_to` pointing at the cloud's instances picker.
// The FE follows it rather than navigating to this instance's /login, because
// that page hands straight back to the cloud relay — which, still holding a
// valid cloud session, signs the user right back in and makes signout look
// like a no-op. Landing on the cloud breaks that loop.
//
// Contract with the FE (apps/admin/src/hooks/auth/use-auth.ts): any non-2xx is
// surfaced as "could not sign out" and client state is kept, so a failed
// session delete MUST be a 500 here. A silent success that leaves the session
// row alive is the exact bug this endpoint exists to avoid.
func (h *SelfHostedAuthHandler) handleInstanceSignout(w http.ResponseWriter, r *http.Request) {
	token := h.getSessionToken(r.Header)
	if token != "" {
		if err := h.selfHostedSvc.SignOut(r.Context(), token); err != nil {
			logger.WithError(err).Error("auth: instance-signout - failed to delete session")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "signout failed"})
			return
		}
	}

	// Expire the cookie even when no session row was found. A stale cookie
	// left in the jar keeps the SPA re-checking a session that no longer
	// exists, which reads as "sign out did nothing".
	h.selfHostedSvc.ClearSessionCookieForRequest(w, r)

	// Deleting the instance session is not enough on its own: the browser
	// still carries the cloud's parent-domain cookie, which this instance's
	// auth fallbacks accept. The marker makes the instance refuse it, so the
	// user lands on the cloud and stays there until they deliberately
	// re-enter through the relay.
	h.selfHostedSvc.SetInstanceSignedOutMarker(w, r)

	logger.WithFields(
		"audit_event", true,
		"event_type", "session.instance.signout",
		"host", instanceHost(r),
		"had_session", token != "",
	).Info("auth: instance signout")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"redirect_to": instanceSignoutRedirect(h.signoutOrganizationSlug(r)),
	})
}

// signoutOrganizationSlug names the org whose instances picker to land on
// after sign-out. On a managed gateway the host-resolved instance knows its
// own org; the auth_config row is shared by every tenant and must not be
// consulted there.
func (h *SelfHostedAuthHandler) signoutOrganizationSlug(r *http.Request) string {
	if scope, ok := contextkeys.RequestInstanceScopeFromContext(r.Context()); ok {
		return strings.TrimSpace(scope.OrganizationSlug)
	}
	if enterprise.ManagedGateway() {
		return ""
	}
	return h.selfHostedSvc.CloudOrganizationSlug(r.Context())
}

// instanceSignoutRedirect builds the cloud URL to land on after an instance
// sign-out: the owning org's instances picker when we know the slug, the cloud
// root when we don't. Returns "" when no cloud is configured (pure self-hosted)
// — the FE falls back to its own /login, which has no relay to loop through.
func instanceSignoutRedirect(orgSlug string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("EVS_CLOUD_URL")), "/")
	if base == "" {
		return ""
	}
	if orgSlug != "" {
		return base + "/" + orgSlug + "/instances"
	}
	return base
}

// exchangeLaunchCode POSTs the code to the auth service back-channel and
// returns the user identity bound to it. Single-use is enforced server-side;
// the cloud rejects duplicate redemptions with 4xx.
func exchangeLaunchCode(ctx context.Context, authSvcURL, m2mKeyB64, code, host string) (*launchCodeExchangeResponse, error) {
	bearer, err := mintLaunchCodeExchangeBearer(m2mKeyB64)
	if err != nil {
		return nil, fmt.Errorf("mint bearer: %w", err)
	}

	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"instance_host": host,
	})

	endpoint := strings.TrimRight(authSvcURL, "/") + "/internal/m2m/exchange-launch-code"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("exchange status %d: %s", resp.StatusCode, string(raw))
	}

	var er launchCodeExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, err
	}
	return &er, nil
}

// mintLaunchCodeExchangeBearer mints a 30-second JWT signed with the
// launch-code-exchange m2m client key. Mirror of the cloud-side
// derivation in internal/controlplane/cloud_callback.go.
func mintLaunchCodeExchangeBearer(m2mKeyB64 string) (string, error) {
	masterKey, err := base64.StdEncoding.DecodeString(m2mKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode signing key: %w", err)
	}
	signingKey := deriveLaunchCodeExchangeKey(masterKey)

	now := time.Now()
	claims := jwt.MapClaims{
		"aud": launchCodeExchangeAudience,
		"iat": now.Unix(),
		"exp": now.Add(30 * time.Second).Unix(),
		"iss": "tenant-gateway",
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(signingKey)
}

// deriveLaunchCodeExchangeKey produces the per-client signing key. The HKDF
// info string MUST match deriveLaunchCodeExchangeKey on the auth service
// (services/auth) and deriveLaunchCodeExchangeKeyTenant in the controlplane
// helper, otherwise the cloud rejects the bearer.
func deriveLaunchCodeExchangeKey(masterKey []byte) []byte {
	h := hkdf.New(sha256.New, masterKey, nil, []byte("everstack-m2m-client-launch-code-exchange"))
	out := make([]byte, 32)
	if _, err := io.ReadFull(h, out); err != nil {
		// Mirrors the original helper: return master key as fallback rather
		// than panicking. In practice HKDF-SHA256 on a non-empty input never
		// fails, but the fallback is a defensive copy that won't crash the
		// gateway during sign-in.
		return masterKey
	}
	return out
}

// instanceHost returns the host the cloud bound the launch code to. The cloud
// relay binds bare host (no port). We honor X-Forwarded-Host first because
// the gateway sits behind ingress-nginx in cluster deployments.
func instanceHost(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		return fwd
	}
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}
