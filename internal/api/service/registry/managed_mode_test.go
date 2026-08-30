package registry

import (
	"testing"

	"github.com/jmoiron/sqlx"
)

// Managed gates LocalTenantInterceptor, which injects a single local tenant id
// into any request that reaches it without one. On a gateway serving other
// people's tenants that id belongs to nobody, so the flag has to survive
// unchanged from the wiring in cmd/serve to BuildInterceptors.
func TestSetManagedModeRoundTrips(t *testing.T) {
	r := &Registry{}
	if r.Managed {
		t.Fatal("Managed must default to false so a standalone gateway keeps injecting")
	}

	r.SetManagedMode(true)
	if !r.Managed {
		t.Fatal("SetManagedMode(true) did not stick; the Connect path would inject a phantom tenant")
	}

	r.SetManagedMode(false)
	if r.Managed {
		t.Fatal("SetManagedMode(false) did not stick")
	}
}

func TestSetCLIAuthorizationDBRoundTrips(t *testing.T) {
	r := &Registry{}
	db := &sqlx.DB{}

	r.SetCLIAuthorizationDB(db)
	if r.cliAuthDB != db {
		t.Fatal("SetCLIAuthorizationDB did not preserve the platform authorization pool")
	}
}
