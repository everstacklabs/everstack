package runtime

import (
	"context"

	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// ContextWithTenantIdentity binds a tenant identity trusted by an internal
// worker to both context keys consumed by provider and persistence routing.
// It intentionally does not mark the context as externally authenticated.
func ContextWithTenantIdentity(ctx context.Context, tenantID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if tenantID == "" {
		return ctx
	}
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	return database.WithTenantSchema(ctx, tenantID)
}
