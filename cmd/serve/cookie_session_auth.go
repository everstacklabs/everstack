package serve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/tenantstatus"
	"github.com/jmoiron/sqlx"
)

// activeOrgHeader is the FE-supplied signal for which organization a
// multi-membership user wants to operate as. Mirrors the header
// `ExtractTenantID` already reads from gRPC metadata so the contract
// is symmetric between cookie auth and API-key auth.
const activeOrgHeader = "x-org-id"

// instanceTokenExchangePath is where the cloud sends the browser with a
// short-lived instance-exchange token (internal/auth/selfhosted/transport
// handleTokenExchange).
const instanceTokenExchangePath = "/api/auth/exchange"

// cloudCallbackPath is where the cloud relay sends the browser with a
// single-use launch code (handleCloudCallback). Like the token exchange it
// authenticates a credential against the host-selected instance.
const cloudCallbackPath = "/auth/cloud-callback"

// cookieSessionCookieAliases mirrors tenant_middleware.tenantSessionCookieAliases
// in the services binary. The gateway accepts the same set so a session
// minted by either pod is honored on the same browser.
var cookieSessionCookieAliases = []string{
	"es_tenant_session",
	"es_everstack_session",
	"everstack_session",
}

type cookieOrganizationMembership struct {
	OrganizationID string `db:"organization_id"`
	Role           string `db:"role"`
}

// cookieSessionAuthMiddleware enriches the request context with the cloud
// user's UUID, the tenant-authenticated flag, and the resolved tenant
// ID when a valid session cookie is present.
//
// Why this exists: the services pod runs the full TenantMiddleware which
// validates cloud sessions and sets CloudUserID + TenantAuthenticated +
// TenantID on context. The gateway pod (this binary) has no equivalent —
// cookie-authenticated requests landed here with cloud_user_id="" and
// resolveUserID fell through to "admin", AND the API-key interceptor
// rejected them outright because TenantAuthenticated was unset. Surface:
// "Reconnecting to sandbox..." in the agent chat tab because every
// GetSandboxInstance/etc. RPC came back with an auth error, which the
// FE renders as sandboxError.
//
// Tenant resolution: after the post-2026-05-06 P0 fix, handlers refuse
// any request whose context lacks a tenant id (no more body-supplied
// `tenant_id` fallback, no more "first org in DB" fallback in
// multi-tenant deployments). To keep cookie-authenticated browser
// traffic working, this middleware also looks up the user's
// organization memberships and picks the active one. See pickActiveOrg
// for the policy — it's intentionally strict: single-membership users
// auto-resolve, multi-membership users must send `x-org-id`, anything
// else leaves TenantID unset so the handler returns PermissionDenied
// (fail closed).
//
// Scope: this middleware sets CloudUserID + TenantAuthenticated +
// TenantID + the verified membership role. It does NOT redirect on missing cookie, NOT mirror
// cookies, NOT touch shadow sessions — those belong to the full
// TenantMiddleware. We just stop the "cookie present but ignored" leak.
//
// Session validation is sessionlive.Lookup, the same check
// tenant_middleware.validateCloudSession uses: the mirror row must be
// unexpired and, when linked to a Better Auth session, that session must
// still exist and be unexpired.
//
// db is resolved through the supplied accessor closure so the middleware
// can be installed before the DB pool is opened — the closure returns
// nil until the pool is ready, and on nil we just skip enrichment for
// that request.
func cookieSessionAuthMiddleware(dbFn func() *sqlx.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The cloud instance-token exchange and the launch-code callback
			// authenticate the presented credential against the host-selected
			// instance, so it needs that
			// instance regardless of which cookies the browser carries: a
			// user arriving from the cloud login holds the parent-domain
			// cloud cookie, one re-entering after sign-out holds the
			// signed-out marker, and one whose session expired holds a dead
			// cookie. Every cookie branch below would drop the host scope,
			// so resolve it here and skip cookie enrichment entirely: the
			// handler never reads cookie identity, and host resolution alone
			// does not mark the request authenticated.
			if r.URL.Path == instanceTokenExchangePath || r.URL.Path == cloudCallbackPath {
				next.ServeHTTP(w, withResolvedRequestInstance(r, dbFn()))
				return
			}
			// Cheap short-circuits first so we never call dbFn() (or
			// hit the DB) on requests that don't need enrichment.
			if uid := contextkeys.CloudUserIDFromContext(r.Context()); uid != "" {
				next.ServeHTTP(w, r)
				return
			}
			// A browser that signed out of this instance gets no identity
			// from any cookie until it re-enters through the cloud relay
			// (which mints a fresh session and clears the marker). Without
			// this the parent-domain cloud cookie keeps authenticating API
			// calls after sign-out, so the SPA bounces to the cloud while
			// the RPCs behind it still succeed — a sign-out that is only
			// skin deep.
			if _, err := r.Cookie(authdomain.InstanceSignedOutCookie); err == nil {
				// The marker is caller-controlled, so it must never be a
				// way to skip host rejection: a signed-out browser gets no
				// identity from ANY cookie, including the API-key
				// interceptor's own (cached) cookie branch (EVS-309 review).
				if len(readSessionTokens(r)) > 0 {
					r = r.WithContext(contextkeys.WithTenantAuthRejected(r.Context()))
				}
				next.ServeHTTP(w, withShellInstanceScope(r, dbFn))
				return
			}
			tokens := readSessionTokens(r)
			if len(tokens) == 0 {
				if requestNeedsInstanceScope(r) {
					next.ServeHTTP(w, withResolvedRequestInstance(r, dbFn()))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			db := dbFn()
			if db == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Bound the lookup so a slow DB doesn't add latency to
			// every request. 1s matches the tenant_middleware's
			// session lookup deadline, which has held in prod.
			lookupCtx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
			defer cancel()

			// Try every alias: an invalid (stale) cookie under one name
			// must neither shadow a valid one under another nor leave the
			// request for a downstream cookie branch to re-authenticate.
			// sessionlive.Lookup also rejects a mirror row whose Better
			// Auth session is gone (sign-out, revocation, user deletion).
			var userID string
			var storeUnavailable bool
			for _, token := range tokens {
				session, err := sessionlive.Lookup(lookupCtx, db, token)
				if err != nil {
					if !sessionlive.IsRejected(err) {
						storeUnavailable = true
						logger.WithError(err).Warn("cookie_session_auth: session verification failed")
					}
					continue
				}
				userID = session.UserID
				break
			}
			if userID == "" {
				// A session cookie was presented and none validated. Mark
				// the request so the API-key interceptors do not honour a
				// cached answer for the same cookie. When the store could
				// not be reached, also say so: the request is still denied
				// cookie identity, but an authenticated route answers 503
				// rather than 401 (public routes are unaffected).
				ctx := contextkeys.WithTenantAuthRejected(r.Context())
				if storeUnavailable {
					ctx = contextkeys.WithSessionUnavailable(ctx)
				}
				next.ServeHTTP(w, withShellInstanceScope(r.WithContext(ctx), func() *sqlx.DB { return db }))
				return
			}

			// Resolve tenant identity. In cloud mode each instance
			// subdomain maps to a tenant_config row whose instance_id
			// is the isolation boundary. Try that first; fall back to
			// org membership for self-hosted (no tenant_config table)
			// or non-instance hosts.
			var tenantID string
			var userRole string
			var tenantOK bool
			var requestInstance contextkeys.RequestInstanceScope

			hostResolved := false
			var hostInstance contextkeys.RequestInstanceScope
			if host := r.Host; host != "" {
				resolvedInstance, err := resolveInstanceFromHost(lookupCtx, db, host)
				if err == nil && resolvedInstance.InstanceID != "" {
					hostResolved = true
					hostInstance = resolvedInstance
					// Verify the authenticated user belongs to this
					// instance's org before trusting the hostname.
					var role string
					membershipErr := db.GetContext(lookupCtx, &role,
						`SELECT COALESCE(role, '') FROM everstack.organization_members
						 WHERE user_id = $1 AND organization_id = $2::uuid
						 LIMIT 1`, userID, resolvedInstance.OrganizationID)
					if membershipErr == nil {
						// Only a verified member gets the host's instance
						// scope. Stamping it before this check handed a
						// non-member the victim's org/instance ids, which
						// GetSession synthesis and GetLicenseMonitorStatus
						// then echoed back (EVS-309).
						requestInstance = resolvedInstance
						tenantID = resolvedInstance.InstanceID
						userRole = strings.TrimSpace(role)
						tenantOK = true
					}
				}
			}

			if hostResolved && !tenantOK {
				// The Host selected a real instance and this user is not a
				// member of its organization (or the check failed). Fail
				// closed: no identity, no tenant, no scope. Falling back to
				// the user's own org would let them operate under a
				// hostname they are not authorized for. The rejection marker
				// stops the API-key interceptor's own cookie branch from
				// re-authenticating the same cookie as the user's first
				// tenant.
				logger.WithFields(
					"audit_event", true,
					"event_type", "session.tenant.rejected",
					"cloud_user_id", userID,
					"host", r.Host,
					"reason", "not a member of the host instance's organization",
				).Info("cookie_session_auth: host membership rejected")
				ctx := contextkeys.WithTenantAuthRejected(r.Context())
				// The shell still learns the host is a managed instance (from
				// the row resolved above), so the visit is recorded anonymously.
				if isAnonymousShellLoad(r) && hostInstance.OrganizationID != "" {
					ctx = contextkeys.WithRequestInstanceScope(ctx, hostInstance)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if !hostResolved && enterprise.ManagedGateway() {
				// On a managed gateway every request belongs to a
				// hostname-selected tenant. A Host that resolves to nothing
				// must not drop into the self-hosted membership fallback:
				// that path marked the request authenticated even with no
				// tenant, which alone skips API-key and license validation
				// on handlers that do no tenant check (EVS-309 review).
				logger.WithFields(
					"audit_event", true,
					"event_type", "session.tenant.rejected",
					"cloud_user_id", userID,
					"host", r.Host,
					"reason", "host does not resolve to a managed instance",
				).Info("cookie_session_auth: unresolved host on managed gateway")
				next.ServeHTTP(w, r.WithContext(contextkeys.WithTenantAuthRejected(r.Context())))
				return
			}

			if !tenantOK {
				// Fallback: org membership resolution for self-hosted
				// deployments that don't have tenant_config.
				var memberships []cookieOrganizationMembership
				if err := db.SelectContext(lookupCtx, &memberships,
					`SELECT organization_id::text AS organization_id, COALESCE(role, '') AS role
					 FROM everstack.organization_members
					 WHERE user_id = $1`, userID); err != nil {
					logger.WithError(err).SetFields(
						"cloud_user_id", userID,
					).Warn("cookie_session_auth: membership lookup failed — request will land without tenant context")
				}
				membershipIDs := make([]string, 0, len(memberships))
				for _, membership := range memberships {
					membershipIDs = append(membershipIDs, membership.OrganizationID)
				}
				headerOrg := r.Header.Get(activeOrgHeader)
				tenantID, tenantOK = pickActiveOrg(membershipIDs, headerOrg)
				if tenantOK {
					for _, membership := range memberships {
						if membership.OrganizationID == tenantID {
							userRole = strings.TrimSpace(membership.Role)
							break
						}
					}
				}
				if !tenantOK {
					// No verified tenant: do not mark the request
					// authenticated. The self-hosted API-key interceptor
					// still recognises the cookie for pre-onboarding screens;
					// this middleware only ever vouches for a verified
					// membership.
					logger.WithFields(
						"cloud_user_id", userID,
						"membership_count", len(memberships),
						"header_org_present", headerOrg != "",
					).Warn("cookie_session_auth: tenant unresolved — request continues unauthenticated")
					next.ServeHTTP(w, r)
					return
				}
			}

			ctx := r.Context()
			if requestInstance.InstanceID != "" {
				ctx = contextkeys.WithRequestInstanceScope(ctx, requestInstance)
			}
			ctx = contextkeys.WithCloudUserID(ctx, userID)
			ctx = contextkeys.WithTenantAuthenticated(ctx)
			if tenantID != "" {
				ctx = contextkeys.WithTenantID(ctx, tenantID)
				ctx = database.WithTenantSchema(ctx, tenantID)
			}
			if userRole != "" {
				ctx = contextkeys.WithUserRole(ctx, userRole)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requestNeedsInstanceScope identifies unauthenticated transport steps that
// must still carry the hostname-selected instance into later authentication.
// Host resolution alone never marks the request authenticated.
func requestNeedsInstanceScope(r *http.Request) bool {
	if r == nil {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
		return true
	}
	// Token exchanges: the handler authenticates the presented code/token
	// against the host-selected instance. The pre-auth status RPCs
	// (GetLicenseMonitorStatus, GetGatewayInstanceStatus, GetTrialStatus)
	// deliberately are NOT here: they have no legitimate consumer of an
	// unauthenticated instance scope, and carrying one let a Host sweep
	// read every tenant's ids and sandbox billing flag (EVS-309).
	switch r.URL.Path {
	case "/oauth/token", "/oauth/revoke",
		"/everstack.auth.v1.AuthService/ExchangeDeviceCode":
		return true
	}

	return isSPAShellRequest(r)
}

// isSPAShellRequest identifies browser loads of the SPA shell. The SPA
// handler injects instance-specific runtime configuration into that HTML, so
// it needs the host-selected instance. Resolve extensionless browser routes,
// including the root shell, while keeping static assets and health probes on
// the no-DB fast path.
func isSPAShellRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet {
		return false
	}
	path := strings.TrimSpace(r.URL.Path)
	if path == "" || path == "/" || path == "/index.html" {
		return true
	}
	if strings.HasPrefix(path, "/assets/") || path == "/favicon.ico" || path == "/manifest.json" ||
		path == "/health" || path == "/healthz" {
		return false
	}
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") ||
		strings.HasPrefix(path, "/everstack.") || strings.HasPrefix(path, "/oauth/") {
		return false
	}
	return filepath.Ext(path) == ""
}

// withShellInstanceScope resolves the host-selected instance onto a request
// that is being served without identity (no session, a rejected or foreign
// session, or a browser signed out of this instance), but only for a load
// of the SPA shell. The scope is the resolved tenant_config row, never the
// Host string, and resolution alone grants nothing: the shell uses it only
// to decide that this is a managed instance (the runtime env then serves
// the analytics key, with no tenant ids, so signed-out visits are recorded
// anonymously). API, RPC and asset requests are unchanged.
func withShellInstanceScope(r *http.Request, dbFn func() *sqlx.DB) *http.Request {
	if !isAnonymousShellLoad(r) {
		return r
	}
	if _, ok := contextkeys.RequestInstanceScopeFromContext(r.Context()); ok {
		return r
	}
	return withResolvedRequestInstance(r, dbFn())
}

func isAnonymousShellLoad(r *http.Request) bool {
	return isSPAShellRequest(r) && strings.TrimSpace(r.Header.Get("Authorization")) == ""
}

func withResolvedRequestInstance(r *http.Request, db *sqlx.DB) *http.Request {
	if r == nil || db == nil || strings.TrimSpace(r.Host) == "" {
		return r
	}
	lookupCtx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	requestInstance, err := resolveInstanceFromHost(lookupCtx, db, r.Host)
	if err != nil || requestInstance.InstanceID == "" || requestInstance.OrganizationID == "" {
		return r
	}
	ctx := contextkeys.WithRequestInstanceScope(r.Context(), requestInstance)
	return r.WithContext(ctx)
}

// pickActiveOrg returns the organization id this request should be
// scoped to, given the cookie-user's verified memberships and the
// FE-supplied "active org" header.
//
// Policy (intentionally strict — this is the post-P0 surface):
//
//   - 0 memberships → ("", false). Either the user belongs to no org or
//     the membership query failed; the handler must 403, not guess.
//   - Header set: only honored when it matches one of the verified
//     memberships. Mismatch → ("", false). The header is just a hint;
//     the trust anchor is the membership query (`WHERE user_id = $1`),
//     never client-supplied data.
//   - Header empty + exactly one membership → that membership.
//   - Header empty + multiple memberships → ("", false). Picking
//     "first by joined_at" is the same shape as the P0 fallback that
//     leaked a stranger's tenant — we do NOT reintroduce it.
//
// Multi-membership users therefore MUST send `x-org-id`. Today the
// admin FE has only a single-org concept (use-auth.ts always reads
// `organizations[0]`), so the membership-of-one branch covers every
// real user; the strict multi-org branch is a guardrail for the day
// the FE adds an org switcher.
func pickActiveOrg(memberships []string, headerOrg string) (string, bool) {
	if len(memberships) == 0 {
		return "", false
	}
	headerOrg = strings.TrimSpace(headerOrg)
	if headerOrg != "" {
		for _, m := range memberships {
			if m == headerOrg {
				return m, true
			}
		}
		// Header set but doesn't match any verified membership. Fail
		// closed — never silently fall back to a different membership,
		// even if the user has only one. A mismatched header signals
		// stale FE state at best, a forged header at worst.
		return "", false
	}
	if len(memberships) == 1 {
		return memberships[0], true
	}
	return "", false
}

// resolveInstanceFromHost extracts the host label from r.Host and looks up
// the matching tenant_config row to get the instance_id (the per-instance
// isolation boundary) and organization_id (for membership verification).
// Returns an error when the host doesn't match a known instance. The caller
// falls back to org-membership resolution for self-hosted compatibility.
func resolveInstanceFromHost(ctx context.Context, db *sqlx.DB, host string) (contextkeys.RequestInstanceScope, error) {
	host = normalizeRequestHost(host)
	// Extract the first DNS label (e.g. "stage-d4e5f6" from
	// "stage-d4e5f6.dev.eu-gra-1.everstack.ai").
	label := host
	if idx := strings.IndexByte(host, '.'); idx >= 0 {
		label = host[:idx]
	}
	if label == "" {
		return contextkeys.RequestInstanceScope{}, fmt.Errorf("empty host label")
	}
	// A database with no tenant_config table (any self-hosted gateway)
	// resolves no host at all. Answer as the failing query did, without
	// paying for it on every request.
	if missingTenantConfigTable.known(db, "") {
		return contextkeys.RequestInstanceScope{}, errNoTenantConfigTable
	}
	// A recently confirmed unknown host answers exactly like the first miss
	// without another unindexed scan (EVS-324 item 7). Label misses are
	// keyed by label; a label that exists under a different full host is
	// keyed by that full host, which never collides with a label (it has a
	// dot) and so never hides the canonical spelling.
	if unknownHostCache.known(db, label) || unknownHostCache.known(db, host) {
		return contextkeys.RequestInstanceScope{}, sql.ErrNoRows
	}

	var row struct {
		contextkeys.RequestInstanceScope
		GatewayURL string `db:"gateway_url"`
	}
	err := db.GetContext(ctx, &row,
		`SELECT
			tc.instance_id::text AS instance_id,
			tc.organization_id::text AS organization_id,
			COALESCE(o.slug, '') AS organization_slug,
			COALESCE(tc.gateway_url, '') AS gateway_url
		 FROM everstack.tenant_config AS tc
		 LEFT JOIN everstack.organizations AS o ON o.id = tc.organization_id
		 WHERE tc.slug || '-' || RIGHT(REPLACE(tc.instance_id::text, '-', ''), 6) = $1`,
		label)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			unknownHostCache.remember(db, label)
		} else if isUndefinedRelation(err) {
			missingTenantConfigTable.remember(db, "")
		}
		return contextkeys.RequestInstanceScope{}, err
	}
	// The label is only the first DNS label: the full host must be the
	// tenant's canonical host, or "acme-a1b2c3.attacker.example" would name
	// the acme tenant.
	if !tenantstatus.HostMatchesCanonical(host, row.GatewayURL) {
		// Only a dotted host can be remembered: a dotless host IS the
		// label, and caching it would lock the real tenant out.
		if host != label {
			unknownHostCache.remember(db, host)
		}
		return contextkeys.RequestInstanceScope{}, sql.ErrNoRows
	}
	return row.RequestInstanceScope, nil
}

// normalizeRequestHost canonicalises a Host header before tenant lookup:
// lowercase (DNS is case-insensitive, the DB compare is not), no port, no
// trailing dot. Every host comparison must go through the same
// normalization or an uppercased spelling of a real tenant misses the lookup
// and lands in a different code path than the canonical spelling.
func normalizeRequestHost(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

// readSessionTokens returns every session-cookie value presented, in alias
// priority order. Callers validate each in turn: a stale cookie under one
// alias must not shadow a valid one under another.
func readSessionTokens(r *http.Request) []string {
	var tokens []string
	for _, name := range cookieSessionCookieAliases {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			tokens = append(tokens, c.Value)
		}
	}
	return tokens
}
