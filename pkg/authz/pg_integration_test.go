package authz

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// These tests run only when EVS_TEST_PG_DSN points at a real Postgres, e.g.:
//
//	EVS_TEST_PG_DSN='postgres://postgres:verify@localhost:55432/everstack?sslmode=disable' \
//	  go test ./pkg/authz/ -run PG -v
//
// They validate the relation_tuples DDL + PostgresStore + the engine against a
// real database, and that the RLS arm step actually isolates tenants (per the
// "verify RLS against real Postgres, never by reasoning" rule).

func pgConn(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EVS_TEST_PG_DSN not set; skipping real-Postgres integration test")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

const relationTuplesDDL = `
CREATE TABLE IF NOT EXISTS relation_tuples (
    tenant_id        TEXT NOT NULL DEFAULT '',
    object_type      TEXT NOT NULL,
    object_id        TEXT NOT NULL,
    relation         TEXT NOT NULL,
    subject_type     TEXT NOT NULL,
    subject_id       TEXT NOT NULL,
    subject_relation TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, object_type, object_id, relation, subject_type, subject_id, subject_relation)
);`

func TestPGStoreAndEngine(t *testing.T) {
	db := pgConn(t)
	// All of this fixture's tuples belong to org "acme"; scope the store ops to
	// that tenant (the Postgres store fails closed without one).
	ctx := ContextWithTenant(context.Background(), "acme")
	if _, err := db.ExecContext(ctx, `SET search_path TO everstack, public`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, relationTuplesDDL); err != nil {
		t.Fatalf("apply relation_tuples DDL: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE relation_tuples`); err != nil {
		t.Fatal(err)
	}

	store := NewPostgresStore(db, "relation_tuples")
	eng := NewEngine(store, EverstackSchema().WithResourceTypes("dataset"))

	if err := store.Write(ctx,
		OrgMembership("acme", "alice", RoleOwner),
		OrgMembership("acme", "carol", RoleViewer),
		WorkspaceParent("prod", "acme"),
		InstanceParent("inst1", "prod"),
		ResourceParent("dataset", "ds1", "inst1"),
	); err != nil {
		t.Fatalf("write tuples: %v", err)
	}

	check := func(user, rel string, obj Object, want bool) {
		t.Helper()
		got, err := eng.Check(ctx, user, rel, obj)
		if err != nil {
			t.Fatalf("Check(%s,%s,%s): %v", user, rel, obj, err)
		}
		if got != want {
			t.Errorf("Check(%s,%s,%s)=%v want %v", user, rel, obj, got, want)
		}
	}
	// Inheritance through the PG-backed store, end to end.
	check("alice", "can_manage_billing", Org("acme"), true)
	check("alice", "can_edit", Resource("dataset", "ds1"), true) // owner -> ... -> dataset
	check("carol", "can_view", Resource("dataset", "ds1"), true)
	check("carol", "can_edit", Resource("dataset", "ds1"), false)
	check("mallory", "can_view", Resource("dataset", "ds1"), false)

	// Delete removes a grant.
	if err := store.Delete(ctx, OrgMembership("acme", "carol", RoleViewer)); err != nil {
		t.Fatal(err)
	}
	check("carol", "can_view", Resource("dataset", "ds1"), false)

	// Tenant isolation at the SQL layer: the same object ids under a different
	// tenant resolve to nothing, so the owner check denies.
	otherCtx := ContextWithTenant(context.Background(), "other-tenant")
	if got, err := eng.Check(otherCtx, "alice", "can_manage_billing", Org("acme")); err != nil || got {
		t.Errorf("another tenant must not see acme's tuples (got=%v err=%v)", got, err)
	}
}

// TestPGRelationTuplesRLS validates the full RLS arming of relation_tuples: the
// PostgresStore (which sets app.current_tenant per transaction) run as a
// NON-superuser role against an RLS-armed table actually isolates tenants. This
// exercises the real store + the migration's policy together, per the standing
// "verify RLS against real Postgres, never by reasoning" rule.
func TestPGRelationTuplesRLS(t *testing.T) {
	db := pgConn(t)
	ctx := context.Background()

	setup := []string{
		`CREATE SCHEMA IF NOT EXISTS everstack`,
		`CREATE OR REPLACE FUNCTION everstack.tenant_matches(row_tenant_id text)
		   RETURNS boolean LANGUAGE sql STABLE AS $$
		     SELECT current_setting('app.bypass_rls', true) = 'on'
		       OR (current_setting('app.current_tenant', true) IS NOT NULL
		           AND current_setting('app.current_tenant', true) <> ''
		           AND row_tenant_id = current_setting('app.current_tenant', true));
		   $$`,
		`DROP TABLE IF EXISTS everstack.relation_tuples`,
		`CREATE TABLE everstack.relation_tuples (
		    tenant_id        TEXT NOT NULL DEFAULT '',
		    object_type      TEXT NOT NULL, object_id TEXT NOT NULL, relation TEXT NOT NULL,
		    subject_type     TEXT NOT NULL, subject_id TEXT NOT NULL, subject_relation TEXT NOT NULL DEFAULT '',
		    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    PRIMARY KEY (tenant_id, object_type, object_id, relation, subject_type, subject_id, subject_relation)
		)`,
		// Policy (mirrors the authz_relation_tuples_rls_policy migration) + arm.
		`DROP POLICY IF EXISTS tenant_isolation ON everstack.relation_tuples`,
		`CREATE POLICY tenant_isolation ON everstack.relation_tuples FOR ALL
		   USING (everstack.tenant_matches(tenant_id)) WITH CHECK (everstack.tenant_matches(tenant_id))`,
		`ALTER TABLE everstack.relation_tuples ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE everstack.relation_tuples FORCE ROW LEVEL SECURITY`,
		// Non-superuser role the store will connect as (superusers bypass RLS).
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='authz_rls_test')
		   THEN CREATE ROLE authz_rls_test LOGIN PASSWORD 'rlspw'; END IF; END $$`,
		`GRANT USAGE ON SCHEMA everstack TO authz_rls_test`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON everstack.relation_tuples TO authz_rls_test`,
		`GRANT EXECUTE ON FUNCTION everstack.tenant_matches(text) TO authz_rls_test`,
		`ALTER ROLE authz_rls_test SET search_path TO everstack, public`,
	}
	for _, s := range setup {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// Connect as the non-superuser role so RLS is actually enforced.
	u, err := url.Parse(os.Getenv("EVS_TEST_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("authz_rls_test", "rlspw")
	roleDB, err := sqlx.Connect("pgx", u.String())
	if err != nil {
		t.Fatalf("connect as authz_rls_test: %v", err)
	}
	t.Cleanup(func() { _ = roleDB.Close() })

	store := NewPostgresStore(roleDB, "relation_tuples")
	eng := NewEngine(store, EverstackSchema())

	// Seed two tenants' graphs THROUGH the store (it sets the GUC; RLS WITH CHECK
	// must accept each write).
	ctxA := ContextWithTenant(context.Background(), "tenantA")
	ctxB := ContextWithTenant(context.Background(), "tenantB")
	if err := store.Write(ctxA, OrgMembership("acme", "alice", RoleOwner)); err != nil {
		t.Fatalf("write under tenant A: %v", err)
	}
	if err := store.Write(ctxB, OrgMembership("acme", "bob", RoleOwner)); err != nil {
		t.Fatalf("write under tenant B: %v", err)
	}

	// Engine checks via the store are isolated by RLS + the GUC.
	if ok, err := eng.Check(ctxA, "alice", "owner", Org("acme")); err != nil || !ok {
		t.Fatalf("tenant A should see alice owner (ok=%v err=%v)", ok, err)
	}
	if ok, err := eng.Check(ctxB, "alice", "owner", Org("acme")); err != nil || ok {
		t.Fatalf("RLS must hide tenant A's alice from tenant B (ok=%v err=%v)", ok, err)
	}
	if ok, err := eng.Check(ctxB, "bob", "owner", Org("acme")); err != nil || !ok {
		t.Fatalf("tenant B should see bob owner (ok=%v err=%v)", ok, err)
	}

	// Prove RLS itself enforces (not just the store's WHERE filter): an UNFILTERED
	// count under a tenant's GUC sees only that tenant's rows.
	countUnfiltered := func(tenant string) int {
		tx, err := roleDB.BeginTxx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_tenant',$1,true)`, tenant); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.GetContext(ctx, &n, `SELECT count(*) FROM everstack.relation_tuples`); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := countUnfiltered("tenantA"); n != 1 {
		t.Errorf("RLS: unfiltered count under tenant A should be 1, got %d", n)
	}
	if n := countUnfiltered("ghost"); n != 0 {
		t.Errorf("RLS: unknown tenant should see 0 rows, got %d", n)
	}
}

// TestPGRowLevelSecurity validates that the RLS arm step (ENABLE + FORCE +
// tenant_matches policy) actually isolates tenants on a real Postgres, using a
// non-superuser role (superusers bypass RLS).
func TestPGRowLevelSecurity(t *testing.T) {
	db := pgConn(t)
	ctx := context.Background()

	setup := []string{
		`SET search_path TO everstack, public`,
		`CREATE OR REPLACE FUNCTION everstack.tenant_matches(row_tenant_id text)
		   RETURNS boolean LANGUAGE sql STABLE AS $$
		     SELECT current_setting('app.bypass_rls', true) = 'on'
		       OR (current_setting('app.current_tenant', true) IS NOT NULL
		           AND current_setting('app.current_tenant', true) <> ''
		           AND row_tenant_id = current_setting('app.current_tenant', true));
		   $$`,
		`DROP TABLE IF EXISTS everstack.rls_demo`,
		`CREATE TABLE everstack.rls_demo (id text primary key, tenant_id text not null)`,
		`DROP POLICY IF EXISTS tenant_isolation ON everstack.rls_demo`,
		`CREATE POLICY tenant_isolation ON everstack.rls_demo FOR ALL
		   USING (everstack.tenant_matches(tenant_id)) WITH CHECK (everstack.tenant_matches(tenant_id))`,
		// Arm (mirrors scripts/db/arm-rls.sql).
		`ALTER TABLE everstack.rls_demo ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE everstack.rls_demo FORCE ROW LEVEL SECURITY`,
		// Seed as superuser (RLS bypassed for the owner only after FORCE? FORCE
		// applies to owner too, so seed with bypass on).
		`SELECT set_config('app.bypass_rls','on',false)`,
		`INSERT INTO everstack.rls_demo VALUES ('a1','tenantA'),('b1','tenantB')`,
		`SELECT set_config('app.bypass_rls','off',false)`,
		// Non-superuser role so RLS is enforced.
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='authz_app') THEN CREATE ROLE authz_app NOLOGIN; END IF; END $$`,
		`GRANT USAGE ON SCHEMA everstack TO authz_app`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON everstack.rls_demo TO authz_app`,
		`GRANT EXECUTE ON FUNCTION everstack.tenant_matches(text) TO authz_app`,
	}
	for _, s := range setup {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	countAs := func(tenant string) int {
		t.Helper()
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `SET ROLE authz_app`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_tenant',$1,true)`, tenant); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.GetContext(ctx, &n, `SELECT count(*) FROM everstack.rls_demo`); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got := countAs("tenantA"); got != 1 {
		t.Errorf("tenantA should see exactly its 1 row, saw %d", got)
	}
	if got := countAs("tenantB"); got != 1 {
		t.Errorf("tenantB should see exactly its 1 row, saw %d", got)
	}
	if got := countAs("ghost"); got != 0 {
		t.Errorf("unknown tenant must see 0 rows, saw %d", got)
	}
}
