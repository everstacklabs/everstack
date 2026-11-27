package model_discovery

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/domain/custom_models"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func customModelRows(tenantID, model string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "provider_name", "model_name", "display_name",
		"model_metadata", "source", "is_active", "created_at", "updated_at",
	}).AddRow("id-"+tenantID, tenantID, "ollama", model, "Display "+model, []byte(`{}`), "manual", true, now, now)
}

// The resolver runs on every model lookup, ahead of the gateway's fast route
// cache. It must hit the database once per tenant per TTL, not per request,
// and must never serve one tenant's routes to another.
func TestResolverCachesRoutesPerTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	resolver := NewCustomModelResolverService(custom_models.NewRepository(sqlx.NewDb(rawDB, "sqlmock"), true), true)
	ctxA := contextkeys.WithTenantID(context.Background(), "cache-tenant-a")
	ctxB := contextkeys.WithTenantID(context.Background(), "cache-tenant-b")
	InvalidateCustomModels("cache-tenant-a")
	InvalidateCustomModels("cache-tenant-b")

	// Exactly one query per tenant is expected for everything below until
	// the invalidation.
	mock.ExpectQuery("FROM custom_models").WithArgs("cache-tenant-a").WillReturnRows(customModelRows("cache-tenant-a", "model-a"))
	mock.ExpectQuery("FROM custom_models").WithArgs("cache-tenant-b").WillReturnRows(customModelRows("cache-tenant-b", "model-b"))

	for i := 0; i < 5; i++ {
		provider, model, found, err := resolver.ResolveCustomModel(ctxA, "MODEL-A")
		if err != nil || !found || provider != "ollama" || model != "model-a" {
			t.Fatalf("tenant a lookup %d: provider=%q model=%q found=%v err=%v", i, provider, model, found, err)
		}
	}
	if _, _, found, _ := resolver.ResolveCustomModel(ctxA, "display model-a"); !found {
		t.Fatal("display name should resolve")
	}
	// Tenant b has its own model and cannot see tenant a's.
	if _, _, found, err := resolver.ResolveCustomModel(ctxB, "model-a"); err != nil || found {
		t.Fatalf("tenant b resolved tenant a's model: found=%v err=%v", found, err)
	}
	if _, _, found, _ := resolver.ResolveCustomModel(ctxB, "model-b"); !found {
		t.Fatal("tenant b should resolve its own model")
	}
	// And tenant a cannot see tenant b's, still without a new query.
	if _, _, found, _ := resolver.ResolveCustomModel(ctxA, "model-b"); found {
		t.Fatal("tenant a resolved tenant b's model")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected one query per tenant: %v", err)
	}

	// A write invalidates only that tenant.
	InvalidateCustomModels("cache-tenant-a")
	mock.ExpectQuery("FROM custom_models").WithArgs("cache-tenant-a").WillReturnRows(customModelRows("cache-tenant-a", "model-a2"))
	if _, _, found, _ := resolver.ResolveCustomModel(ctxA, "model-a"); found {
		t.Fatal("a removed model was still served after invalidation")
	}
	if _, _, found, _ := resolver.ResolveCustomModel(ctxA, "model-a2"); !found {
		t.Fatal("the new model should resolve after invalidation")
	}
	if _, _, found, _ := resolver.ResolveCustomModel(ctxB, "model-b"); !found {
		t.Fatal("tenant b's cache must survive tenant a's invalidation")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolverSkipsTheDatabaseOnManagedWithoutATenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	resolver := NewCustomModelResolverService(custom_models.NewRepository(sqlx.NewDb(rawDB, "sqlmock"), true), true)
	if _, _, found, err := resolver.ResolveCustomModel(context.Background(), "model-a"); err != nil || found {
		t.Fatalf("no tenant must resolve nothing, got found=%v err=%v", found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
