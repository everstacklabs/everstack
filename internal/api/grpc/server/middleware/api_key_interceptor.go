package middleware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/mferrors"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1/functionsconnect"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

const (
	cacheExpiration        = 5 * time.Minute
	cacheCleanup           = 10 * time.Minute
	sessionCacheExpiration = 2 * time.Minute
)

// cacheEntry holds cached API key validation results, including the owning
// organisation id so the caller can install it on the request context. Before
// 2026-05-06 the cache only carried `valid` and the request was authenticated
// without ever telling downstream handlers which tenant the key belonged to;
// every tenant-scoped query then either ran without a filter or used a
// fallback that produced cross-tenant reads.
type cacheEntry struct {
	valid     bool
	orgID     string
	expiresAt time.Time
}

// APIKeyConnectInterceptor implements connect.Interceptor with caching and optimized validation.
type APIKeyConnectInterceptor struct {
	allowMissing bool
	policy       *apilic.Policy
	cache        sync.Map // map[string]cacheEntry
	sessionDB    *sqlx.DB // direct DB session validation for self-hosted mode (nil = disabled)
	sessionCache sync.Map // map[token]sessionAuth (session validation cache)
	cliTokens    *deviceauth.TokenManager
	cliAuthDB    *sqlx.DB // platform identity/membership DB; falls back to sessionDB in single-DB mode
}

// NewAPIKeyInterceptor constructs an interceptor with API key validation and caching.
func NewAPIKeyInterceptor(allowMissing bool) *APIKeyConnectInterceptor {
	return NewAPIKeyInterceptorWithPolicy(allowMissing, nil)
}

// NewAPIKeyInterceptorWithPolicy constructs an interceptor with a custom policy.
// If policy is nil, it falls back to the global config.
func NewAPIKeyInterceptorWithPolicy(allowMissing bool, policy *apilic.Policy) *APIKeyConnectInterceptor {
	if policy == nil {
		policy = apilic.FromGlobal()
	}
	interceptor := &APIKeyConnectInterceptor{
		allowMissing: allowMissing,
		policy:       policy,
	}

	// Start cache cleanup goroutine
	go interceptor.cleanupCache()

	return interceptor
}

// NewAPIKeyInterceptorWithSessionDB constructs an interceptor with direct DB session validation
// for self-hosted instances. When sessionDB is set, session cookies are validated directly
// against the sessions table as an alternative to API key authentication.
func NewAPIKeyInterceptorWithSessionDB(allowMissing bool, policy *apilic.Policy, db *sqlx.DB) *APIKeyConnectInterceptor {
	if policy == nil {
		policy = apilic.FromGlobal()
	}
	interceptor := &APIKeyConnectInterceptor{
		allowMissing: allowMissing,
		policy:       policy,
		sessionDB:    db,
	}

	go interceptor.cleanupCache()

	return interceptor
}

// SetCLIDeviceTokenManager enables verified OAuth/device Bearer tokens for the
// bounded CLI management surface. The token is still checked against current
// organization membership before the request receives a tenant principal.
func (i *APIKeyConnectInterceptor) SetCLIDeviceTokenManager(manager *deviceauth.TokenManager) {
	i.cliTokens = manager
}

// SetCLIAuthorizationDB selects the platform database that owns managed
// identity links, organization memberships, and tenant_config. It is separate
// from sessionDB because managed gateways keep local gateway data and platform
// authorization data in different databases.
func (i *APIKeyConnectInterceptor) SetCLIAuthorizationDB(db *sqlx.DB) {
	i.cliAuthDB = db
}

// cleanupCache periodically removes expired entries from both API key and session caches
func (i *APIKeyConnectInterceptor) cleanupCache() {
	ticker := time.NewTicker(cacheCleanup)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		i.cache.Range(func(key, value interface{}) bool {
			if entry, ok := value.(cacheEntry); ok {
				if now.After(entry.expiresAt) {
					i.cache.Delete(key)
				}
			}
			return true
		})
		i.sessionCache.Range(func(key, value interface{}) bool {
			if entry, ok := value.(sessionAuth); ok {
				if now.After(entry.expiresAt) {
					i.sessionCache.Delete(key)
				}
			}
			return true
		})
	}
}

// validateCached checks if a key hash is valid and, on success, returns the
// owning organisation id so the caller can scope the request to that tenant.
// The fastpath bloom filter is consulted only for fast rejection; a "valid"
// hit there still falls through to the cache/DB so we can always recover the
// org id (the bloom filter has no payload). The cache layer stores the org id
// alongside the validity bit, so warm-cache requests stay one map lookup.
func (i *APIKeyConnectInterceptor) validateCached(ctx context.Context, apiKey string, hash string) (string, bool) {
	if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
		_, definitelyInvalid := engine.ValidateAPIKey(apiKey)
		if definitelyInvalid {
			return "", false
		}
	}

	if cached, ok := i.cache.Load(hash); ok {
		if entry, ok := cached.(cacheEntry); ok {
			if time.Now().Before(entry.expiresAt) {
				return entry.orgID, entry.valid
			}
			i.cache.Delete(hash)
		}
	}

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil || sys == nil {
		return "", false
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := time.Now()
	q := apikeyq.NewGetApiKeyByHashQuery(hash, "", "")
	resp, qerr := sys.QueryBus.Execute(queryCtx, q)

	valid := qerr == nil && resp != nil
	orgID := ""
	if valid {
		if qr, ok := resp.(*query.Response); ok && qr.Data != nil {
			if rm, ok := qr.Data.(apikeyq.APIKeyReadModel); ok {
				if rm.InstanceID != nil && *rm.InstanceID != "" {
					orgID = *rm.InstanceID
				} else if rm.OrgID != nil {
					orgID = *rm.OrgID
				}
			}
		} else if rm, ok := resp.(apikeyq.APIKeyReadModel); ok {
			if rm.InstanceID != nil && *rm.InstanceID != "" {
				orgID = *rm.InstanceID
			} else if rm.OrgID != nil {
				orgID = *rm.OrgID
			}
		}
	}

	if !valid {
		hashPrefix := hash
		if len(hash) > 8 {
			hashPrefix = hash[:8]
		}
		respType := "nil"
		if resp != nil {
			respType = fmt.Sprintf("%T", resp)
		}
		logger.WithFields(
			"elapsed_ms", time.Since(start).Milliseconds(),
			"hash_prefix", hashPrefix,
			"query_error", qerr != nil,
			"resp_nil", resp == nil,
			"resp_type", respType,
		).Warn("apikey: not found or invalid")
	}

	i.cache.Store(hash, cacheEntry{
		valid:     valid,
		orgID:     orgID,
		expiresAt: time.Now().Add(cacheExpiration),
	})

	if valid {
		if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
			engine.MarkAPIKeyValid(apiKey)
		}
	}

	return orgID, valid
}

// validateAPIKey performs the complete API key validation flow. It returns a
// context that may have the user's tenant id installed when the request was
// authenticated via a session cookie — without that, every tenant-scoped
// handler downstream rejects the call with "tenant context missing", which
// is the regression the body-trust removal exposed on 2026-05-06. For API
// key and bypass paths we return ctx unchanged.
func (i *APIKeyConnectInterceptor) validateAPIKey(ctx context.Context, hdr http.Header, proc string, correlationID string) (context.Context, error) {
	// Managed/shared tenant requests are authenticated by tenant middleware.
	if tenant.ConfigFromContext(ctx) != nil {
		return ctx, nil
	}
	if contextkeys.IsTenantAuthenticated(ctx) {
		return ctx, nil
	}

	// Check policy-based bypass (configured in services/config/config.yaml)
	if i.policy != nil && i.policy.ShouldBypassProcedure(proc) {
		logger.WithFields("procedure", proc, "correlation_id", correlationID).Debug("Bypassing API key validation per policy")
		return ctx, nil
	}

	if hasAuthBearer(hdr) {
		if i.cliTokens != nil && isCLIBearerProcedure(proc) {
			return i.applyCLIBearerContext(ctx, hdr, correlationID)
		}
		// Keep the legacy CLIService handoff when token verification is not
		// configured on the interceptor. Its handler performs the same signature
		// verification and returns a precise configuration error.
		if strings.HasPrefix(proc, "/everstack.cli.v1.CLIService/") {
			return ctx, nil
		}
		return ctx, i.errorWithCorrelation(
			connect.CodeUnauthenticated,
			mferrors.CreateEverstackError(nil, "Invalid authorization token; use "+common.EverstackApiKey, ""),
			correlationID,
		)
	}

	// Loopback calls this process makes to its own API carry a process-local
	// token an external caller cannot produce. See internal/api/internalauth.
	//
	// This branch used to trigger on isSameOrigin(), which accepted a
	// `Sec-Fetch-Site: same-origin` header, a matching Origin/Referer, or any
	// loopback source address, and then returned a nil error: authenticated
	// with no credential at all. None of those are credentials.
	if internalauth.IsInternalHeader(hdr) {
		return ctx, nil
	}

	// Self-hosted session auth: validate session cookie directly against DB
	if i.sessionDB != nil {
		if token := extractSessionCookie(hdr, "es_everstack_session"); token != "" {
			if newCtx, ok := i.applySessionContext(ctx, token); ok {
				logger.WithFields("procedure", proc, "correlation_id", correlationID).Debug("Authenticated via self-hosted session cookie")
				return newCtx, nil
			}
		}
	}

	// Allow M2M authenticated requests to pass through (validated by M2M interceptor later)
	// M2M requests have either x-mf-service-token or x-mf-instance-id headers with a signature
	if hasM2MAuth(hdr) {
		logger.WithFields("procedure", proc, "correlation_id", correlationID).Debug("Allowing M2M authenticated request")
		return ctx, nil
	}

	// Extract and validate API key (canonical x-evs-api-key, falling back to
	// legacy x-mf-api-key / x-everstack-api-key for deployed clients).
	key := strings.TrimSpace(common.GetHeader(hdr.Get, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey))
	if key == "" {
		return ctx, i.errorWithCorrelation(
			connect.CodeUnauthenticated,
			mferrors.CreateEverstackError(nil, "API key is required. Create one from the dashboard (Settings > Vault > API Keys). Docs: https://docs.everstack.dev/docs/documentation/vault/api-keys", ""),
			correlationID,
		)
	}

	// Hash the key
	hash, ok := apikeylib.HashFromContext(ctx, key)
	if !ok || strings.TrimSpace(hash) == "" {
		return ctx, i.errorWithCorrelation(
			connect.CodeUnauthenticated,
			mferrors.CreateEverstackError(nil, "invalid API key format", ""),
			correlationID,
		)
	}

	// Validate against DB with caching (pass both apiKey and hash for fast-path)
	orgID, valid := i.validateCached(ctx, key, hash)
	if !valid {
		return ctx, i.errorWithCorrelation(
			connect.CodeUnauthenticated,
			mferrors.CreateEverstackError(nil, "invalid API key", ""),
			correlationID,
		)
	}

	// Install the key's owning org id as the request's tenant id. Without
	// this every API-key authenticated call would reach handlers with no
	// tenant in context — the same gap that produced the cross-tenant leak
	// fixed on 2026-05-06 when handlers were trusting body-supplied ids.
	if orgID != "" {
		ctx = contextkeys.WithTenantID(ctx, orgID)
		ctx = database.WithTenantSchema(ctx, orgID)
	}
	return contextkeys.WithAuthenticatedAPIKey(ctx, orgID, hash), nil
}

func isCLIBearerProcedure(procedure string) bool {
	return strings.HasPrefix(procedure, "/everstack.cli.v1.CLIService/") ||
		strings.HasPrefix(procedure, "/everstack.agents.v1.AgentsService/") ||
		// Pulling a legacy agent probes each tool name to distinguish built-in
		// tools from tenant functions. Keep the bearer surface read-only: agent
		// deploys use immutable revisions and do not need Functions CRUD access.
		procedure == functionsconnect.FunctionsServiceGetFunctionByNameProcedure
}

func (i *APIKeyConnectInterceptor) applyCLIBearerContext(
	ctx context.Context,
	header http.Header,
	correlationID string,
) (context.Context, error) {
	reject := func() (context.Context, error) {
		return ctx, i.errorWithCorrelation(
			connect.CodeUnauthenticated,
			mferrors.CreateEverstackError(nil, "invalid or expired CLI access token", ""),
			correlationID,
		)
	}
	authorization := header.Get(common.Authorization)
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	identity, err := i.cliTokens.Verify(token)
	if err != nil || identity == nil ||
		(identity.ClientID != "evs-cli" && identity.ClientID != "ewt-cli") {
		return reject()
	}

	// Client-supplied routing headers may narrow to the signed identity, never
	// select a different organization or instance.
	for _, orgHeader := range []string{common.EverstackOrgID, common.XOrgID} {
		if requested := strings.TrimSpace(header.Get(orgHeader)); requested != "" && requested != identity.OrganizationID {
			return reject()
		}
	}
	tenantID := identity.InstanceID
	if requestInstance, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok {
		if requestInstance.OrganizationID != identity.OrganizationID {
			return reject()
		}
		if tenantID == "" {
			tenantID = requestInstance.InstanceID
		} else if tenantID != requestInstance.InstanceID {
			return reject()
		}
	}
	if tenantID == "" {
		// Standalone self-hosted deployments have no tenant_config hostname
		// mapping. Preserve their single-org scope while managed instances use
		// the verified request instance above.
		tenantID = identity.OrganizationID
	}
	if requested := strings.TrimSpace(header.Get(common.EverstackTenantID)); requested != "" &&
		requested != tenantID && requested != identity.OrganizationID {
		return reject()
	}
	authDB := i.cliAuthDB
	if authDB == nil {
		authDB = i.sessionDB
	}
	if authDB == nil {
		return reject()
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type cliMembership struct {
		Role   string `db:"role"`
		UserID string `db:"user_id"`
	}
	var membership cliMembership
	if tenantID != identity.OrganizationID {
		err = authDB.GetContext(queryCtx, &membership,
			`SELECT COALESCE(om.role, '') AS role, om.user_id::text AS user_id
			 FROM everstack.organization_members om
			 JOIN everstack.tenant_config tc ON tc.organization_id = om.organization_id
			 WHERE om.user_id::text = $1 AND om.organization_id::text = $2
			   AND tc.instance_id::text = $3 AND tc.status = 'active'
			 LIMIT 1`, identity.UserID, identity.OrganizationID, tenantID)
	} else {
		err = authDB.GetContext(queryCtx, &membership,
			`SELECT COALESCE(om.role, '') AS role, om.user_id::text AS user_id
			 FROM everstack.organization_members om
			 WHERE om.user_id::text = $1 AND om.organization_id::text = $2
			 LIMIT 1`, identity.UserID, identity.OrganizationID)
	}
	if errors.Is(err, sql.ErrNoRows) && strings.TrimSpace(identity.Email) != "" {
		// Managed identity and product authorization deliberately use different
		// identifiers. Older device tokens can therefore carry the authenticated
		// identity user ID while organization_members references the stable
		// Everstack user. Resolve that boundary through an authoritative identity
		// link, accepting an email match only when the link itself is verified.
		membership = cliMembership{}
		if tenantID != identity.OrganizationID {
			err = authDB.GetContext(queryCtx, &membership,
				`SELECT COALESCE(om.role, '') AS role, om.user_id::text AS user_id
				 FROM everstack.organization_members om
				 JOIN everstack.identity_links il ON il.user_id = om.user_id
				 JOIN everstack.tenant_config tc ON tc.organization_id = om.organization_id
				 WHERE il.email_verified = TRUE AND lower(il.email_at_link) = lower($3::text)
				   AND om.organization_id::text = $1
				   AND tc.instance_id::text = $2 AND tc.status = 'active'
				 LIMIT 1`, identity.OrganizationID, tenantID, strings.TrimSpace(identity.Email))
		} else {
			err = authDB.GetContext(queryCtx, &membership,
				`SELECT COALESCE(om.role, '') AS role, om.user_id::text AS user_id
				 FROM everstack.organization_members om
				 JOIN everstack.identity_links il ON il.user_id = om.user_id
				 WHERE il.email_verified = TRUE AND lower(il.email_at_link) = lower($2::text)
				   AND om.organization_id::text = $1
				 LIMIT 1`, identity.OrganizationID, strings.TrimSpace(identity.Email))
		}
	}
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.WithFields("error", err.Error(), "user_id", identity.UserID).
				Warn("apikey: CLI bearer membership lookup failed")
		}
		return reject()
	}
	if strings.TrimSpace(membership.UserID) == "" {
		return reject()
	}

	ctx = contextkeys.WithUserID(ctx, membership.UserID)
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	ctx = database.WithTenantSchema(ctx, tenantID)
	if role := strings.TrimSpace(membership.Role); role != "" {
		ctx = contextkeys.WithUserRole(ctx, role)
	}
	return contextkeys.WithTenantAuthenticated(ctx), nil
}

// applySessionContext validates a session cookie and, on success, returns a
// context carrying the session's user id and primary tenant id along with
// the WithTenantAuthenticated marker. Mirrors the HTTP middleware's
// resolveSession + applySessionAuth pair so admin requests reach
// tenant-scoped handlers with a real tenant in context regardless of which
// transport they entered through.
func (i *APIKeyConnectInterceptor) applySessionContext(ctx context.Context, token string) (context.Context, bool) {
	auth := i.lookupSession(ctx, token)
	if !auth.valid {
		return ctx, false
	}
	if auth.userID != "" {
		ctx = contextkeys.WithUserID(ctx, auth.userID)
	}
	if auth.tenantID != "" {
		ctx = contextkeys.WithTenantID(ctx, auth.tenantID)
		ctx = database.WithTenantSchema(ctx, auth.tenantID)
	}
	if auth.role != "" {
		// The role is cached with the session (sessionCacheExpiration), so a role
		// change (demotion or org removal) takes effect in the authz bridge after
		// at most one cache TTL. Explicit per-resource grants are unaffected.
		ctx = contextkeys.WithUserRole(ctx, auth.role)
	}
	return contextkeys.WithTenantAuthenticated(ctx), true
}

// sessionAuth is the cached result of a session-cookie lookup.
type sessionAuth struct {
	valid     bool
	userID    string
	tenantID  string
	role      string // caller's role in the active org (owner/admin/member/viewer)
	expiresAt time.Time
}

// errorWithCorrelation creates a connect error with correlation ID
func (i *APIKeyConnectInterceptor) errorWithCorrelation(code connect.Code, err error, correlationID string) error {
	connErr := connect.NewError(code, err)
	connErr.Meta().Set(correlation.CorrelationIDHeader, correlationID)
	return connErr
}

// WrapUnary implements connect.Interceptor for unary RPCs.
func (i *APIKeyConnectInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		proc := req.Spec().Procedure
		// Detect endpoint type from procedure and generate appropriate correlation ID
		endpoint := correlation.DetectEndpointFromProcedure(proc)
		ctx, correlationID := correlation.EnsureEndpointCorrelationID(ctx, endpoint)
		// Validate API key — also installs session-derived tenant context.
		newCtx, err := i.validateAPIKey(ctx, req.Header(), proc, correlationID)
		if err != nil {
			return nil, err
		}
		ctx = newCtx
		// Call next handler
		resp, err := next(ctx, req)

		// Add correlation ID to response.
		// IMPORTANT: never mutate an existing connect.Error's Meta() — it may
		// be a shared/singleton error from a downstream handler, and concurrent
		// requests writing to the same map cause "concurrent map writes" panics.
		// Always create a fresh error with its own metadata.
		if err != nil {
			var connectErr *connect.Error
			code := connect.CodeUnknown
			if errors.As(err, &connectErr) {
				code = connectErr.Code()
			}
			wrapped := connect.NewError(code, err)
			wrapped.Meta().Set(correlation.CorrelationIDHeader, correlationID)
			return nil, wrapped
		}

		if resp != nil {
			resp.Header().Set(correlation.CorrelationIDHeader, correlationID)
		}

		return resp, nil
	}
}

// WrapStreaming implements connect.Interceptor for streaming RPCs.
func (i *APIKeyConnectInterceptor) WrapStreaming(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		proc := conn.Spec().Procedure
		// Detect endpoint type from procedure and generate appropriate correlation ID
		endpoint := correlation.DetectEndpointFromProcedure(proc)
		ctx, correlationID := correlation.EnsureEndpointCorrelationID(ctx, endpoint)
		// Validate API key — also installs session-derived tenant context.
		newCtx, err := i.validateAPIKey(ctx, conn.RequestHeader(), proc, correlationID)
		if err != nil {
			return err
		}
		return next(newCtx, conn)
	}
}

// WrapStreamingClient implements connect.Interceptor for client-side streaming.
func (i *APIKeyConnectInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		// Detect endpoint type from procedure and generate appropriate correlation ID
		endpoint := correlation.DetectEndpointFromProcedure(spec.Procedure)
		ctx, _ = correlation.EnsureEndpointCorrelationID(ctx, endpoint)
		return next(ctx, spec)
	}
}

// WrapStreamingHandler implements connect.Interceptor for server-side streaming.
func (i *APIKeyConnectInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		proc := conn.Spec().Procedure
		// Detect endpoint type from procedure and generate appropriate correlation ID
		endpoint := correlation.DetectEndpointFromProcedure(proc)
		ctx, correlationID := correlation.EnsureEndpointCorrelationID(ctx, endpoint)

		// Validate API key — also installs session-derived tenant context.
		newCtx, err := i.validateAPIKey(ctx, conn.RequestHeader(), proc, correlationID)
		if err != nil {
			return err
		}

		return next(newCtx, conn)
	}
}

// lookupSession validates a session token against the sessions table and
// resolves the user's primary organisation id. Mirrors the HTTP middleware's
// resolveSession; see that comment for the org-selection rationale.
func (i *APIKeyConnectInterceptor) lookupSession(ctx context.Context, token string) sessionAuth {
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
	// everstack.sessions is the table services/auth writes to (see
	// session_repo.go). Earlier this query targeted unqualified `sessions`
	// which resolves to public.* and is empty in real deployments — every
	// admin page then hit permission_denied because resolveSession returned
	// valid=false. Always qualify the schema.
	err := i.sessionDB.QueryRowContext(queryCtx,
		`SELECT user_id::text, expires_at FROM everstack.sessions WHERE token = $1`, token,
	).Scan(&userID, &sessionExpires)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.WithFields("error", err.Error()).Warn("apikey: self-hosted session DB lookup failed")
		}
		i.sessionCache.Store(token, sessionAuth{expiresAt: time.Now().Add(sessionCacheExpiration)})
		return sessionAuth{}
	}
	if !time.Now().Before(sessionExpires) {
		i.sessionCache.Store(token, sessionAuth{expiresAt: time.Now().Add(sessionCacheExpiration)})
		return sessionAuth{}
	}

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
		// Reset so a partial scan from the failed first query cannot leak a stale
		// tenant/role into the fallback result.
		tenantID, role = "", ""
		if err := i.sessionDB.QueryRowContext(queryCtx,
			`SELECT organization_id::text, COALESCE(role, '') FROM everstack.organization_members
			 WHERE user_id = $1
			 ORDER BY joined_at ASC
			 LIMIT 1`, userID,
		).Scan(&tenantID, &role); err != nil && !errors.Is(err, sql.ErrNoRows) {
			logger.WithFields("error", err.Error(), "user_id", userID).Warn("apikey: org membership lookup failed")
		}
	}

	auth := sessionAuth{
		valid:     true,
		userID:    userID,
		tenantID:  tenantID,
		role:      role,
		expiresAt: time.Now().Add(sessionCacheExpiration),
	}
	i.sessionCache.Store(token, auth)
	return auth
}

// extractSessionCookie extracts a specific cookie value from the Cookie header.
func extractSessionCookie(hdr http.Header, name string) string {
	cookieHeader := hdr.Get("Cookie")
	if cookieHeader == "" {
		return ""
	}
	header := http.Header{}
	header.Add("Cookie", cookieHeader)
	request := http.Request{Header: header}
	cookie, err := request.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// hasAuthBearer checks if Authorization header contains a Bearer token
func hasAuthBearer(hdr http.Header) bool {
	authHeader := hdr.Get(common.Authorization)
	if authHeader == "" {
		return false
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	return token != ""
}

// hasM2MAuth checks if the request has M2M authentication headers
// M2M auth requires either x-mf-service-token or x-mf-instance-id, plus a signature
func hasM2MAuth(hdr http.Header) bool {
	// Check for M2M identity headers
	hasIdentity := hdr.Get(HeaderServiceToken) != "" || hdr.Get(HeaderInstanceID) != ""
	if !hasIdentity {
		return false
	}

	// Must also have a signature to be a valid M2M request
	return hdr.Get(HeaderSignature) != ""
}

// APIKeyInterceptorForPrefixes creates an interceptor for specific service prefixes.
// Note: Current implementation doesn't filter by prefix - enhance if needed.
func APIKeyInterceptorForPrefixes(prefixes []string, allowMissing bool) connect.Interceptor {
	return NewAPIKeyInterceptor(allowMissing)
}

// InvalidateCache removes a specific API key hash from the cache
func (i *APIKeyConnectInterceptor) InvalidateCache(hash string) {
	i.cache.Delete(hash)
}

// ClearCache removes all entries from the cache
func (i *APIKeyConnectInterceptor) ClearCache() {
	i.cache.Range(func(key, value interface{}) bool {
		i.cache.Delete(key)
		return true
	})
}
