package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
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
	"github.com/everstacklabs/everstack/pkg/tenant"
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
}

// NewAPIKeyInterceptor creates a new API key interceptor with policy loaded from global config
func NewAPIKeyInterceptor(allowMissing bool) *APIKeyInterceptor {
	return &APIKeyInterceptor{
		allowMissing: allowMissing,
		policy:       apilic.FromGlobal(),
		cache:        make(map[string]apiKeyCacheEntry, 1024),
		cacheTTL:     60 * time.Second,
	}
}

type apiKeyCacheEntry struct {
	valid     bool
	orgID     string
	expiresAt time.Time
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
}

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
		if policyBypass || anonHosting {
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
		if isViteDevServerPath(path) {
			next.ServeHTTP(w, r)
			return
		}

		// Managed/shared tenant requests are authenticated by tenant middleware.
		if tenant.ConfigFromContext(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}
		if contextkeys.IsTenantAuthenticated(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}

		// Self-hosted session auth: validate the session cookie directly
		// against the DB. This is how the admin UI authenticates, and it also
		// supplies the user's tenant id so tenant-scoped handlers downstream
		// see a real tenant context. Without it every admin UI page would 403
		// with "tenant context missing", which is the regression we hit after
		// locking down the body-supplied tenant id fallback on 2026-05-06.
		if i.sessionDB != nil {
			if cookie, err := r.Cookie("es_everstack_session"); err == nil && cookie.Value != "" {
				if auth := i.resolveSession(r.Context(), cookie.Value); auth.valid {
					r = applySessionAuth(r, auth)
					next.ServeHTTP(w, r)
					return
				}
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

		// Explicitly reject Authorization header for non-exempt routes
		if hasAuthBearer(r.Header) {
			i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid authorization token; use "+common.EverstackApiKey, correlationID)
			return
		}
		if !i.validateAPIKey(r) {
			i.writeErrorResponse(w, http.StatusUnauthorized, "API key is required. Create one from the dashboard (Settings > Vault > API Keys). Docs: https://docs.everstack.dev/docs/documentation/vault/api-keys", correlationID)
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
		if orgID, valid, hit := i.lookupCache(hash); hit {
			if valid {
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
				authCtx := contextkeys.WithAuthenticatedAPIKey(r.Context(), orgID, hash)
				if orgID != "" {
					authCtx = database.WithTenantSchema(authCtx, orgID)
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
		dbStart := time.Now()
		q := apikey.NewGetApiKeyByHashQuery(hash, "", "")
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, qerr := sys.QueryBus.Execute(queryCtx, q)
		cancel()
		if qerr != nil || resp == nil {
			i.rememberCache(hash, false, "")
			i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
			return
		}
		var resolvedOrgID string
		if qr, ok := resp.(*query.Response); ok {
			if qr.Data == nil {
				i.rememberCache(hash, false, "")
				i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
				return
			}
			if rm, ok := qr.Data.(apikey.APIKeyReadModel); ok {
				if rm.InstanceID != nil && *rm.InstanceID != "" {
					resolvedOrgID = *rm.InstanceID
				} else if rm.OrgID != nil {
					resolvedOrgID = *rm.OrgID
				}
			}
			i.rememberCache(hash, true, resolvedOrgID)

			// Mark as valid in fast-path cache
			if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
				engine.MarkAPIKeyValid(apiKey)
			}
		} else {
			i.rememberCache(hash, false, "")
			i.writeErrorResponse(w, http.StatusUnauthorized, "Invalid API key", correlationID)
			return
		}
		dbDuration := time.Since(dbStart)
		authCtx := contextkeys.WithAuthenticatedAPIKey(r.Context(), resolvedOrgID, hash)
		if resolvedOrgID != "" {
			authCtx = database.WithTenantSchema(authCtx, resolvedOrgID)
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

// lookupCache returns (orgID, valid, hit). Carrying orgID in the cache lets
// the wrapper install it on the request context after a warm-cache validation,
// without re-querying the DB. Without that, every API-key authenticated
// request would still need a follow-up lookup to know which tenant it
// belongs to — or, worse, fall through with no tenant context at all.
func (i *APIKeyInterceptor) lookupCache(hash string) (string, bool, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if e, ok := i.cache[hash]; ok {
		if time.Now().Before(e.expiresAt) {
			return e.orgID, e.valid, true
		}
	}
	return "", false, false
}

func (i *APIKeyInterceptor) rememberCache(hash string, valid bool, orgID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cache[hash] = apiKeyCacheEntry{valid: valid, orgID: orgID, expiresAt: time.Now().Add(i.cacheTTL)}
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
	return &APIKeyInterceptor{
		allowMissing: allowMissing,
		policy:       apilic.FromGlobal(),
		cache:        make(map[string]apiKeyCacheEntry, 1024),
		cacheTTL:     60 * time.Second,
		sessionDB:    db,
	}
}

// resolveSession validates a session token against the sessions table and
// resolves the user's primary organisation id. The org id is the missing
// piece that lets downstream handlers read tenant from context only — see
// the comment on sessionAuth for the rationale. Results are cached for
// cacheTTL because every admin UI request goes through here.
//
// Org selection: pick the user's earliest-joined organisation. The admin
// UI today reads `session.user.organizations[0]`, which is also ordered by
// joined_at. A future "active org" concept (header / cookie field) would
// override this; until then we mirror what the FE already does.
func (i *APIKeyInterceptor) resolveSession(ctx context.Context, token string) sessionAuth {
	// Cache hit?
	if cached, ok := i.sessionCache.Load(token); ok {
		if entry, ok := cached.(sessionAuth); ok {
			if time.Now().Before(entry.expiresAt) {
				return entry
			}
			i.sessionCache.Delete(token)
		}
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var (
		userID         string
		sessionExpires time.Time
	)
	// Sessions are written to everstack.sessions by services/auth (see
	// session_repo.go). Earlier this query targeted unqualified `sessions`,
	// which resolves to the public schema and is empty in real deployments —
	// resolveSession then returned valid=false, the caller fell back to
	// "authenticated, no tenant", and every admin page got
	// permission_denied. Always qualify the schema.
	err := i.sessionDB.QueryRowContext(queryCtx,
		`SELECT user_id::text, expires_at FROM everstack.sessions WHERE token = $1`, token,
	).Scan(&userID, &sessionExpires)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.WithFields(map[string]interface{}{
				"error": err.Error(),
			}).Warn("apikey: self-hosted session DB lookup failed")
		}
		i.sessionCache.Store(token, sessionAuth{expiresAt: time.Now().Add(i.cacheTTL)})
		return sessionAuth{}
	}
	if !time.Now().Before(sessionExpires) {
		i.sessionCache.Store(token, sessionAuth{expiresAt: time.Now().Add(i.cacheTTL)})
		return sessionAuth{}
	}

	// Look up the user's primary org. We don't fail closed if the user has
	// no org — some pre-onboarding screens (org-create, billing setup) need
	// to load before membership exists. Tenant-scoped handlers will reject
	// such requests with PermissionDenied, which is the correct behaviour;
	// onboarding endpoints should not be tenant-scoped in the first place.
	// Prefer instance_id from tenant_config (cloud multi-instance mode).
	// Fall back to organization_id for self-hosted (single-instance) deployments.
	var tenantID, role string
	if err := i.sessionDB.QueryRowContext(queryCtx,
		`SELECT tc.instance_id::text, COALESCE(om.role, '')
		 FROM everstack.tenant_config tc
		 JOIN everstack.organization_members om ON om.organization_id = tc.organization_id
		 WHERE om.user_id = $1 AND tc.status = 'active'
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
		expiresAt: time.Now().Add(i.cacheTTL),
	}
	i.sessionCache.Store(token, auth)
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
