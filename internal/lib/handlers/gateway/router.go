package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
)

// ModelRoute describes how to route a model request to a provider.
type ModelRoute struct {
	ProviderName string
	ModelName    string
	IsCustom     bool // Whether this route is for a custom model
}

// CustomModelResolver defines an interface for resolving custom models
type CustomModelResolver interface {
	ResolveCustomModel(ctx context.Context, modelName string) (providerName string, customModelName string, found bool, err error)
}

// Router resolves a provider from a model name and configuration.
type Router struct {
	registry *Registry
	// modelToProvider can be filled from gateway.yaml: model -> provider name
	modelToProvider map[string]string
	// modelRoutes maps lower-cased public model alias to explicit route
	modelRoutes map[string]ModelRoute
	// fallbacks maps alias -> ordered fallback routes
	fallbacks map[string][]ModelRoute
	// customResolver for resolving custom models from database
	customResolver CustomModelResolver
}

func NewRouter(registry *Registry, modelToProvider map[string]string) *Router {
	return &Router{registry: registry, modelToProvider: modelToProvider}
}

// SetRegistry updates the provider registry reference.
func (r *Router) SetRegistry(registry *Registry) { r.registry = registry }

// SetModelToProvider updates the model->provider mapping.
func (r *Router) SetModelToProvider(modelToProvider map[string]string) {
	r.modelToProvider = modelToProvider
}

// SetModelRoutes configures explicit model routes (alias -> route).
func (r *Router) SetModelRoutes(routes map[string]ModelRoute) { r.modelRoutes = routes }

// SetFallbacks configures ordered fallback routes per alias.
func (r *Router) SetFallbacks(fb map[string][]ModelRoute) { r.fallbacks = fb }

// SetCustomResolver sets the custom model resolver for looking up custom models from database
func (r *Router) SetCustomResolver(resolver CustomModelResolver) { r.customResolver = resolver }

// FallbacksFor returns fallback routes for a public alias, if any.
func (r *Router) FallbacksFor(alias string) []ModelRoute {
	if r.fallbacks == nil {
		return nil
	}
	return r.fallbacks[strings.ToLower(alias)]
}

// ProviderForRoute returns the provider instance for a given route.
func (r *Router) ProviderForRoute(route ModelRoute) (Provider, bool) {
	return r.registry.Get(route.ProviderName)
}

// Resolve finds the provider and canonical model for the given requested model.
// Deprecated: Use ResolveWithContext for custom model support
func (r *Router) Resolve(requestedModel string) (Provider, ModelRoute, error) {
	return r.ResolveWithContext(context.Background(), requestedModel)
}

// ResolveWithContext finds the provider and canonical model for the given requested model.
// It checks custom models first, then falls back to catalog-based routing.
func (r *Router) ResolveWithContext(ctx context.Context, requestedModel string) (Provider, ModelRoute, error) {
	if requestedModel == "" {
		return nil, ModelRoute{}, errors.New("model is required")
	}

	// Accept the "@provider/model" reference emitted by the admin UI (and some
	// SDKs) so the whole gateway understands it, not just bare model ids. When
	// the named provider is registered, route straight to it; otherwise strip
	// the "@provider/" prefix and resolve the bare model through the catalog
	// path below.
	if provName, bareModel, ok := parseProviderRef(requestedModel); ok {
		if p, exists := r.lookupProvider(provName); exists {
			return p, ModelRoute{ProviderName: provName, ModelName: bareModel}, nil
		}
		requestedModel = bareModel
	}

	// 0. Check FastPath RouterCache
	// This avoids regex matching and map iteration for frequently accessed models.
	if engine := fastpath.GetGlobalEngine(); engine != nil && engine.RouterCache() != nil {
		if info, ok := engine.RouterCache().Resolve(requestedModel); ok {
			// Type assert the provider
			if p, ok := info.ProviderRef.(Provider); ok {
				return p, ModelRoute{
					ProviderName: info.Name,
					ModelName:    info.ModelName,
					IsCustom:     info.IsCustom,
				}, nil
			}
		}
	}

	// 1. Check custom models first (highest priority for user-configured models)
	if r.customResolver != nil {
		providerName, customModelName, found, err := r.customResolver.ResolveCustomModel(ctx, requestedModel)
		if err == nil && found {
			if p, exists := r.registry.Get(providerName); exists {
				return p, ModelRoute{
					ProviderName: providerName,
					ModelName:    customModelName,
					IsCustom:     true,
				}, nil
			}
		}
		// If error occurred during custom model resolution, log it but continue to catalog
		// This ensures the gateway doesn't fail if the database is temporarily unavailable
	}

	// 2. Explicit route map takes precedence (from configuration)
	if r.modelRoutes != nil {
		if route, ok := r.modelRoutes[strings.ToLower(requestedModel)]; ok {
			if p, exists := r.registry.Get(route.ProviderName); exists {
				return p, route, nil
			}
		}
	}

	// 3. Consult explicit mapping if present (from gateway.yaml)
	if r.modelToProvider != nil {
		if providerName, ok := r.modelToProvider[strings.ToLower(requestedModel)]; ok {
			if p, exists := r.registry.Get(providerName); exists {
				return p, ModelRoute{ProviderName: providerName, ModelName: requestedModel}, nil
			}
		}
	}

	// 4. Ask each provider if it supports the requested model (catalog lookup)
	for name, p := range r.registry.All() {
		if p.SupportsModel(requestedModel) {
			return p, ModelRoute{ProviderName: name, ModelName: requestedModel}, nil
		}
	}

	// Model not found - return non-retriable error that should NOT trigger fallback
	return nil, ModelRoute{}, &ErrModelNotFound{
		RequestedModel: requestedModel,
		Message:        "no provider found for model (model may not be activated or configured)",
	}
}

// parseProviderRef splits an "@provider/model" reference (e.g.
// "@openai/gpt-5.5") into its provider and bare model. Returns ok=false for any
// other form so bare model ids fall straight through.
func parseProviderRef(s string) (provider, model string, ok bool) {
	if len(s) < 2 || s[0] != '@' {
		return "", "", false
	}
	rest := s[1:]
	i := strings.IndexByte(rest, '/')
	if i <= 0 || i >= len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// lookupProvider finds a registered provider by name, case-insensitively —
// registry keys are provider.Name(), whose casing may differ from the name in
// an "@provider/" prefix.
func (r *Router) lookupProvider(name string) (Provider, bool) {
	if p, ok := r.registry.Get(name); ok {
		return p, true
	}
	lower := strings.ToLower(name)
	for k, p := range r.registry.All() {
		if strings.ToLower(k) == lower {
			return p, true
		}
	}
	return nil, false
}
