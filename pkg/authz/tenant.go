package authz

import "context"

type tenantCtxKey struct{}

// ContextWithTenant scopes every tuple-store read and write on ctx to tenantID.
// The engine threads ctx through unchanged, so a Check started under one tenant
// can never see another tenant's tuples. The Postgres store REQUIRES a non-empty
// tenant and fails closed without one; the in-memory store treats the value
// (including "") as a namespace, which keeps single-tenant tests simple.
func ContextWithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, tenantID)
}

// TenantFromContext returns the tenant scoping ctx, or "" if none is set.
func TenantFromContext(ctx context.Context) string {
	v, _ := ctx.Value(tenantCtxKey{}).(string)
	return v
}
