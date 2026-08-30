package database

import (
	"context"

	pkgdb "github.com/everstacklabs/everstack/pkg/database"
	"github.com/jmoiron/sqlx"
)

// tenantConnCtxKey is the context key for a per-tenant scoped *sqlx.Conn.
// Deprecated: use WithTenantSchema + pgxpool hooks instead.
type tenantConnCtxKey struct{}

// WithTenantConn stores a tenant-scoped *sqlx.Conn in the context.
// Deprecated: use WithTenantSchema instead; the pgxpool hook approach
// sets search_path automatically per-query.
func WithTenantConn(ctx context.Context, conn *sqlx.Conn) context.Context {
	return context.WithValue(ctx, tenantConnCtxKey{}, conn)
}

// TenantConnFromContext retrieves the tenant-scoped *sqlx.Conn from context, or nil.
// Deprecated: see WithTenantConn.
func TenantConnFromContext(ctx context.Context) *sqlx.Conn {
	conn, _ := ctx.Value(tenantConnCtxKey{}).(*sqlx.Conn)
	return conn
}

// WithTenantSchema stores a tenant identifier (`inst_<uuid>` shaped)
// in the context.
//
// **Misleading name, kept for backwards compatibility.** This used to
// drive a tenant-aware driver that issued `SET search_path TO <schema>`
// before each query so reads/writes resolved to a per-tenant Postgres
// schema. That model was never fully adopted (per-tenant schemas got
// migrations replayed into them but every projection / query handler
// kept using unqualified table names that resolved to `everstack`),
// and the search_path machinery was removed in the cleanup that
// landed alongside the tenant-isolation hardening. The value flowing
// through this key is now an opaque tenant identifier — same shape
// as before for ClickHouse `tenant_id` data continuity.
//
// Tenant isolation is enforced via `WHERE tenant_id = $N` in every
// handler. RLS policies on top of that are the durable defense in
// depth.
func WithTenantSchema(ctx context.Context, schema string) context.Context {
	return pkgdb.WithTenantSchema(ctx, schema)
}

// TenantSchemaFromContext retrieves the tenant identifier from context,
// or "". See WithTenantSchema for naming history.
func TenantSchemaFromContext(ctx context.Context) string {
	return pkgdb.TenantSchemaFromContext(ctx)
}

// ClickHouse: shared single-database mode with tenant_id column filtering.
// No per-tenant USE {database} context keys needed.
