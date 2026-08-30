package v1

import (
	"context"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestProviderBundleForRequestReturnsOneTenantBundle(t *testing.T) {
	serverCtx := context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)
	server := &Server{ctx: serverCtx}

	registryA := gw.NewRegistry()
	routerA := gw.NewRouter(registryA, nil)
	registryB := gw.NewRegistry()
	routerB := gw.NewRouter(registryB, nil)
	server.installBundle("tenant-a", &tenantBundle{reg: registryA, router: routerA})
	server.installBundle("tenant-b", &tenantBundle{reg: registryB, router: routerB})

	tenantA := contextkeys.WithTenantID(context.Background(), "tenant-a")
	gotRegistry, gotRouter, err := server.ProviderBundleForRequest(tenantA)
	if err != nil {
		t.Fatalf("resolve tenant A bundle: %v", err)
	}
	if gotRegistry != registryA || gotRouter != routerA {
		t.Fatal("provider source returned pointers from different tenant bundles")
	}
	if _, _, err := server.ProviderBundleForRequest(context.Background()); err == nil {
		t.Fatal("provider source did not fail closed without tenant context")
	}
}
