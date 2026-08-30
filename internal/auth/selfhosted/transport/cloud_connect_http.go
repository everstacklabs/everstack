package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/jtiseen"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/golang-jwt/jwt/v5"
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

	user, _, err := h.selfHostedSvc.GetOrCreateUser(r.Context(), externalID, email, name, avatarURL)
	if err != nil {
		logger.WithError(err).Error("auth: exchange - failed to get or create user")
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

	orgID, _ := claims["organization_id"].(string)
	orgSlug, _ := claims["organization_slug"].(string)
	workspaceID, _ := claims["workspace_id"].(string)
	workspaceSlug, _ := claims["workspace_slug"].(string)
	instanceURL, _ := claims["instance_url"].(string)
	if err := h.selfHostedSvc.SetCloudManaged(r.Context(), orgID, orgSlug, workspaceID, workspaceSlug, instanceURL); err != nil {
		logger.WithError(err).Warn("auth: failed to persist cloud-managed auth mode")
	}
	if _, err := h.selfHostedSvc.EnsureUserHasConfiguredCloudOrganization(r.Context(), user.ID, nil); err != nil {
		logger.WithError(err).Warn("auth: exchange - failed to attach user to cloud organization")
	}

	h.selfHostedSvc.SetSessionCookieForRequest(w, r, session)
	logger.Infof("auth: exchange - user %s authenticated on instance via cloud token", user.Email)
	http.Redirect(w, r, "/", http.StatusFound)
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
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}

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

	exch, err := exchangeLaunchCode(r.Context(), authSvcURL, masterKeyB64, code, instanceHost(r))
	if err != nil {
		logger.WithError(err).Warn("auth: cloud-callback launch-code exchange failed")
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
	user, _, err := h.selfHostedSvc.GetOrCreateUser(r.Context(), exch.UserID, exch.Email, name, avatar)
	if err != nil {
		logger.WithError(err).Error("auth: cloud-callback - GetOrCreateUser failed")
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

	// Persist cloud-managed mode + workspace context so the rest of the
	// instance knows it's running under the cloud's org/workspace identity.
	if err := h.selfHostedSvc.SetCloudManaged(r.Context(), exch.OrganizationID, exch.OrganizationSlug, "", "", ""); err != nil {
		logger.WithError(err).Warn("auth: cloud-callback - SetCloudManaged failed")
	}
	if _, err := h.selfHostedSvc.EnsureUserHasConfiguredCloudOrganization(r.Context(), user.ID, nil); err != nil {
		logger.WithError(err).Warn("auth: cloud-callback - failed to attach user to cloud organization")
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
		"redirect_to": instanceSignoutRedirect(h.selfHostedSvc.CloudOrganizationSlug(r.Context())),
	})
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
