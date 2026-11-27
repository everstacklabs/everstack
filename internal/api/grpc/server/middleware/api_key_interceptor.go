package middleware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/api/surfaceauth"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
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
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1/api_keyconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1/functionsconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/memory/v1/memorypbconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/openai_agents/v1/openaiagentsconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1/orgconnect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1/providersconnect"
)

const (
	cacheExpiration = 5 * time.Minute
	cacheCleanup    = 10 * time.Minute
	// sessionCacheExpiration bounds how long a session-cookie answer is
	// reused. It must not exceed sessionlive.MaxCacheTTL: a cached "valid"
	// keeps a revoked Better Auth session working until it expires.
	sessionCacheExpiration = sessionlive.MaxCacheTTL
)

// cacheEntry holds cached API key validation results, including the owning
// organisation id so the caller can install it on the request context. Before
// 2026-05-06 the cache only carried `valid` and the request was authenticated
// without ever telling downstream handlers which tenant the key belonged to;
// every tenant-scoped query then either ran without a filter or used a
// fallback that produced cross-tenant reads.
type cacheEntry struct {
	valid bool
	orgID string
	// keyID is the api_keys.id of a valid key, for spend attribution.
	keyID        string
	surfaces     []string
	rateLimitRPM *int
	expiresAt    time.Time
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
	// lookups shares concurrent key lookups and briefly remembers failed
	// ones, so a struggling database is not asked by every request.
	lookups *apikeylib.LookupGuard
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
		lookups:      apikeylib.NewLookupGuard(apikeylib.DefaultFailureWindow),
	}

	interceptor.registerCacheInvalidation()
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
		lookups:      apikeylib.NewLookupGuard(apikeylib.DefaultFailureWindow),
	}

	interceptor.registerCacheInvalidation()
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
// alongside the validity bit, so warm-cache requests stay one map lookup. The
// key's id rides the same entry, for spend attribution.
//
// lookupErr means the lookup itself failed, so the key's validity is unknown.
// That answer is never cached.
func (i *APIKeyConnectInterceptor) validateCached(ctx context.Context, apiKey string, hash string) (orgID, keyID string, surfaces []string, rpm *int, valid bool, lookupErr error) {
	if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
		_, definitelyInvalid := engine.ValidateAPIKey(apiKey)
		if definitelyInvalid {
			return "", "", nil, nil, false, nil
		}
	}

	if cached, ok := i.cache.Load(hash); ok {
		if entry, ok := cached.(cacheEntry); ok {
			if time.Now().Before(entry.expiresAt) {
				return entry.orgID, entry.keyID, entry.surfaces, entry.rateLimitRPM, entry.valid, nil
			}
			i.cache.Delete(hash)
		}
	}

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil || sys == nil {
		return "", "", nil, nil, false, nil
	}

	revision := apikeylib.CacheRevision()
	start := time.Now()
	q := apikeyq.NewGetApiKeyByHashQuery(hash, "", "")
	q.CallerEnforces = true
	resp, qerr := i.lookups.Do(ctx, hash, func(lctx context.Context) (interface{}, error) {
		return sys.QueryBus.Execute(lctx, q)
	})
	if qerr != nil {
		// Caching a failed lookup as "invalid" turned one refused database
		// connection into five minutes of rejections for a valid key.
		logger.WithFields("elapsed_ms", time.Since(start).Milliseconds()).Warn("apikey: lookup failed; not cached")
		return "", "", nil, nil, false, apikeylib.ErrLookupUnavailable
	}

	if qr, ok := resp.(*query.Response); ok {
		resp = qr.Data
	}
	if rm, ok := resp.(apikeyq.APIKeyReadModel); qerr == nil && ok {
		valid = true
		keyID = rm.ID
		surfaces = rm.Surfaces
		rpm = rm.RateLimitRPM
		if rm.InstanceID != nil && *rm.InstanceID != "" {
			orgID = *rm.InstanceID
		} else if rm.OrgID != nil {
			orgID = *rm.OrgID
		}
	}

	if !valid {
		respType := "nil"
		if resp != nil {
			respType = fmt.Sprintf("%T", resp)
		}
		logger.WithFields(
			"elapsed_ms", time.Since(start).Milliseconds(),
			"query_error", qerr != nil,
			"resp_nil", resp == nil,
			"resp_type", respType,
		).Warn("apikey: not found or invalid")
	}

	if !apikeylib.WithCacheRevision(revision, func() {
		i.cache.Store(hash, cacheEntry{
			valid:        valid,
			orgID:        orgID,
			keyID:        keyID,
			surfaces:     surfaces,
			rateLimitRPM: rpm,
			expiresAt:    time.Now().Add(apikeylib.CacheTTL(surfaces, cacheExpiration)),
		})
	}) {
		return "", "", nil, nil, false, nil
	}

	if valid {
		if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
			engine.MarkAPIKeyValid(apiKey)
		}
	}

	return orgID, keyID, surfaces, rpm, valid, nil
}

// validateAPIKey performs the complete API key validation flow. It returns a
// context that may have the user's tenant id installed when the request was
// authenticated via a session cookie — without that, every tenant-scoped
// handler downstream rejects the call with "tenant context missing", which
// is the regression the body-trust removal exposed on 2026-05-06. For API
// key and bypass paths we return ctx unchanged.
func (i *APIKeyConnectInterceptor) validateAPIKey(ctx context.Context, hdr http.Header, proc string, correlationID string) (context.Context, error) {
	// A forwarded identity is signed by this process, never copied from a client.
	if identity, ok := internalauth.GatewayKeyIdentity(hdr); ok {
		if !contextkeys.HostScopeAdmitsTenant(ctx, identity.TenantID) || tenantlive.Deleted(identity.TenantID) {
			return ctx, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid API key"))
		}
		ctx = contextkeys.WithAuthenticatedAPIKeyID(ctx, identity.TenantID, identity.Hash, identity.KeyID)
		ctx = contextkeys.WithAPIKeySurfaces(ctx, identity.Surfaces)
		ctx = database.WithTenantSchema(ctx, identity.TenantID)
		ctx = contextkeys.WithAPIKeyRateLimit(ctx, identity.RateLimitRPM)
		if identity.Processed {
			ctx = apikeylib.MarkProcessed(ctx, identity.KeyID, apikeylib.SurfaceInference)
		}
	}
	if contextkeys.IsTenantAuthenticated(ctx) {
		if contextkeys.GetAPIKeyHash(ctx) != "" && !surfaceauth.Procedure(contextkeys.GetAPIKeySurfaces(ctx), proc) {
			return ctx, connect.NewError(connect.CodePermissionDenied, errors.New("this API key cannot call this API"))
		}
		if contextkeys.GetAPIKeyHash(ctx) != "" {
			return admitInference(ctx, contextkeys.GetAPIKeyID(ctx), contextkeys.GetTenantID(ctx), contextkeys.GetAPIKeySurfaces(ctx), contextkeys.GetAPIKeyRateLimit(ctx))
		}
		return ctx, nil
	}

	// Check policy-based bypass (configured in services/config/config.yaml)
	if i.policy != nil && i.policy.ShouldBypassProcedure(proc) && common.GetHeader(hdr.Get, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey) == "" {
		logger.WithFields("procedure", proc, "correlation_id", correlationID).Debug("Bypassing API key validation per policy")
		return ctx, nil
	}

	if hasAuthBearer(hdr) {
		if i.cliTokens != nil && isCLIBearerProcedure(proc) {
			return i.applyCLIBearerContext(ctx, hdr, proc, correlationID)
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
		// A cookie the tenant middleware already refused for this host (non
		// member, or unresolved host on a managed gateway) must not be
		// re-authenticated here as the user's own tenant (EVS-309).
		if token := extractSessionCookie(hdr, "es_everstack_session"); token != "" && !contextkeys.IsTenantAuthRejected(ctx) {
			newCtx, ok, unavailable := i.applySessionContext(ctx, token)
			if ok {
				logger.WithFields("procedure", proc, "correlation_id", correlationID).Debug("Authenticated via self-hosted session cookie")
				return newCtx, nil
			}
			if unavailable {
				ctx = contextkeys.WithSessionUnavailable(ctx)
			}
		}
	}

	// x-evs-service-token / x-evs-signature are NOT credentials here. This
	// branch used to return nil whenever those headers were present, and no
	// later layer verified the HMAC (the gateway holds no key to verify it
	// with), so any caller could reach every GatewayService procedure by
	// sending two arbitrary header values. The procedures billing/license call
	// on the gateway authenticate themselves and are policy-bypassed.

	// Extract and validate API key (canonical x-evs-api-key, falling back to
	// legacy x-mf-api-key / x-everstack-api-key for deployed clients).
	key := strings.TrimSpace(common.GetHeader(hdr.Get, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey))
	if key == "" {
		if contextkeys.IsSessionUnavailable(ctx) {
			// The only credential was a session cookie the session store
			// could not verify: deny, as Unavailable rather than
			// Unauthenticated so an outage does not read as a sign-out.
			return ctx, i.errorWithCorrelation(
				connect.CodeUnavailable,
				mferrors.CreateEverstackError(nil, "session verification unavailable, retry shortly", ""),
				correlationID,
			)
		}
		return ctx, i.errorWithCorrelation(
			connect.CodeUnauthenticated,
			mferrors.CreateEverstackError(nil, "API key is required. Create one from the dashboard (Settings > Vault > API Keys). Docs: https://docs.everstack.ai/getting-started/vault/api-keys", ""),
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
	orgID, keyID, surfaces, rpm, valid, lookupErr := i.validateCached(ctx, key, hash)
	if lookupErr != nil {
		// Still refused, but as retryable: the key may well be valid.
		return ctx, i.errorWithCorrelation(
			connect.CodeUnavailable,
			mferrors.CreateEverstackError(nil, lookupErr.Error(), ""),
			correlationID,
		)
	}
	// A key of a deleted managed instance stops working, even while its
	// positive answer is still cached (tenantlive, managed gateways only).
	if valid && tenantlive.Deleted(orgID) {
		valid = false
	}
	// A valid key on another tenant's hostname is answered exactly like an
	// unknown key, so the response does not reveal that it is valid
	// elsewhere. The cached positive answer stays: the key still works on
	// its own host. See contextkeys.HostScopeAdmitsTenant.
	if valid && !contextkeys.HostScopeAdmitsTenant(ctx, orgID) {
		hostInstance := ""
		if scope, ok := contextkeys.HostSelectedTenantFromContext(ctx); ok {
			hostInstance = scope.InstanceID
		} else if scope, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok {
			hostInstance = scope.InstanceID
		}
		logger.WithFields(
			"audit_event", true,
			"event_type", "apikey.host_mismatch",
			"key_tenant_id", orgID,
			"host_instance", hostInstance,
			"procedure", proc,
			"correlation_id", correlationID,
		).Warn("apikey: key presented on another tenant's host; refused as invalid")
		valid = false
	}
	if !valid {
		return ctx, i.errorWithCorrelation(
			connect.CodeUnauthenticated,
			mferrors.CreateEverstackError(nil, "invalid API key", ""),
			correlationID,
		)
	}

	if !surfaceauth.Procedure(surfaces, proc) {
		return ctx, connect.NewError(connect.CodePermissionDenied, errors.New("this API key cannot call this API"))
	}
	// Install the key's owning org id as the request's tenant id. Without
	// this every API-key authenticated call would reach handlers with no
	// tenant in context — the same gap that produced the cross-tenant leak
	// fixed on 2026-05-06 when handlers were trusting body-supplied ids.
	if orgID != "" {
		ctx = contextkeys.WithTenantID(ctx, orgID)
		ctx = database.WithTenantSchema(ctx, orgID)
	}
	ctx = contextkeys.WithAPIKeyRateLimit(contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(ctx, orgID, hash, keyID), surfaces), rpm)
	return admitInference(ctx, keyID, orgID, surfaces, rpm)
}

// isCLIBearerProcedure reports whether the CLI login token (`evs login`) may
// authenticate procedure. The token is a user credential on a developer's
// machine, so the surface it reaches is bounded on purpose.
//
// Whole services are admitted only where every RPC is part of the CLI's
// agent workflow. Everything else is admitted by exact procedure, never by
// service prefix, so adding an RPC to a service does not silently widen what
// a login token can do. Inference is never admitted: the gateway's metering
// and per-key limits are keyed on an API key.
func isCLIBearerProcedure(procedure string) bool {
	return strings.HasPrefix(procedure, "/everstack.cli.v1.CLIService/") ||
		strings.HasPrefix(procedure, "/everstack.agents.v1.AgentsService/") ||
		strings.HasPrefix(procedure, "/"+openaiagentsconnect.OpenAIAgentsServiceName+"/") ||
		cliBearerProcedures[procedure] ||
		cliBearerOrgAdminProcedures[procedure]
}

// cliBearerProcedures are the individual procedures any organization member
// may call with a login token. They match what the same member can already
// do from the dashboard with a session.
var cliBearerProcedures = map[string]bool{
	// Pulling a legacy agent probes each tool name to distinguish built-in
	// tools from tenant functions. Keep the bearer surface read-only: agent
	// deploys use immutable revisions and do not need Functions CRUD access.
	functionsconnect.FunctionsServiceGetFunctionByNameProcedure: true,

	// `evs providers list|catalog`, `evs models list`, `evs status`,
	// `evs doctor`. Provider keys come back masked. ListModels reads the
	// catalog and the tenant's provider rows; it runs no inference.
	providersconnect.ProvidersServiceListConfiguredProvidersProcedure: true,
	providersconnect.ProvidersServiceListProviderCatalogProcedure:     true,
	gatewayconnect.GatewayServiceListModelsProcedure:                  true,

	// `evs org list|get` and `evs org members list`. The handler pins a
	// login token to the organization it was issued for. Writes are not
	// admitted.
	orgconnect.OrganizationServiceListOrganizationsProcedure: true,
	orgconnect.OrganizationServiceGetOrganizationProcedure:   true,
	orgconnect.OrganizationServiceListMembersProcedure:       true,

	// `evs keys list|get`.
	api_keyconnect.ApiKeyServiceListApiKeysProcedure: true,
	api_keyconnect.ApiKeyServiceGetApiKeyProcedure:   true,

	// `evs memory collections ...` and `evs memory analytics`. Left out on
	// purpose: AddDocuments and QueryCollection run embedding models, which
	// is inference with no per-key metering, and SetupPgVector runs shared
	// DDL. Those stay API-key operations.
	memorypbconnect.MemoryServiceListCollectionsProcedure:    true,
	memorypbconnect.MemoryServiceGetCollectionProcedure:      true,
	memorypbconnect.MemoryServiceCreateCollectionProcedure:   true,
	memorypbconnect.MemoryServiceDeleteCollectionProcedure:   true,
	memorypbconnect.MemoryServiceGetMemoryAnalyticsProcedure: true,
}

// cliBearerOrgAdminProcedures are admitted only when the token's user is an
// owner or admin of the organization. They write credentials or change
// routing: a provider secret, or an API key that outlives the login token and
// can call inference. The handlers themselves do not check role, so the check
// is made here, where the membership row has just been read.
var cliBearerOrgAdminProcedures = map[string]bool{
	// `evs providers add|update|remove`.
	providersconnect.ProvidersServiceConfigureProviderProcedure:           true,
	providersconnect.ProvidersServiceToggleProviderProcedure:              true,
	providersconnect.ProvidersServiceDeleteProviderConfigurationProcedure: true,

	// `evs keys create|revoke`.
	api_keyconnect.ApiKeyServiceCreateApiKeyProcedure: true,
	api_keyconnect.ApiKeyServiceDeleteApiKeyProcedure: true,
}

// orgAdminWriteAllowed is the role check for credential and routing writes,
// applied to every signed-in person however they authenticated (dashboard
// session or CLI login token). The handlers for these procedures do not check
// role, so before this any member could mint a full-surface API key or
// replace a provider secret.
//
// It only constrains people. An API key or an internal caller carries no role
// and is not affected: a key that can reach these procedures was itself
// created by an owner or admin.
//
// A person whose role is not recorded is refused on a managed gateway. On a
// self-hosted gateway an unrecorded role is let through, because older
// single-operator installs have members with no role and would otherwise be
// locked out of their own provider settings.
func orgAdminWriteAllowed(ctx context.Context, procedure string) error {
	if !orgAdminWriteProcedures[procedure] || !contextkeys.IsDashboardUser(ctx) {
		return nil
	}
	switch strings.TrimSpace(contextkeys.GetUserRole(ctx)) {
	case "owner", "admin":
		return nil
	case "":
		if !enterprise.ManagedGateway() {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied, errors.New("this action needs an organization owner or admin"))
}

// orgAdminWriteProcedures are the procedures orgAdminWriteAllowed gates: the
// login token's admin set plus shared DDL the login token cannot reach at all.
var orgAdminWriteProcedures = func() map[string]bool {
	set := map[string]bool{
		memorypbconnect.MemoryServiceSetupPgVectorProcedure: true,
		// Per-provider upstream keys: adding one routes the organization's
		// traffic through the caller's own provider account; the others
		// reweight, disable or remove keys.
		providersconnect.ProvidersServiceAddProviderAPIKeyProcedure:    true,
		providersconnect.ProvidersServiceUpdateAPIKeyWeightProcedure:   true,
		providersconnect.ProvidersServiceToggleAPIKeyProcedure:         true,
		providersconnect.ProvidersServiceDeleteProviderAPIKeyProcedure: true,
		// Whole-gateway configuration (self-hosted; managed refuses these).
		providersconnect.ProvidersServiceSaveConfigYAMLProcedure: true,
		providersconnect.ProvidersServiceReloadConfigProcedure:   true,
	}
	for procedure := range cliBearerOrgAdminProcedures {
		set[procedure] = true
	}
	return set
}()

// cliBearerRoleAllows reports whether a member with role may call procedure
// with a login token. It fails closed: an empty or unknown role is not an
// admin. The role names match contextkeys.IsVerifiedOrgAdmin.
func cliBearerRoleAllows(procedure, role string) bool {
	if !cliBearerOrgAdminProcedures[procedure] {
		return true
	}
	switch strings.TrimSpace(role) {
	case "owner", "admin":
		return true
	}
	return false
}

func (i *APIKeyConnectInterceptor) applyCLIBearerContext(
	ctx context.Context,
	header http.Header,
	procedure string,
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
	if err != nil || identity == nil || !deviceauth.IsCLIClientID(identity.ClientID) {
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
	// Set when the member was found by verified email rather than by the
	// token's own user id. That lookup is good enough to establish
	// membership but not to borrow a role from: email_at_link is not unique.
	viaIdentityLink := false
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
		viaIdentityLink = true
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
	// A token issued before the account was secured (an undone email
	// change) is refused although it is otherwise valid.
	revoked, err := deviceauth.CredentialsRevoked(queryCtx, authDB, membership.UserID, identity.IssuedAt)
	if err != nil {
		logger.WithFields("error", err.Error(), "user_id", membership.UserID).
			Warn("apikey: CLI bearer revocation check failed")
		return reject()
	}
	if revoked {
		return reject()
	}
	// The token is valid and the user is a member. Credential and routing
	// writes additionally need an owner or admin.
	// Two cases where a role must not be trusted for those writes: the
	// member was matched by email rather than by the token's user id, and a
	// managed gateway where the request did not resolve to an instance, so
	// the write would land on the bare organization id.
	adminWriteUnsafe := cliBearerOrgAdminProcedures[procedure] &&
		(viaIdentityLink || (enterprise.ManagedGateway() && tenantID == identity.OrganizationID))
	if adminWriteUnsafe || !cliBearerRoleAllows(procedure, membership.Role) {
		return ctx, i.errorWithCorrelation(
			connect.CodePermissionDenied,
			mferrors.CreateEverstackError(nil, "this action needs an organization owner or admin", ""),
			correlationID,
		)
	}

	ctx = contextkeys.WithUserID(ctx, membership.UserID)
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	ctx = database.WithTenantSchema(ctx, tenantID)
	if role := strings.TrimSpace(membership.Role); role != "" {
		ctx = contextkeys.WithUserRole(ctx, role)
	}
	ctx = contextkeys.WithCLIOrganizationID(ctx, identity.OrganizationID)
	return contextkeys.WithTenantAuthenticated(ctx), nil
}

// applySessionContext validates a session cookie and, on success, returns a
// context carrying the session's user id and primary tenant id along with
// the WithTenantAuthenticated marker. Mirrors the HTTP middleware's
// resolveSession + applySessionAuth pair so admin requests reach
// tenant-scoped handlers with a real tenant in context regardless of which
// transport they entered through.
func (i *APIKeyConnectInterceptor) applySessionContext(ctx context.Context, token string) (context.Context, bool, bool) {
	auth := i.lookupSession(ctx, token)
	if !auth.valid {
		return ctx, false, auth.unavailable
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
	return contextkeys.WithTenantAuthenticated(ctx), true, false
}

// sessionAuth is the cached result of a session-cookie lookup.
type sessionAuth struct {
	valid     bool
	userID    string
	tenantID  string
	role      string // caller's role in the active org (owner/admin/member/viewer)
	expiresAt time.Time
	// unavailable marks a lookup that could not reach the session store.
	// Such a result is a deny and is never cached.
	unavailable bool
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
		if err := orgAdminWriteAllowed(newCtx, proc); err != nil {
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
		if err := orgAdminWriteAllowed(newCtx, proc); err != nil {
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
		if err := orgAdminWriteAllowed(newCtx, proc); err != nil {
			return err
		}

		return next(newCtx, conn)
	}
}

// lookupSession validates a session token against the sessions table and
// resolves the user's primary organisation id. Mirrors the HTTP middleware's
// resolveSession; see that comment for the org-selection rationale.
func (i *APIKeyConnectInterceptor) lookupSession(ctx context.Context, token string) sessionAuth {
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
	// deployments) and, for a row mirrored from Better Auth, confirms the
	// identity session is still live.
	session, err := sessionlive.Lookup(queryCtx, i.sessionDB, token)
	if err != nil {
		if !sessionlive.IsRejected(err) {
			logger.WithFields("error", err.Error()).Warn("apikey: session verification failed")
			return sessionAuth{unavailable: true}
		}
		i.sessionCache.Store(cacheKey, sessionAuth{expiresAt: time.Now().Add(sessionCacheExpiration)})
		return sessionAuth{}
	}
	userID := session.UserID
	// A positive answer is reused for at most sessionCacheExpiration and
	// never past the mirror row's own expiry.
	validUntil := time.Now().Add(sessionCacheExpiration)
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
				logger.WithFields("error", err.Error(), "user_id", userID).Warn("apikey: host membership lookup failed")
			}
			i.sessionCache.Store(cacheKey, sessionAuth{expiresAt: time.Now().Add(sessionCacheExpiration)})
			return sessionAuth{}
		}
		auth := sessionAuth{valid: true, userID: userID, tenantID: hostScope.InstanceID, role: role,
			expiresAt: validUntil}
		i.sessionCache.Store(cacheKey, auth)
		return auth
	}

	// No hostname-selected instance. Prefer instance_id from tenant_config
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
		expiresAt: validUntil,
	}
	i.sessionCache.Store(cacheKey, auth)
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

func (i *APIKeyConnectInterceptor) registerCacheInvalidation() {
	apikeylib.RegisterCacheInvalidator(func(hash, keyID string) {
		i.cache.Range(func(k, v interface{}) bool {
			if entry, ok := v.(cacheEntry); ok && (hash != "" && k == hash || keyID != "" && entry.keyID == keyID) {
				i.cache.Delete(k)
			}
			return true
		})
	})
}

func admitInference(ctx context.Context, key, tenant string, surfaces []string, rpm *int) (context.Context, error) {
	ctx = apikeylib.Accept(ctx, key, tenant, surfaces, apikeylib.SurfaceInference, rpm)
	if n := apikeylib.RetryAfter(ctx); n > 0 {
		err := connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded"))
		err.Meta().Set("Retry-After", strconv.Itoa(n))
		return ctx, err
	}
	return ctx, nil
}
