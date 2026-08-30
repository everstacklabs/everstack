package tenancy

import (
	"context"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	pkgdb "github.com/everstacklabs/everstack/pkg/database"
	"github.com/everstacklabs/everstack/pkg/tenant"
	"github.com/google/uuid"
)

func TestDefaultSelfHostedTenantIDIsUUID(t *testing.T) {
	if _, err := uuid.Parse(defaultSelfHostedTenantID); err != nil {
		t.Fatalf("default self-hosted tenant id must be UUID-shaped: %v", err)
	}
}

func TestLocalScopeResolverInjectsStandaloneFallback(t *testing.T) {
	r := NewLocalScopeResolver(nil, false)
	ctx := r.Inject(context.Background())

	if got := contextkeys.GetTenantID(ctx); got != defaultSelfHostedTenantID {
		t.Fatalf("tenant id = %q, want %q", got, defaultSelfHostedTenantID)
	}
	if got := contextkeys.ExtractTenantID(ctx); got != defaultSelfHostedTenantID {
		t.Fatalf("extracted tenant id = %q, want %q", got, defaultSelfHostedTenantID)
	}
	if got := pkgdb.TenantSchemaFromContext(ctx); got != defaultSelfHostedTenantID {
		t.Fatalf("tenant schema = %q, want %q", got, defaultSelfHostedTenantID)
	}
}

func TestLocalScopeResolverKeepsExistingTenant(t *testing.T) {
	r := NewLocalScopeResolver(nil, false)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-real")
	ctx = r.Inject(ctx)

	if got := contextkeys.GetTenantID(ctx); got != "tenant-real" {
		t.Fatalf("tenant id = %q, want tenant-real", got)
	}
	if got := pkgdb.TenantSchemaFromContext(ctx); got != "tenant-real" {
		t.Fatalf("tenant schema = %q, want tenant-real", got)
	}
}

func TestLocalScopeResolverSkipsSharedMode(t *testing.T) {
	r := NewLocalScopeResolver(nil, true)
	ctx := r.Inject(context.Background())

	if got := contextkeys.GetTenantID(ctx); got != "" {
		t.Fatalf("tenant id = %q, want empty", got)
	}
}

func TestLocalScopeResolverSkipsTenantConfig(t *testing.T) {
	r := NewLocalScopeResolver(nil, false)
	ctx := tenant.WithConfig(context.Background(), &tenant.Config{InstanceID: "inst-cloud"})
	ctx = r.Inject(ctx)

	if got := contextkeys.GetTenantID(ctx); got != "" {
		t.Fatalf("tenant id = %q, want empty", got)
	}
}
