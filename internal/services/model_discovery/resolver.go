package model_discovery

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/domain/custom_models"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// CustomModelResolverService implements the gateway.CustomModelResolver interface
type CustomModelResolverService struct {
	repo    *custom_models.Repository
	managed bool
}

// NewCustomModelResolverService creates a new custom model resolver
func NewCustomModelResolverService(repo *custom_models.Repository, managed bool) *CustomModelResolverService {
	return &CustomModelResolverService{repo: repo, managed: managed}
}

// ResolveCustomModel attempts to resolve a model name to a custom model configuration
// Returns: providerName, customModelName, found, error
func (s *CustomModelResolverService) ResolveCustomModel(ctx context.Context, modelName string) (string, string, bool, error) {
	if s.repo == nil {
		return "", "", false, nil
	}
	tenantID := contextkeys.GetTenantID(ctx)
	if s.managed && tenantID == "" {
		return "", "", false, nil
	}

	routes, err := s.routesFor(ctx, tenantID)
	if err != nil {
		return "", "", false, err
	}
	if route, ok := routes[strings.ToLower(modelName)]; ok {
		return route.provider, route.model, true, nil
	}
	return "", "", false, nil
}

// customRoute is one resolved custom model.
type customRoute struct{ provider, model string }

// customRouteEntry is a tenant's custom routes as of loadedAt.
type customRouteEntry struct {
	routes   map[string]customRoute
	loadedAt time.Time
}

// The resolver is built to sit in the gateway's model lookup path, so it must
// not query the database per request. It is not attached to the gateway
// router today: custom models do not route on master, and turning that on is
// a separate change. Each
// tenant's custom routes are cached for customRouteTTL. Most tenants have
// none, and that empty answer is cached too.
//
// The cache is keyed by tenant and shared by every resolver in the process.
// Writes through the model discovery API drop the tenant's entry with
// InvalidateCustomModels; other replicas pick the change up within the TTL.
const (
	customRouteTTL        = 30 * time.Second
	customRouteMaxTenants = 10000
)

var (
	customRouteMu    sync.RWMutex
	customRouteCache = map[string]customRouteEntry{}
)

// InvalidateCustomModels drops the cached custom routes of one tenant. Call
// it after creating, updating or deleting a custom model.
func InvalidateCustomModels(tenantID string) {
	customRouteMu.Lock()
	delete(customRouteCache, tenantID)
	customRouteMu.Unlock()
}

func (s *CustomModelResolverService) routesFor(ctx context.Context, tenantID string) (map[string]customRoute, error) {
	customRouteMu.RLock()
	entry, ok := customRouteCache[tenantID]
	customRouteMu.RUnlock()
	if ok && time.Since(entry.loadedAt) < customRouteTTL {
		return entry.routes, nil
	}

	models, err := s.repo.List(ctx, tenantID)
	if err != nil {
		// Not cached: the next lookup tries again.
		return nil, err
	}
	routes := make(map[string]customRoute, len(models)*2)
	// Match on both model_name and display_name, case-insensitively. A model
	// name wins over another model's display name, and the tenant's own row
	// wins over a legacy row with no owner: ownerless rows go in first so
	// the tenant's rows overwrite them.
	for _, ownerless := range []bool{true, false} {
		for _, model := range models {
			if !model.IsActive || (model.TenantID == "") != ownerless {
				continue
			}
			routes[strings.ToLower(model.ModelName)] = customRoute{provider: model.ProviderName, model: model.ModelName}
		}
	}
	for _, model := range models {
		if !model.IsActive {
			continue
		}
		key := strings.ToLower(model.DisplayName)
		if _, taken := routes[key]; !taken && key != "" {
			routes[key] = customRoute{provider: model.ProviderName, model: model.ModelName}
		}
	}

	customRouteMu.Lock()
	if len(customRouteCache) >= customRouteMaxTenants {
		customRouteCache = map[string]customRouteEntry{}
	}
	customRouteCache[tenantID] = customRouteEntry{routes: routes, loadedAt: time.Now()}
	customRouteMu.Unlock()
	return routes, nil
}
