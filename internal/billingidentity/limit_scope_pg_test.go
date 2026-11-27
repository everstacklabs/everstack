package billingidentity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Platform tables typed as on the production platform DB
// (information_schema, 2026-10-06).
const limitScopePlatformSchema = `
CREATE SCHEMA everstack;
CREATE TABLE everstack.organizations (id UUID PRIMARY KEY, plan_tier VARCHAR(50) NOT NULL DEFAULT 'free');
CREATE TABLE everstack.managed_instances (id UUID PRIMARY KEY, organization_id UUID NOT NULL, deleted_at TIMESTAMPTZ);
CREATE TABLE everstack.tenant_config (instance_id UUID PRIMARY KEY, organization_id UUID NOT NULL);
`

const (
	scopeOrgA        = "aaaaaaaa-0000-4000-8000-00000000000a"
	scopeInstA       = "aaaaaaaa-0000-4000-8000-0000000000a1" // live managed instance of A
	scopeDeletedA    = "aaaaaaaa-0000-4000-8000-0000000000a2" // deleted managed instance of A
	scopeOrgB        = "bbbbbbbb-0000-4000-8000-00000000000b"
	scopeTenantB     = "bbbbbbbb-0000-4000-8000-0000000000b1" // tenant_config alias of B
	scopeContested   = "eeeeeeee-0000-4000-8000-00000000000e" // claimed by A (tenant_config) and B (managed)
	scopeOrphan      = "dddddddd-0000-4000-8000-00000000000d"
	scopeHalfClaimed = "ffffffff-0000-4000-8000-00000000000f" // live for A, deleted claim by B
)

func seedLimitScope(t *testing.T) *sqlx.DB {
	t.Helper()
	db := pgscratch.New(t, "limit_scope")
	pgscratch.MustExec(t, db, limitScopePlatformSchema,
		`INSERT INTO everstack.organizations (id, plan_tier) VALUES
			('`+scopeOrgA+`', ' Basic '), ('`+scopeOrgB+`', 'free')`,
		`INSERT INTO everstack.managed_instances (id, organization_id, deleted_at) VALUES
			('`+scopeInstA+`', '`+scopeOrgA+`', NULL),
			('`+scopeDeletedA+`', '`+scopeOrgA+`', NOW()),
			('`+scopeContested+`', '`+scopeOrgB+`', NULL),
			('`+scopeHalfClaimed+`', '`+scopeOrgB+`', NOW())`,
		`INSERT INTO everstack.tenant_config (instance_id, organization_id) VALUES
			('`+scopeTenantB+`', '`+scopeOrgB+`'),
			('`+scopeContested+`', '`+scopeOrgA+`'),
			('`+scopeHalfClaimed+`', '`+scopeOrgA+`')`,
	)
	return db
}

func TestPGResolveLimitScopeCoversEveryAliasOfTheOrganization(t *testing.T) {
	db := seedLimitScope(t)
	ctx := context.Background()

	for _, tenant := range []string{scopeOrgA, scopeInstA, " " + scopeInstA + " "} {
		scope, err := ResolveLimitScope(ctx, db, tenant)
		if err != nil {
			t.Fatalf("ResolveLimitScope(%q): %v", tenant, err)
		}
		if scope.ID != scopeOrgA || scope.Tier != "basic" {
			t.Fatalf("ResolveLimitScope(%q) = org %q tier %q, want %q basic", tenant, scope.ID, scope.Tier, scopeOrgA)
		}
		// A deleted instance's objects are unreachable to the customer, so
		// they no longer count (deleting an instance frees the cap). The
		// contested and half-claimed ids are another organization's too, so
		// counting them would leak B's objects into A's cap.
		want := []string{scopeOrgA, scopeInstA}
		if !sameSet(scope.Aliases, want) {
			t.Fatalf("aliases for %q = %v, want %v", tenant, scope.Aliases, want)
		}
	}

	scope, err := ResolveLimitScope(ctx, db, scopeTenantB)
	if err != nil {
		t.Fatalf("ResolveLimitScope(B tenant): %v", err)
	}
	if scope.ID != scopeOrgB || !sameSet(scope.Aliases, []string{scopeOrgB, scopeTenantB}) {
		t.Fatalf("B scope = %+v", scope)
	}
}

func TestPGResolveLimitScopeRefusesAmbiguousOrUnknownTenants(t *testing.T) {
	db := seedLimitScope(t)
	ctx := context.Background()

	if _, err := ResolveLimitScope(ctx, db, scopeContested); !errors.Is(err, ErrContestedTenant) {
		t.Fatalf("contested tenant err = %v, want ErrContestedTenant", err)
	}
	// Live for A only, but B still claims it through a deleted instance: its
	// rows cannot be attributed to A alone, so it resolves to nobody.
	// Its managed-instance row (B's) is deleted, so it is not a live
	// identity at all; either refusal means it is attributed to nobody.
	if _, err := ResolveLimitScope(ctx, db, scopeHalfClaimed); !errors.Is(err, ErrContestedTenant) && !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("half-claimed tenant err = %v, want no organization scope", err)
	}
	if _, err := ResolveLimitScope(ctx, db, scopeOrphan); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("orphan tenant err = %v, want ErrNoOrganization", err)
	}
	// A deleted instance must not regain an allocation scope.
	if _, err := ResolveLimitScope(ctx, db, scopeDeletedA); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("deleted instance err = %v, want ErrNoOrganization", err)
	}
	if _, err := ResolveLimitScope(ctx, db, ""); err == nil {
		t.Fatal("empty tenant must not resolve")
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			return false
		}
		seen[w]--
	}
	return true
}

// The exemption comes only from organizations.plan_limit_exempt, and only for
// the organization that owns the tenant. A gateway deployed before the
// services migration (no column yet) reads FALSE rather than failing.
func TestPGResolveLimitScopeReadsTheExemptFlag(t *testing.T) {
	db := seedLimitScope(t)
	ctx := context.Background()

	scope, err := ResolveLimitScope(ctx, db, scopeInstA)
	if err != nil || scope.Exempt {
		t.Fatalf("before the column exists: scope = %+v, %v; want not exempt, no error", scope, err)
	}

	migration, err := os.ReadFile(filepath.Join("..", "..", "services", "cloud", "internal", "database",
		"migrations", "postgres", "organizations_plan_limit_exempt_20261006085237", "up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	pgscratch.MustExec(t, db, string(migration),
		`UPDATE everstack.organizations SET plan_limit_exempt = TRUE WHERE id = '`+scopeOrgA+`'`)

	for _, tenant := range []string{scopeOrgA, scopeInstA} {
		scope, err := ResolveLimitScope(ctx, db, tenant)
		if err != nil || !scope.Exempt {
			t.Fatalf("A via %s: scope = %+v, %v; want exempt", tenant, scope, err)
		}
	}
	if scope, err := ResolveLimitScope(ctx, db, scopeTenantB); err != nil || scope.Exempt {
		t.Fatalf("B: scope = %+v, %v; another organization's flag must not apply", scope, err)
	}
}

// An instance deleted from managed_instances whose tenant_config row was left
// behind must not resolve as live, and its id is not counted for the org.
func TestPGDeletedInstanceWithLeftoverTenantConfigDoesNotResolve(t *testing.T) {
	db := seedLimitScope(t)
	ctx := context.Background()
	pgscratch.MustExec(t, db, `INSERT INTO everstack.tenant_config (instance_id, organization_id)
		VALUES ('`+scopeDeletedA+`', '`+scopeOrgA+`')`)

	if _, err := ResolveLimitScope(ctx, db, scopeDeletedA); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("deleted instance with a tenant_config row: err = %v, want ErrNoOrganization", err)
	}
	scope, err := ResolveLimitScope(ctx, db, scopeOrgA)
	if err != nil || !sameSet(scope.Aliases, []string{scopeOrgA, scopeInstA}) {
		t.Fatalf("A scope = %+v, %v; the deleted instance must not be an alias", scope, err)
	}
}

// Hosted sites of a deleted instance keep counting until they are taken down
// (a failed takedown must not free the quota), so the scope also lists the
// organization's deleted instances, separately from its live aliases and
// never including another organization's ids.
func TestPGResolveLimitScopeListsDeletedInstances(t *testing.T) {
	db := seedLimitScope(t)
	scope, err := ResolveLimitScope(context.Background(), db, scopeInstA)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(scope.DeletedAliases, []string{scopeDeletedA}) {
		t.Fatalf("deleted aliases = %v, want [%s]", scope.DeletedAliases, scopeDeletedA)
	}
	if b, err := ResolveLimitScope(context.Background(), db, scopeTenantB); err != nil || len(b.DeletedAliases) != 0 {
		t.Fatalf("B deleted aliases = %v, %v; want none (the half-claimed id is contested)", b.DeletedAliases, err)
	}
}
