package v1

import (
	"context"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	catalogsvc "github.com/everstacklabs/everstack/internal/services/catalog"
)

// The gateway installs the catalog service on its SERVER context at startup.
// Request contexts are built from scratch by the RPC stack and never inherit
// it, which is why the chat processor copies contextkeys.ProviderRepo across
// by hand.
//
// bootstrapFromDatabase used to look the catalog up on whatever context it was
// handed. In shared multi-tenant mode that is the request context
// (EnsureProvidersForRequest -> bootstrapFromDatabase), so the lookup missed,
// every per-tenant provider bundle was built with CatalogCache = nil, and
// TracingMiddleware skipped cost calculation entirely for those tenants.
func TestCatalogServiceForFallsBackToServerContext(t *testing.T) {
	catalog := catalogsvc.NewService(nil, nil, nil)
	server := &Server{ctx: context.WithValue(context.Background(), contextkeys.CatalogService, catalog)}

	// A bare request context, exactly what EnsureProvidersForRequest passes.
	requestCtx := context.Background()

	// This is the lookup the old code performed, and why the bug existed.
	if requestCtx.Value(contextkeys.CatalogService) != nil {
		t.Fatal("precondition: a bare request context must not carry the catalog service")
	}

	got := server.catalogServiceFor(requestCtx)
	if got == nil {
		t.Fatal("catalogServiceFor returned nil for a request context; per-tenant bundles get no catalog and lose cost calculation")
	}
	if got != catalog {
		t.Errorf("catalogServiceFor returned a different catalog service than the one on the server context")
	}
	if got.GetCache() == nil {
		t.Error("resolved catalog service must expose a cache; a nil cache is what leaves CostCalculator nil")
	}
}

// The boot path calls bootstrapFromDatabase with the server context itself.
// An explicitly supplied catalog must still win so that path is unchanged.
func TestCatalogServiceForPrefersExplicitRequestContext(t *testing.T) {
	serverCatalog := catalogsvc.NewService(nil, nil, nil)
	explicitCatalog := catalogsvc.NewService(nil, nil, nil)

	server := &Server{ctx: context.WithValue(context.Background(), contextkeys.CatalogService, serverCatalog)}
	ctx := context.WithValue(context.Background(), contextkeys.CatalogService, explicitCatalog)

	if got := server.catalogServiceFor(ctx); got != explicitCatalog {
		t.Error("an explicit request-context catalog must take precedence over the server context")
	}
}

func TestCatalogServiceForIsNilSafe(t *testing.T) {
	// No catalog anywhere: callers must get nil rather than panic, and fall
	// back to running without cost calculation as they always have.
	server := &Server{ctx: context.Background()}
	if got := server.catalogServiceFor(context.Background()); got != nil {
		t.Errorf("expected nil when no catalog is registered, got %v", got)
	}
	if got := server.catalogServiceFor(nil); got != nil {
		t.Errorf("expected nil for a nil context, got %v", got)
	}

	var nilServer *Server
	if got := nilServer.catalogServiceFor(context.Background()); got != nil {
		t.Errorf("expected nil for a nil server, got %v", got)
	}
}

// Shared mode must not gate DB-configured models by the catalog whitelist.
// This is load-bearing for the catalog fix: before it, shared-mode bootstraps
// left `allowed` nil purely BECAUSE the catalog lookup failed, and the filter
// loop passes everything when allowed == nil. Resolving the catalog correctly
// makes `allowed` non-nil, so enforcement has to be driven by shared mode read
// from the server context -- otherwise fixing cost would start silently
// dropping every cloud model that is not in the catalog whitelist.
func TestSharedModeDisablesCatalogWhitelistEnforcement(t *testing.T) {
	shared := &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)}
	if !shared.isSharedMode() {
		t.Fatal("precondition: server context marks shared mode")
	}
	if enforce := !shared.isSharedMode(); enforce {
		t.Error("shared mode must not enforce the catalog whitelist over DB-configured models")
	}

	single := &Server{ctx: context.Background()}
	if enforce := !single.isSharedMode(); !enforce {
		t.Error("single-tenant mode must keep enforcing the catalog whitelist")
	}
}
