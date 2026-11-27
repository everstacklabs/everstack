package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/api/surfaceauth"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway/chat"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	apikey "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

const (
	// Error response structure
	ErrorResponseType = "error"
)

// APIKeyInterceptor validates API keys and adds correlation IDs
type APIKeyInterceptor struct {
	allowMissing bool
	policy       *apilic.Policy
	cache        map[string]apiKeyCacheEntry
	mu           sync.RWMutex
	cacheTTL     time.Duration
	sessionDB    *sqlx.DB // direct DB session validation for self-hosted mode
	sessionCache sync.Map // map[token]sessionAuth
	// lookups shares concurrent key lookups and briefly remembers failed
	// ones, so a struggling database is not asked by every request.
	lookups *apikeylib.LookupGuard
}

// NewAPIKeyInterceptor creates a new API key interceptor with policy loaded from global config
func NewAPIKeyInterceptor(allowMissing bool) *APIKeyInterceptor {
	interceptor := &APIKeyInterceptor{
		allowMissing: allowMissing,
		policy:       apilic.FromGlobal(),
		cache:        make(map[string]apiKeyCacheEntry, 1024),
		cacheTTL:     60 * time.Second,
		lookups:      apikeylib.NewLookupGuard(apikeylib.DefaultFailureWindow),
	}
	interceptor.registerCacheInvalidation()
	return interceptor
}

type apiKeyCacheEntry struct {
	valid bool
	orgID string
	// keyID is the api_keys.id of a valid key, for spend attribution.
	keyID        string
	surfaces     []string
	rateLimitRPM *int
	expiresAt    time.Time
}

// sessionAuth carries the bits of a validated session cookie that downstream
// handlers need: the user id, and the user's primary organisation id (the
// first org they joined). Both are stamped into the request context so
// tenant-scoped handlers don't have to rediscover them — and so they can
// stop trusting the request body for tenant id, which is what caused the
// 2026-05-06 cross-tenant leak.
type sessionAuth struct {
	valid     bool
	userID    string
	tenantID  string
	role      string
	expiresAt time.Time
	// unavailable marks a lookup that could not reach the session store.
	// Such a result is a deny and is never cached.
	unavailable bool
}

// sessionCacheTTL bounds how long a session-cookie answer is reused,
// independently of the API-key cacheTTL. It must not exceed
// sessionlive.MaxCacheTTL: a cached "valid" keeps a revoked Better Auth
// session working until it expires.
const sessionCacheTTL = sessionlive.MaxCacheTTL

// WithAPIKeyValidation wraps an http.Handler with API key validation
func (i *APIKeyInterceptor) WithAPIKeyValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Detect endpoint type from path and generate appropriate correlation ID
		path := r.URL.Path
		endpoint := correlation.DetectEndpointFromPath(path)
		corrStart := time.Now()
		ctx, correlationID := correlation.EnsureEndpointCorrelationID(r.Context(), endpoint)
		r = r.WithContext(ctx)
		corrDuration := time.Since(corrStart)

		// Add correlation ID to response headers
		w.Header().Set(correlation.CorrelationIDHeader, correlationID)

		// Check policy-based bypass (configured in services/config/config.yaml).
		// The evs.run hosting surface (isAnonymousHostingPath) is auth-OPTIONAL
		// rather than bypassed outright: with a credential present we fall
		// through to normal validation so the handler sees the caller's tenant,
		// which is required to republish a slug they already own; with no
		// credential the request proceeds anonymously and the handler rate
		// limits by IP.
		policyBypass := i.policy != nil && i.policy.ShouldBypassPath(path)
		anonHosting := isAnonymousHostingPath(path)
		if (policyBypass || anonHosting) && extractAPIKeyFromHeaders(r.Header) == "" {
			// For hosting, only bypass when the caller presented NO credential
			// at all. An API key or a Bearer (device-auth JWT) must fall
			// through to normal validation so the publish is attributed to the
			// caller's tenant rather than treated as anonymous.
			if !anonHosting || (extractAPIKeyFromHeaders(r.Header) == "" && !hasAuthBearer(r.Header)) {
				logger.WithFields(map[string]interface{}{
					"path":           path,
					"correlation_id": correlationID,
				}).Debug("Bypassing API key validation per policy")
				next.ServeHTTP(w, r)
				return
			}
		}

		// Skip API key validation for Vite dev server assets (HMR, client, etc.)
		if isViteDevServerPath(path) && extractAPIKeyFromHeaders(r.Header) == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Host selection alone is not authentication. Recheck key identities.
		if contextkeys.IsTenantAuthenticated(r.Context()) {
			if contextkeys.GetAPIKeyHash(r.Context()) != "" && !surfaceauth.Request(contextkeys.GetAPIKeySurfaces(r.Context()), r) {
				i.writeErrorResponse(w, http.StatusForbidden, "this API key cannot call this API", correlationID)
				return
			}
			if contextkeys.GetAPIKeyHash(r.Context()) != "" {
				ctx := r.Context()
				ctx = apikeylib.Accept(ctx, contextkeys.GetAPIKeyID(ctx), contextkeys.GetTenantID(ctx), contextkeys.GetAPIKeySurfaces(ctx), apikeylib.SurfaceInference, contextkeys.GetAPIKeyRateLimit(ctx))
				if i.rateLimited(w, ctx) {
					return
				}
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
			return
		}

		// Self-hosted session auth: validate the session cookie directly
		// against the DB. This is how the admin UI authenticates, and it also
		// supplies the user's tenant id so tenant-scoped handlers downstream
		// see a real tenant context. Without it every admin UI page would 403
		// with "tenant context missing", which is the regression we hit after
		// locking down the body-supplied tenant id fallback on 2026-05-06.
		// A cookie the tenant middleware already refused for this host (non
		// member, or unresolved host on a managed gateway) must not be
		// re-authenticated here as the user's own tenant (EVS-309).
		sessionUnavailable := contextkeys.IsSessionUnavailable(r.Context())
		if i.sessionDB != nil && !contextkeys.IsTenantAuthRejected(r.Context()) {
			if cookie, err := r.Cookie("es_everstack_session"); err == nil && cookie.Value != "" {
				auth := i.resolveSession(r.Context(), cookie.Value)
				if auth.valid {
					r = applySessionAuth(r, auth)
					next.ServeHTTP(w, r)
					return
				}
				sessionUnavailable = sessionUnavailable || auth.unavailable
			}
		}

		// Loopback calls this process makes to its own API (eval runner,
		// scorers, dataset generation hitting /v1/chat/completions). These
		// carry a process-local token from internalauth that an external
		// caller cannot produce.
		//
		// This branch used to trigger on `Sec-Fetch-Site: same-origin`, which
		// is not a credential: the header is forbidden to scripts but any non
		// browser client sets it freely. That made the x-tenant-id read below
		// an unauthenticated cross-tenant read/write primitive. See
		// internal/api/internalauth for the replacement.
		if internalauth.IsInternalRequest(r) || i.allowMissing {
			ctx := contextkeys.WithTenantAuthenticated(r.Context())
			// Honor the caller-supplied tenant so downstream provider-config
			// and metrics queries resolve. Safe here only because the internal
			// token proves the caller is this process.
			if tid := strings.TrimSpace(r.Header.Get("x-tenant-id")); tid != "" {
				// Set both tenant context keys (see cookie_session_auth.go for
				// the longer rationale — emission and query paths read different
				// keys, missing TenantSchema is why the Logs page came up empty
				// in cloud mode).
				ctx = contextkeys.WithTenantID(ctx, tid)
				ctx = database.WithTenantSchema(ctx, tid)
			}
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			return
		}

		// Validate API key presence
		validateStart := time.Now()

		// Deployment invoke endpoints use their own Bearer-token auth (deployment API keys)
		if strings.HasPrefix(path, "/v1/deploy/") {
			next.ServeHTTP(w, r)
			return
		}

		// The inbound MCP server (/mcp) authenticates each request via its own
		// Bearer path — the tenant's Everstack API key — inside the MCP handler.
		// Let it through here so the standard Bearer rejection below doesn't 401
		// external MCP clients (Claude Desktop, Cursor, ADK) before they reach
		// it. Mirrors the /v1/deploy/ bypass above.
		if path == "/mcp" {
			next.ServeHTTP(w, r)
			return
		}

		// The A2A server (/a2a/...) likewise authenticates each request via its
		// own Bearer path (tenant Everstack API key) inside the A2A handlers.
		if strings.HasPrefix(path, "/a2a/") {
			next.ServeHTTP(w, r)
			return
		}

		// The OTLP/HTTP ingest receivers (/v1/{traces,logs,metrics} and the
		// /api/public/otel/v1/* aliases) authenticate via their own Bearer
		// API-key path (otlp.WithTenantAuth) — same rationale as /mcp: the
		// shared gateway does not inject a per-request tenant into context, so
		// these endpoints resolve the tenant from the key themselves.
		if isOTLPIngestPath(path) {
			next.ServeHTTP(w, r)
			return
		}

		// Explicitly reject Authorization header for non-exempt routes.
		// An issued API key sent as Bearer to an OpenAI verb under /v1 never
		// gets here: the /v1 mount moves it onto the api-key header first
		// (see withV1OpenAICompat in internal/api). What remains is a
		// device-auth JWT, a session token, or a key on a route that takes
		// only the api-key header, so name both accepted forms.
		if hasAuthBearer(r.Header) {
			i.writeErrorResponse(w, http.StatusUnauthorized,
				"Invalid authorization token; use "+common.EverstackApiKey+
					", or send an Everstack API key as Authorization: Bearer to the OpenAI-compatible endpoints under /v1",
				correlationID)
			return
		}
		if !i.validateAPIKey(r) {
			if sessionUnavailable {
				// The only credential was a session cookie the session
				// store could not verify: deny, as 503 rather than 401 so
				// an outage does not read as a sign-out.
				w.Header().Set("Retry-After", "1")
				i.writeErrorResponse(w, http.StatusServiceUnavailable, "session verification unavailable, retry shortly", correlationID)
				return
			}
			i.writeErrorResponse(w, http.StatusUnauthorized, "API key is required. Create one from the dashboard (Settings > Vault > API Keys). Docs: https://docs.everstack.ai/getting-started/vault/api-keys", correlationID)
			return
		}
		validateDuration := time.Since(validateStart)

		// If a CQRS system is in context, validate API key exists (by hash) and not revoked. Mandatory.
		cqrsStart := time.Now()
		sys, err := cqrs.GetSystemFromContext(ctx)
		if err != nil || sys == nil {
			i.writeErrorResponse(w, http.StatusUnauthorized, "API key validation unavailable", correlationID)
			return
		}
		cqrsDuration := time.Since(cqrsStart)

		// Extract and hash API key
		extractStart := time.Now()
		apiKey := extractAPIKeyFromHeaders(r.Header)

		// Fast-path bloom filter is consulted only for fast rejection.
		// "Valid" hits previously short-circuited the request without ever
		// looking at which tenant the key belonged to — the bloom has no
		// payload — so every API-key call entered downstream handlers with
		// no tenant in context. We now always fall through to the cache/DB
		// path so we can recover the org id and install it on the context.
		if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
			_, definitelyInvalid := engine.ValidateAPIKey(apiKey)
			if definitelyInvalid {
				i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
				return
			}
		}

		// Prefer HMAC hash when configured; else fallback to legacy
		hash := ""
		if hv, ok := apikeylib.HashFromContext(r.Context(), apiKey); ok {
			hash = hv
		} else {
			hash = chat.HashAPIKey(apiKey)
		}
		extractDuration := time.Since(extractStart)

		// Cache fast-path
		cacheStart := time.Now()
		if orgID, keyID, surfaces, rpm, valid, hit := i.lookupCache(hash); hit {
			// A key of a deleted managed instance stops working even while
			// its positive answer is cached (managed gateways only).
			if valid && tenantlive.Deleted(orgID) {
				valid = false
			}
			// A valid key on another tenant's hostname is answered exactly
			// like an unknown key. The cache keeps the positive answer: the
			// key still works on its own host.
			if valid && keyRefusedOnHost(r, orgID, hash) {
				valid = false
			}
			if valid {
				if !surfaceauth.Request(surfaces, r) {
					i.writeErrorResponse(w, http.StatusForbidden, "this API key cannot call this API", correlationID)
					return
				}
				cacheDuration := time.Since(cacheStart)
				totalDuration := time.Since(start)
				logger.WithFields(map[string]interface{}{
					"correlation_id":       correlationID,
					"corr_duration_us":     corrDuration.Microseconds(),
					"validate_duration_us": validateDuration.Microseconds(),
					"cqrs_duration_us":     cqrsDuration.Microseconds(),
					"extract_duration_us":  extractDuration.Microseconds(),
					"cache_duration_us":    cacheDuration.Microseconds(),
					"total_duration_us":    totalDuration.Microseconds(),
					"cache_hit":            true,
				}).Debug("API key validation profiling (cache hit)")
				authCtx := contextkeys.WithAPIKeyRateLimit(contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(r.Context(), orgID, hash, keyID), surfaces), rpm)
				if orgID != "" {
					authCtx = database.WithTenantSchema(authCtx, orgID)
				}
				authCtx = apikeylib.Accept(authCtx, contextkeys.GetAPIKeyID(authCtx), contextkeys.GetTenantID(authCtx), surfaces, apikeylib.SurfaceInference, rpm)
				if i.rateLimited(w, authCtx) {
					return
				}
				r = r.WithContext(authCtx)
				next.ServeHTTP(w, r)
				return
			}
			i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
			return
		}
		cacheDuration := time.Since(cacheStart)

		// Database lookup
		revision := apikeylib.CacheRevision()
		dbStart := time.Now()
		q := apikey.NewGetApiKeyByHashQuery(hash, "", "")
		q.CallerEnforces = true
		resp, qerr := i.lookups.Do(ctx, hash, func(lctx context.Context) (interface{}, error) {
			return sys.QueryBus.Execute(lctx, q)
		})
		if qerr != nil {
			// The lookup failed (database unreachable, out of connections,
			// timed out), so whether the key is valid is unknown. Refuse this
			// request as retryable and do not cache it as invalid: that
			// turned one refused connection into a minute of 401s for every
			// caller using a valid key.
			w.Header().Set("Retry-After", "1")
			i.writeErrorResponse(w, http.StatusServiceUnavailable, apikeylib.ErrLookupUnavailable.Error(), correlationID)
			return
		}
		if resp == nil {
			i.rememberCache(hash, false, "", "", nil)
			i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
			return
		}
		var resolvedOrgID, resolvedKeyID string
		var surfaces []string
		var rpm *int
		if qr, ok := resp.(*query.Response); ok {
			if qr.Data == nil {
				i.rememberCache(hash, false, "", "", nil)
				i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
				return
			}
			if rm, ok := qr.Data.(apikey.APIKeyReadModel); ok {
				resolvedKeyID = rm.ID
				surfaces = rm.Surfaces
				rpm = rm.RateLimitRPM
				if rm.InstanceID != nil && *rm.InstanceID != "" {
					resolvedOrgID = *rm.InstanceID
				} else if rm.OrgID != nil {
					resolvedOrgID = *rm.OrgID
				}
			} else {
				i.rememberCache(hash, false, "", "", nil)
				i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
				return
			}
			if tenantlive.Deleted(resolvedOrgID) {
				i.rememberCache(hash, false, "", "", nil)
				i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
				return
			}
			if !i.rememberCacheAt(revision, hash, true, resolvedOrgID, resolvedKeyID, surfaces, rpm) {
				i.writeErrorResponse(w, http.StatusUnauthorized, "API key changed; retry request", correlationID)
				return
			}

			// Mark as valid in fast-path cache
			if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
				engine.MarkAPIKeyValid(apiKey)
			}
			if keyRefusedOnHost(r, resolvedOrgID, hash) {
				i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
				return
			}
		} else {
			i.rememberCache(hash, false, "", "", nil)
			i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
			return
		}
		if !surfaceauth.Request(surfaces, r) {
			i.writeErrorResponse(w, http.StatusForbidden, "this API key cannot call this API", correlationID)
			return
		}
		dbDuration := time.Since(dbStart)
		authCtx := contextkeys.WithAPIKeyRateLimit(contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(r.Context(), resolvedOrgID, hash, resolvedKeyID), surfaces), rpm)
		if resolvedOrgID != "" {
			authCtx = database.WithTenantSchema(authCtx, resolvedOrgID)
		}
		authCtx = apikeylib.Accept(authCtx, contextkeys.GetAPIKeyID(authCtx), contextkeys.GetTenantID(authCtx), surfaces, apikeylib.SurfaceInference, rpm)
		if i.rateLimited(w, authCtx) {
			return
		}
		r = r.WithContext(authCtx)

		totalDuration := time.Since(start)
		logger.WithFields(map[string]interface{}{
			"correlation_id":       correlationID,
			"corr_duration_us":     corrDuration.Microseconds(),
			"validate_duration_us": validateDuration.Microseconds(),
			"cqrs_duration_us":     cqrsDuration.Microseconds(),
			"extract_duration_us":  extractDuration.Microseconds(),
			"cache_duration_us":    cacheDuration.Microseconds(),
			"db_duration_us":       dbDuration.Microseconds(),
			"total_duration_us":    totalDuration.Microseconds(),
			"cache_hit":            false,
		}).Debug("API key validation profiling (cache miss)")

		// Call the next handler
		next.ServeHTTP(w, r)
	})
}

// keyRefusedOnHost reports whether a valid key that resolves to tenantID must
// be refused because the request's hostname names a different tenant (see
// contextkeys.HostScopeAdmitsTenant). Without this, tenant A's key sent to
// tenant B's hostname ran as A while host-based attribution, limits and
// last-use tracking filed it under B. Callers answer with the same 401 as an
// unknown key, so the response does not reveal that the key is valid
// elsewhere. Only tenant and host metadata are logged.
func keyRefusedOnHost(r *http.Request, tenantID, hash string) bool {
	if contextkeys.HostScopeAdmitsTenant(r.Context(), tenantID) {
		return false
	}
	hostInstance := ""
	if scope, ok := contextkeys.HostSelectedTenantFromContext(r.Context()); ok {
		hostInstance = scope.InstanceID
	} else if scope, ok := contextkeys.RequestInstanceScopeFromContext(r.Context()); ok {
		hostInstance = scope.InstanceID
	}
	logger.WithFields(map[string]interface{}{
		"audit_event":   true,
		"event_type":    "apikey.host_mismatch",
		"key_tenant_id": tenantID,
		"host_instance": hostInstance,
		"host":          r.Host,
		"path":          r.URL.Path,
	}).Warn("apikey: key presented on another tenant's host; refused as invalid")
	return true
}

// isViteDevServerPath checks if the request is for Vite dev server assets
// that should bypass API key validation during development
func isViteDevServerPath(path string) bool {
	vitePatterns := []string{
		"/@vite/",
		"/@react-refresh",
		"/@fs/",
		"/src/",
		"/node_modules/",
		"/@id/",
		"/__vite_ping",
	}
	for _, pattern := range vitePatterns {
		if strings.HasPrefix(path, pattern) || strings.Contains(path, pattern) {
			return true
		}
	}
	// Also allow common static assets
	if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") ||
		strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".ts") ||
		strings.HasSuffix(path, ".jsx") || strings.HasSuffix(path, ".map") {
		return true
	}
	return false
}

// lookupCache returns the identity, surfaces, limit, validity and cache hit.
// Carrying orgID in the cache lets
// the wrapper install it on the request context after a warm-cache validation,
// without re-querying the DB. Without that, every API-key authenticated
// request would still need a follow-up lookup to know which tenant it
// belongs to — or, worse, fall through with no tenant context at all.
func (i *APIKeyInterceptor) lookupCache(hash string) (string, string, []string, *int, bool, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if e, ok := i.cache[hash]; ok {
		if time.Now().Before(e.expiresAt) {
			return e.orgID, e.keyID, e.surfaces, e.rateLimitRPM, e.valid, true
		}
	}
	return "", "", nil, nil, false, false
}

func (i *APIKeyInterceptor) rememberCache(hash string, valid bool, orgID, keyID string, surfaces []string, rateLimits ...*int) {
	i.rememberCacheAt(apikeylib.CacheRevision(), hash, valid, orgID, keyID, surfaces, rateLimits...)
}
func (i *APIKeyInterceptor) rememberCacheAt(revision uint64, hash string, valid bool, orgID, keyID string, surfaces []string, rateLimits ...*int) bool {
	var rpm *int
	if len(rateLimits) > 0 {
		rpm = rateLimits[0]
	}
	return apikeylib.WithCacheRevision(revision, func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		i.cache[hash] = apiKeyCacheEntry{valid: valid, orgID: orgID, keyID: keyID, surfaces: surfaces, rateLimitRPM: rpm, expiresAt: time.Now().Add(apikeylib.CacheTTL(surfaces, i.cacheTTL))}
	})
}
func (i *APIKeyInterceptor) registerCacheInvalidation() {
	apikeylib.RegisterCacheInvalidator(func(hash, keyID string) {
		i.mu.Lock()
		defer i.mu.Unlock()
		for k, entry := range i.cache {
			if hash != "" && k == hash || keyID != "" && entry.keyID == keyID {
				delete(i.cache, k)
			}
		}
	})
}

func extractAPIKeyFromHeaders(h http.Header) string {
	// Canonical x-evs-api-key, falling back to legacy x-mf-api-key and
	// x-everstack-api-key for backward compatibility with deployed clients.
	return common.GetHTTPHeader(h, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey)
}

func hasAuthBearer(hdr http.Header) bool {
	authHeader := hdr.Get(common.Authorization)
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		key := strings.TrimPrefix(authHeader, "Bearer ")
		if key != "" {
			return true
		}
	}
	return false
}

// isOTLPIngestPath reports whether the path is one of the OTLP/HTTP ingest
// receivers, which do their own Bearer API-key auth (otlp.WithTenantAuth) and
// must therefore be exempt from the standard Bearer rejection above — mirroring
// the /mcp and /a2a/ exemptions.
func isOTLPIngestPath(path string) bool {
	switch path {
	case "/v1/traces", "/v1/logs", "/v1/metrics",
		"/api/public/otel/v1/traces", "/api/public/otel/v1/logs", "/api/public/otel/v1/metrics":
		return true
	}
	return false
}

// isAnonymousHostingPath reports whether the path belongs to the evs.run
// hosting anonymous surface (publish/finalize/claim/request-code/verify-code,
// REST and Connect forms). These are auth-optional: see the policy-bypass block
// above. The authenticated site CRUD (/v1/sites, GetSite/ListSites/UpdateSite/
// DeleteSite) is deliberately NOT matched — only the exact
// "/v1/sites/{slug}/claim" shape, which prefix matching in the policy cannot
// express.
func isAnonymousHostingPath(path string) bool {
	switch {
	case path == "/v1/hosting/request-code", path == "/v1/hosting/verify-code":
		return true
	case path == "/v1/publish", strings.HasPrefix(path, "/v1/publish/"):
		return true
	case strings.HasPrefix(path, "/v1/sites/") && strings.HasSuffix(path, "/claim"):
		return true
	case strings.HasPrefix(path, "/everstack.hosting.v1.SitesService/"):
		switch strings.TrimPrefix(path, "/everstack.hosting.v1.SitesService/") {
		case "PublishSite", "FinalizeSite", "ClaimSite", "RequestCode", "VerifyCode":
			return true
		}
	}
	return false
}

// validateAPIKey checks if the request has an API key
func (i *APIKeyInterceptor) validateAPIKey(r *http.Request) bool {
	// If missing API keys are allowed, skip validation
	if i.allowMissing {
		return true
	}

	// Accept the API key in the canonical x-evs-api-key header or the legacy
	// x-mf-api-key / x-everstack-api-key names (see extractAPIKeyFromHeaders).
	return extractAPIKeyFromHeaders(r.Header) != ""
}

// writeErrorResponse writes an error response with correlation ID
func (i *APIKeyInterceptor) writeErrorResponse(w http.ResponseWriter, statusCode int, message string, correlationID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(correlation.CorrelationIDHeader, correlationID)
	w.WriteHeader(statusCode)

	errorResponse := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    ErrorResponseType,
			"message": message,
			"code":    statusCode,
		},
	}

	responseJSON, err := json.Marshal(errorResponse)
	if err != nil {
		logger.WithError(err).Error("Failed to marshal error response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Write(responseJSON)
}

// NewAPIKeyInterceptorWithSessionDB creates an interceptor with direct DB session validation
// for self-hosted instances.
func NewAPIKeyInterceptorWithSessionDB(allowMissing bool, db *sqlx.DB) *APIKeyInterceptor {
	interceptor := &APIKeyInterceptor{
		allowMissing: allowMissing,
		policy:       apilic.FromGlobal(),
		cache:        make(map[string]apiKeyCacheEntry, 1024),
		cacheTTL:     60 * time.Second,
		sessionDB:    db,
		lookups:      apikeylib.NewLookupGuard(apikeylib.DefaultFailureWindow),
	}
	interceptor.registerCacheInvalidation()
	return interceptor
}

// resolveSession validates a session token against the sessions table and
// resolves the user's primary organisation id. The org id is the missing
// piece that lets downstream handlers read tenant from context only — see
// the comment on sessionAuth for the rationale. Results are cached for
// sessionCacheTTL because every admin UI request goes through here.
//
// Org selection: pick the user's earliest-joined organisation. The admin
// UI today reads `session.user.organizations[0]`, which is also ordered by
// joined_at. A future "active org" concept (header / cookie field) would
// override this; until then we mirror what the FE already does.
func (i *APIKeyInterceptor) resolveSession(ctx context.Context, token string) sessionAuth {
	// A hostname-selected instance changes the answer, so it is part of the
	// cache key: the same cookie on tenant B's host must not reuse tenant A.
	hostScope, hasHostScope := contextkeys.RequestInstanceScopeFromContext(ctx)
	cacheKey := token
	if hasHostScope {
		cacheKey = token + "\x00" + hostScope.InstanceID
	}
	if cached, ok := i.sessionCache.Load(cacheKey); ok {
		if entry, ok := cached.(sessionAuth); ok {
			if time.Now().Before(entry.expiresAt) {
				return entry
			}
			i.sessionCache.Delete(cacheKey)
		}
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// sessionlive.Lookup reads everstack.sessions (schema-qualified: the
	// unqualified table resolves to public.* and is empty in real
	// deployments, which once made every admin page permission_denied) and,
	// for a row mirrored from Better Auth, confirms the identity session is
	// still live.
	session, err := sessionlive.Lookup(queryCtx, i.sessionDB, token)
	if err != nil {
		if !sessionlive.IsRejected(err) {
			logger.WithFields(map[string]interface{}{
				"error": err.Error(),
			}).Warn("apikey: session verification failed")
			return sessionAuth{unavailable: true}
		}
		i.sessionCache.Store(cacheKey, sessionAuth{expiresAt: time.Now().Add(sessionCacheTTL)})
		return sessionAuth{}
	}
	userID := session.UserID
	// A positive answer is reused for at most sessionCacheTTL and never
	// past the mirror row's own expiry.
	validUntil := time.Now().Add(sessionCacheTTL)
	if session.ExpiresAt.Before(validUntil) {
		validUntil = session.ExpiresAt
	}

	var tenantID, role string
	if hasHostScope {
		// The Host selected an instance: the session is valid for THAT
		// instance only if the user is a member of its organization. Never
		// answer with a different tenant the user happens to belong to.
		if err := i.sessionDB.QueryRowContext(queryCtx,
			`SELECT COALESCE(om.role, '') FROM everstack.organization_members om
			 WHERE om.user_id = $1 AND om.organization_id = $2::uuid
			 LIMIT 1`, userID, hostScope.OrganizationID,
		).Scan(&role); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				logger.WithFields(map[string]interface{}{"error": err.Error(), "user_id": userID}).
					Warn("apikey: host membership lookup failed")
			}
			i.sessionCache.Store(cacheKey, sessionAuth{expiresAt: time.Now().Add(sessionCacheTTL)})
			return sessionAuth{}
		}
		auth := sessionAuth{valid: true, userID: userID, tenantID: hostScope.InstanceID, role: role,
			expiresAt: validUntil}
		i.sessionCache.Store(cacheKey, auth)
		return auth
	}

	// No hostname-selected instance. Look up the user's primary org. We
	// don't fail closed if the user has no org — some pre-onboarding screens
	// (org-create, billing setup) need to load before membership exists.
	// Tenant-scoped handlers will reject such requests with PermissionDenied,
	// which is the correct behaviour; onboarding endpoints should not be
	// tenant-scoped in the first place. Prefer instance_id from tenant_config
	// (cloud multi-instance mode), deterministically ordered; fall back to
	// organization_id for self-hosted (single-instance) deployments.
	if err := i.sessionDB.QueryRowContext(queryCtx,
		`SELECT tc.instance_id::text, COALESCE(om.role, '')
		 FROM everstack.tenant_config tc
		 JOIN everstack.organization_members om ON om.organization_id = tc.organization_id
		 WHERE om.user_id = $1 AND tc.status = 'active'
		 ORDER BY om.joined_at ASC, tc.instance_id ASC
		 LIMIT 1`, userID,
	).Scan(&tenantID, &role); err != nil {
		tenantID, role = "", ""
		if err := i.sessionDB.QueryRowContext(queryCtx,
			`SELECT organization_id::text, COALESCE(role, '') FROM everstack.organization_members
			 WHERE user_id = $1
			 ORDER BY joined_at ASC
			 LIMIT 1`, userID,
		).Scan(&tenantID, &role); err != nil && !errors.Is(err, sql.ErrNoRows) {
			logger.WithFields(map[string]interface{}{
				"error":   err.Error(),
				"user_id": userID,
			}).Warn("apikey: org membership lookup failed")
		}
	}

	auth := sessionAuth{
		valid:     true,
		userID:    userID,
		tenantID:  tenantID,
		role:      role,
		expiresAt: validUntil,
	}
	i.sessionCache.Store(cacheKey, auth)
	return auth
}

// applySessionAuth installs the validated session's user and tenant ids on
// the request context. Returns the original request unchanged if the auth
// is empty so callers can chain it without checking validity.
func applySessionAuth(r *http.Request, auth sessionAuth) *http.Request {
	if !auth.valid {
		return r
	}
	ctx := r.Context()
	if auth.userID != "" {
		ctx = contextkeys.WithUserID(ctx, auth.userID)
	}
	if auth.tenantID != "" {
		ctx = contextkeys.WithTenantID(ctx, auth.tenantID)
		ctx = database.WithTenantSchema(ctx, auth.tenantID)
	}
	if auth.role != "" {
		ctx = contextkeys.WithUserRole(ctx, auth.role)
	}
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	return r.WithContext(ctx)
}

// WithAPIKeyValidation is a convenience function that creates an interceptor and wraps a handler
func WithAPIKeyValidation(next http.Handler, allowMissing bool) http.Handler {
	interceptor := NewAPIKeyInterceptor(allowMissing)
	return interceptor.WithAPIKeyValidation(next)
}

// WithAPIKeyValidationAndPolicy creates an interceptor with a custom policy
func WithAPIKeyValidationAndPolicy(next http.Handler, allowMissing bool, policy *apilic.Policy) http.Handler {
	interceptor := NewAPIKeyInterceptor(allowMissing)
	if policy != nil {
		interceptor.policy = policy
	}
	return interceptor.WithAPIKeyValidation(next)
}

func (i *APIKeyInterceptor) rateLimited(w http.ResponseWriter, ctx context.Context) bool {
	if n := apikeylib.RetryAfter(ctx); n > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(n))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "rate limit exceeded", "type": "rate_limit_error", "code": "rate_limit_exceeded"}})
		return true
	}
	return false
}
