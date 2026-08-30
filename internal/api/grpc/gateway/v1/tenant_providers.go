package v1

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/database"
)

// tenantBundle is the gateway runtime state owned by a single tenant: the
// provider registry, the router, and the per-provider sampling overrides.
//
// Before this type existed the gateway kept one Registry/Router on the Server
// struct and rewrote them on every request whenever the active tenant schema
// changed. In shared multi-tenant mode that meant concurrent requests from
// different tenants raced on the same global pointers — request A could be
// halfway through resolving a model when request B's reload swapped the
// registry out from under it, so A's stream finished against B's API keys.
// That is the LLM-key cross-tenant leak the user hit. With per-tenant
// bundles the registry/router for tenant A is stable for the lifetime of a
// request even while tenant B's request reloads its own bundle in parallel.
type tenantBundle struct {
	reg              *gw.Registry
	router           *gw.Router
	providerDefaults map[string]modelRequestDefaults
	modelDefaults    map[string]modelRequestDefaults
	loadedAt         time.Time
}

// tenantBundleCache holds a per-tenant bundle keyed by the tenant identity.
// In shared mode the key is the request's tenant schema (one schema = one
// tenant). In non-shared / dedicated mode there is exactly one tenant for
// the life of the process and we use the defaultBundle pointer instead, so
// the cache stays empty.
type tenantBundleCache struct {
	bundles sync.Map // map[string]*tenantBundle

	// loadLocks serialises bootstraps of the same tenant so two concurrent
	// requests don't both query the DB and build duplicate bundles. We use
	// per-key sync.Mutex via sync.Map; each key is collected lazily.
	loadLocks sync.Map // map[string]*sync.Mutex
}

// loadLockFor returns (and creates if necessary) the mutex for a tenant key.
func (c *tenantBundleCache) loadLockFor(key string) *sync.Mutex {
	if mu, ok := c.loadLocks.Load(key); ok {
		return mu.(*sync.Mutex)
	}
	fresh := &sync.Mutex{}
	actual, _ := c.loadLocks.LoadOrStore(key, fresh)
	return actual.(*sync.Mutex)
}

// get returns the cached bundle for a tenant key, or nil if missing/stale.
func (c *tenantBundleCache) get(key string, ttl time.Duration) *tenantBundle {
	v, ok := c.bundles.Load(key)
	if !ok {
		return nil
	}
	b := v.(*tenantBundle)
	if ttl > 0 && !b.loadedAt.IsZero() && time.Since(b.loadedAt) > ttl {
		return nil
	}
	return b
}

func (c *tenantBundleCache) store(key string, b *tenantBundle) {
	b.loadedAt = time.Now()
	c.bundles.Store(key, b)
}

// tenantKeyFromContext picks the cache key for the request. We prefer the
// tenant schema because that is what the existing bootstrap pipeline uses to
// scope DB queries; the org id is the same identity in cloud, but the schema
// is what survives through the database driver's search_path. An empty key
// means "no tenant scoping in play" — callers fall back to the default
// bundle.
func tenantKeyFromContext(ctx context.Context) string {
	if schema := database.TenantSchemaFromContext(ctx); schema != "" {
		return schema
	}
	if orgID := contextkeys.GetTenantID(ctx); orgID != "" {
		return orgID
	}
	return ""
}

// providersFor returns the bundle that should serve the current request. In
// shared mode the bundle is per-tenant; in single-tenant mode it is the
// process-wide default. Empty bundle (no providers configured) is a valid
// state — handlers will see a nil registry and fail the request with a
// clean "no providers configured" error rather than reading another
// tenant's keys.
func (s *Server) providersFor(ctx context.Context) *tenantBundle {
	if s == nil {
		return nil
	}
	if !s.isSharedMode() {
		return s.defaultBundle.Load()
	}
	key := tenantKeyFromContext(ctx)
	if key == "" {
		// No tenant ctx in shared mode — refuse to fall back to a
		// neighbouring tenant's bundle. Callers see a nil bundle and
		// surface a permission error to the client. This is the same
		// fail-closed posture the auth layer adopted on 2026-05-06.
		return nil
	}
	return s.tenantBundles.get(key, providerRefreshStaleness)
}

// ProviderBundleForRequest atomically returns the registry and router for one
// request. Consumers outside the gateway (notably the agent runtime) must use
// this instead of calling GetRegistry and GetRouter separately: a provider
// refresh can replace the tenant bundle between two independent calls.
func (s *Server) ProviderBundleForRequest(ctx context.Context) (*gw.Registry, *gw.Router, error) {
	// Retry once for the narrow TTL-boundary race where Ensure observes a
	// still-fresh bundle and providersFor observes it just after expiry.
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.EnsureProvidersForRequest(ctx); err != nil {
			return nil, nil, err
		}
		bundle := s.providersFor(ctx)
		if bundle != nil && bundle.reg != nil && bundle.router != nil {
			return bundle.reg, bundle.router, nil
		}
	}
	return nil, nil, fmt.Errorf("provider bundle unavailable for request")
}

// isSharedMode reports whether the gateway is serving multiple tenants out
// of a single process. The flag is set at boot from cmd/serve/start_api.go;
// the value never changes for the life of the process.
func (s *Server) isSharedMode() bool {
	if s.ctx == nil {
		return false
	}
	shared, _ := s.ctx.Value(contextkeys.SharedGatewayMode).(bool)
	return shared
}

// regFor returns the registry the caller should use for this request, or nil
// if no tenant bundle is available. Callers must always nil-check the return
// — a nil registry means "no providers configured for this tenant" and must
// surface as a 4xx rather than a panic.
func (s *Server) regFor(ctx context.Context) *gw.Registry {
	b := s.providersFor(ctx)
	if b == nil {
		return nil
	}
	return b.reg
}

// routerFor returns the router for the request's tenant, or nil if missing.
func (s *Server) routerFor(ctx context.Context) *gw.Router {
	b := s.providersFor(ctx)
	if b == nil {
		return nil
	}
	return b.router
}

// providerDefaultsFor returns the provider-wide request defaults for the
// request's tenant: the values that apply to every model under that provider
// unless the model overrides them.
func (s *Server) providerDefaultsFor(ctx context.Context, providerName string) (modelRequestDefaults, bool) {
	b := s.providersFor(ctx)
	if b == nil || len(b.providerDefaults) == 0 {
		return modelRequestDefaults{}, false
	}
	v, ok := b.providerDefaults[providerName]
	return v, ok
}

func (s *Server) modelDefaultsFor(ctx context.Context, providerName, modelName string) (modelRequestDefaults, bool) {
	b := s.providersFor(ctx)
	if b == nil || len(b.modelDefaults) == 0 {
		return modelRequestDefaults{}, false
	}
	v, ok := b.modelDefaults[modelDefaultsKey(providerName, modelName)]
	return v, ok
}

// installBundle caches a bundle for a tenant key, and (if no tenant key was
// resolvable, e.g. single-tenant boot) sets it as the default bundle.
func (s *Server) installBundle(key string, b *tenantBundle) {
	if b == nil {
		return
	}
	if key == "" {
		s.defaultBundle.Store(b)
		return
	}
	s.tenantBundles.store(key, b)
	if !s.isSharedMode() {
		// In single-tenant deployments callers without ctx (startup
		// priming, tests) still expect to read through the default
		// bundle pointer.
		s.defaultBundle.Store(b)
	}
}

// invalidateBundle drops the cached bundle for a tenant so the next request
// re-bootstraps from the DB. Used after admin writes that change provider
// configs (configure / delete / toggle).
func (s *Server) invalidateBundle(key string) {
	if key == "" {
		s.defaultBundle.Store(nil)
		return
	}
	s.tenantBundles.bundles.Delete(key)
}

// listProviderConfigs returns provider configurations scoped to the tenant
// in ctx when one is set, falling back to the unscoped repo list for
// boot-time or single-tenant paths.
//
// Without this scoping the gateway runtime path
// (bootstrapFromDatabase / failedKeyAndRotateProvider / ListModels) loads
// EVERY tenant's provider rows into the per-tenant bundle and serves them
// to whoever's request triggered the reload — that is the LLM-key
// cross-tenant leak the schema-per-tenant rip-out (commit 853e9e93)
// reopened. The bundles are still keyed by tenant, but the data feeding
// them came from a global SELECT.
func listProviderConfigs(ctx context.Context, repo *provider_config.Repository) ([]*provider_config.Configuration, error) {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return repo.ListForOrg(ctx, tid)
	}
	return repo.List(ctx)
}

// dropAllBundles is a debugging / test helper.
func (s *Server) dropAllBundles() {
	s.tenantBundles.bundles.Range(func(k, _ any) bool {
		s.tenantBundles.bundles.Delete(k)
		return true
	})
	s.defaultBundle.Store(nil)
	logger.Debug("gateway: dropped all tenant bundles")
}

// Compile-time assertion that atomic.Pointer is the type we expect — keeps
// the import honest if Server is restructured.
var _ atomic.Pointer[tenantBundle]
