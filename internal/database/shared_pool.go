package database

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// OpenShared opens a postgres pool with a fixed default search_path
// (`<defaultSchema>, public`).
//
// Historical context: this function used to wrap every connection in
// a tenant-aware driver that issued `SET search_path TO <inst_uuid>`
// before each query, with the goal of routing reads/writes to a
// per-tenant schema. The schema-per-tenant model was never fully
// adopted — every projection / query handler still used unqualified
// table names that resolved to the default `everstack` schema, and
// the per-tenant schemas got migrations replayed into them but stayed
// empty. The driver was theatre.
//
// Tenant isolation is now enforced via `WHERE tenant_id = $N` in
// every query handler (see #42 / #43 / #44 / #46 / #47). RLS policies
// on top of that — tracked separately — are the durable defense in
// depth. WithTenantSchema / TenantSchemaFromContext remain in place
// as a tenant-identifier carrier for the ClickHouse `tenant_id`
// column (whose value is still the inst_<uuid> string for data
// continuity), but they no longer affect search_path.
func OpenShared(ctx context.Context, cfg Config, defaultSchema string) (*Conn, error) {
	pgxCfg, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("shared pool: parse dsn: %w", err)
	}

	if pgxCfg.RuntimeParams == nil {
		pgxCfg.RuntimeParams = make(map[string]string)
	}
	pgxCfg.RuntimeParams["search_path"] = defaultSchema + ", public"

	sqlDB := stdlib.OpenDB(*pgxCfg)
	db := sqlx.NewDb(sqlDB, "pgx")

	if cfg.MaxOpen > 0 {
		db.SetMaxOpenConns(cfg.MaxOpen)
	}
	if cfg.MaxIdle > 0 {
		db.SetMaxIdleConns(cfg.MaxIdle)
	}
	if cfg.MaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.MaxLifetime)
	}

	logger.WithFields("default_schema", defaultSchema).Info("shared pool: opened (tenancy enforced via WHERE tenant_id, not search_path)")

	return &Conn{
		Type: TypePostgres,
		RW:   db,
		CloseF: func(_ context.Context) error {
			return db.Close()
		},
	}, nil
}

// pgSanitizeIdentifier kept for any external callers that still use it
// for safe identifier emission (e.g. EnsureOnSchema). Ensures a name
// contains only [A-Za-z0-9_] to prevent SQL injection when interpolated
// into DDL.
func pgSanitizeIdentifier(id string) string {
	out := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "public"
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// ClickHouse: shared single-database mode with tenant_id column filtering.
// No per-tenant USE {database} switching needed.
// ---------------------------------------------------------------------------
