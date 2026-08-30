package serve

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	authdomain "github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// activeOrgHeader is the FE-supplied signal for which organization a
// multi-membership user wants to operate as. Mirrors the header
// `ExtractTenantID` already reads from gRPC metadata so the contract
// is symmetric between cookie auth and API-key auth.
const activeOrgHeader = "x-org-id"

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
// db lookup query mirrors tenant_middleware.validateCloudSession:
//
//	SELECT user_id FROM everstack.sessions
//	WHERE token = $1 AND expires_at > NOW()
//
// db is resolved through the supplied accessor closure so the middleware
// can be installed before the DB pool is opened — the closure returns
// nil until the pool is ready, and on nil we just skip enrichment for
// that request.
func cookieSessionAuthMiddleware(dbFn func() *sqlx.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				next.ServeHTTP(w, r)
				return
			}
			token := readSessionToken(r)
			if token == "" {
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

			var userID string
			err := db.GetContext(lookupCtx, &userID,
				`SELECT user_id::text FROM everstack.sessions
				 WHERE token = $1 AND expires_at > NOW() LIMIT 1`, token)
			if err != nil {
				if err != sql.ErrNoRows {
					logger.WithError(err).Debug("cookie_session_auth: lookup failed (non-row)")
				}
				next.ServeHTTP(w, r)
				return
			}
			if userID == "" {
				next.ServeHTTP(w, r)
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

			if host := r.Host; host != "" {
				resolvedInstance, err := resolveInstanceFromHost(lookupCtx, db, host)
				if err == nil && resolvedInstance.InstanceID != "" {
					requestInstance = resolvedInstance
					// Verify the authenticated user belongs to this
					// instance's org before trusting the hostname.
					var role string
					membershipErr := db.GetContext(lookupCtx, &role,
						`SELECT COALESCE(role, '') FROM everstack.organization_members
						 WHERE user_id = $1 AND organization_id = $2::uuid
						 LIMIT 1`, userID, resolvedInstance.OrganizationID)
					if membershipErr == nil {
						tenantID = resolvedInstance.InstanceID
						userRole = strings.TrimSpace(role)
						tenantOK = true
					}
				}
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
					logger.WithFields(
						"cloud_user_id", userID,
						"membership_count", len(memberships),
						"header_org_present", headerOrg != "",
					).Warn("cookie_session_auth: tenant unresolved — handler will return PermissionDenied")
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
	switch r.URL.Path {
	case "/oauth/token", "/oauth/revoke",
		"/everstack.auth.v1.AuthService/ExchangeDeviceCode",
		"/everstack.gateway.v1.GatewayService/GetLicenseMonitorStatus",
		"/everstack.gateway.v1.GatewayService/GetGatewayInstanceStatus",
		"/everstack.gateway.v1.GatewayService/GetTrialStatus",
		"/everstack.gateway.v1.GatewayService/GetGateway",
		"/everstack.config.v1.ConfigService/GetRuntimeConfigSection":
		return true
	}

	// The SPA handler injects instance-specific runtime configuration into HTML.
	// Resolve extensionless browser routes, including the root shell, while
	// keeping static assets and health probes on the no-DB fast path.
	if r.Method != http.MethodGet {
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
	// Strip port if present.
	if idx := strings.IndexByte(host, ':'); idx >= 0 {
		host = host[:idx]
	}
	// Extract the first DNS label (e.g. "stage-a668c8" from
	// "stage-a668c8.dev.eu-gra-1.everstack.ai").
	label := host
	if idx := strings.IndexByte(host, '.'); idx >= 0 {
		label = host[:idx]
	}
	if label == "" {
		return contextkeys.RequestInstanceScope{}, fmt.Errorf("empty host label")
	}

	var scope contextkeys.RequestInstanceScope
	err := db.GetContext(ctx, &scope,
		`SELECT
			tc.instance_id::text AS instance_id,
			tc.organization_id::text AS organization_id,
			COALESCE(o.slug, '') AS organization_slug
		 FROM everstack.tenant_config AS tc
		 LEFT JOIN everstack.organizations AS o ON o.id = tc.organization_id
		 WHERE tc.slug || '-' || RIGHT(REPLACE(tc.instance_id::text, '-', ''), 6) = $1`,
		label)
	if err != nil {
		return contextkeys.RequestInstanceScope{}, err
	}
	return scope, nil
}

// readSessionToken returns the first session-cookie value found, trying
// each alias in priority order. Browsers may send multiple cookies with
// the same name when a domain migration is in progress — we don't try to
// disambiguate, the first valid one is enough for user_id resolution.
func readSessionToken(r *http.Request) string {
	for _, name := range cookieSessionCookieAliases {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}
