// Package database provides shared database context helpers for cross-module use.
// The canonical connection management remains in internal/database; this package
// only exports the context helpers needed by external modules (e.g. the cloud repo).
package database

import "context"

// tenantSchemaCtxKey is the context key for the tenant schema name.
type tenantSchemaCtxKey struct{}

// WithTenantSchema stores a tenant schema name in the context.
// The pgxpool BeforeAcquire hook reads this to SET search_path per-query.
func WithTenantSchema(ctx context.Context, schema string) context.Context {
	return context.WithValue(ctx, tenantSchemaCtxKey{}, schema)
}

// TenantSchemaFromContext retrieves the tenant schema name from context, or "".
func TenantSchemaFromContext(ctx context.Context) string {
	s, _ := ctx.Value(tenantSchemaCtxKey{}).(string)
	return s
}
