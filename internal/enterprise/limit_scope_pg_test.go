package enterprise

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Real-Postgres tenant-isolation tests for organization-scoped caps. They
// skip unless EVS_TEST_PG_DSN is set (see internal/lib/pgscratch).
//
// Platform and gateway tables live in one scratch database here; in
// production they are two databases, which the code reaches through two
// handles, so nothing below relies on a cross-database join.

const capPlatformSchema = `
CREATE SCHEMA everstack;
CREATE TABLE everstack.organizations (id UUID PRIMARY KEY, plan_tier VARCHAR(50) NOT NULL DEFAULT 'free');
CREATE TABLE everstack.managed_instances (id UUID PRIMARY KEY, organization_id UUID NOT NULL, deleted_at TIMESTAMPTZ);
CREATE TABLE everstack.tenant_config (instance_id UUID PRIMARY KEY, organization_id UUID NOT NULL);
`

// Gateway tables typed as on the production gateway DB (information_schema,
// 2026-10-06). annotation_queues and agent_channel_bindings deliberately have
// no deleted_at column: production has none.
const capGatewaySchema = `
CREATE TABLE agent_definitions (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL,
	lifecycle_mode VARCHAR(50) NOT NULL DEFAULT 'ephemeral', lifecycle_status VARCHAR(50) NOT NULL DEFAULT 'created',
	deleted_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE dataset_items (id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id VARCHAR(255) NOT NULL);
CREATE TABLE eval_runs (id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id VARCHAR(255) NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE annotation_queues (id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id VARCHAR(255) NOT NULL);
CREATE TABLE channel_configs (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL);
CREATE TABLE agent_channel_bindings (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL);
CREATE TABLE trooper_channel_bindings (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL);
`

const (
	capOrgA      = "aaaaaaaa-0000-4000-8000-00000000000a"
	capInstA     = "aaaaaaaa-0000-4000-8000-0000000000a1" // live managed instance of A
	capDeletedA  = "aaaaaaaa-0000-4000-8000-0000000000a2" // deleted managed instance of A
	capOrgB      = "bbbbbbbb-0000-4000-8000-00000000000b"
	capTenantB   = "bbbbbbbb-0000-4000-8000-0000000000b1"
	capContested = "eeeeeeee-0000-4000-8000-00000000000e" // claimed by A and B
	capOrphan    = "dddddddd-0000-4000-8000-00000000000d"
)

func capScratch(t *testing.T) *sqlx.DB {
	t.Helper()
	db := pgscratch.New(t, "org_caps")
	pgscratch.MustExec(t, db, capPlatformSchema, capGatewaySchema,
		`INSERT INTO everstack.organizations (id, plan_tier) VALUES
			('`+capOrgA+`', 'free'), ('`+capOrgB+`', 'free')`,
		`INSERT INTO everstack.managed_instances (id, organization_id, deleted_at) VALUES
			('`+capInstA+`', '`+capOrgA+`', NULL),
			('`+capDeletedA+`', '`+capOrgA+`', NOW()),
			('`+capContested+`', '`+capOrgB+`', NULL)`,
		`INSERT INTO everstack.tenant_config (instance_id, organization_id) VALUES
			('`+capTenantB+`', '`+capOrgB+`'),
			('`+capContested+`', '`+capOrgA+`')`,
	)
	return db
}

// withEnforcedOrgScopes installs the platform-DB resolver on a managed
// gateway with every tier enforced.
func withEnforcedOrgScopes(t *testing.T, db *sqlx.DB) {
	t.Helper()
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, NewDBPlanTierResolver(db, time.Minute))
	SetEnforcementPolicy(true, nil)
	// These tests pin counting, not class policy: apply activity caps too.
	SetActivityEnforcement(true)
	t.Cleanup(func() { SetShadowEnforcement(true); SetActivityEnforcement(false) })
}

// seed inserts n rows for tenant into the table behind one limit type.
func seed(t *testing.T, db *sqlx.DB, limit UsageType, tenant string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		var q string
		switch limit {
		case UsageTypeAgents:
			q = `INSERT INTO agent_definitions (tenant_id) VALUES ($1)`
		case UsageTypePersistentAgents:
			q = `INSERT INTO agent_definitions (tenant_id, lifecycle_mode, lifecycle_status) VALUES ($1, 'persistent', 'sleeping')`
		case UsageTypeConcurrentRunning:
			q = `INSERT INTO agent_definitions (tenant_id, lifecycle_mode, lifecycle_status) VALUES ($1, 'persistent', 'running')`
		case UsageTypeDatasetItems:
			q = `INSERT INTO dataset_items (tenant_id) VALUES ($1)`
		case UsageTypeEvalRunsMonthly:
			q = `INSERT INTO eval_runs (tenant_id) VALUES ($1)`
		case UsageTypeAnnotationQueues:
			q = `INSERT INTO annotation_queues (tenant_id) VALUES ($1)`
		case UsageTypeChannels:
			q = `INSERT INTO channel_configs (tenant_id) VALUES ($1)`
		case UsageTypeChannelBindings:
			q = `INSERT INTO agent_channel_bindings (tenant_id) VALUES ($1)`
		default:
			t.Fatalf("no seed for %s", limit)
		}
		if _, err := db.Exec(q, tenant); err != nil {
			t.Fatalf("seed %s: %v", limit, err)
		}
	}
}

func checkFor(limit UsageType, tenant string) LimitCheck {
	switch limit {
	case UsageTypeAgents:
		return AgentsLimit(tenant)
	case UsageTypePersistentAgents:
		return PersistentAgentsLimit(tenant)
	case UsageTypeConcurrentRunning:
		return ConcurrentRunningLimit(tenant, "00000000-0000-0000-0000-000000000000")
	case UsageTypeDatasetItems:
		return DatasetItemsLimit(tenant, 1)
	case UsageTypeEvalRunsMonthly:
		return EvalRunsMonthlyLimit(tenant, time.Now())
	case UsageTypeAnnotationQueues:
		return AnnotationQueuesLimit(tenant)
	case UsageTypeChannels:
		return ChannelsLimit(tenant)
	case UsageTypeChannelBindings:
		return ChannelBindingsLimit(tenant)
	}
	panic("unknown limit " + limit)
}

// Every capped resource counts over the whole organization: rows A keeps
// under its own id and its live instance add up, while rows under its deleted
// instance, B's rows, a contested tenant's rows and an orphan's rows never do.
func TestPGCapsCountEveryAliasOfTheOrganizationAndNothingElse(t *testing.T) {
	free, ok := planLimitsForTier("free")
	if !ok {
		t.Fatal("free plan missing from plans data")
	}

	for _, limit := range []UsageType{
		UsageTypeAgents, UsageTypePersistentAgents, UsageTypeConcurrentRunning, UsageTypeDatasetItems,
		UsageTypeEvalRunsMonthly, UsageTypeAnnotationQueues, UsageTypeChannels, UsageTypeChannelBindings,
	} {
		t.Run(string(limit), func(t *testing.T) {
			db := capScratch(t)
			withEnforcedOrgScopes(t, db)
			cap := int(free[limit])
			if cap < 1 {
				t.Fatalf("free %s cap = %d, want a positive cap to test", limit, cap)
			}

			// A is one short of its cap, spread over its two live ids. Rows
			// under its deleted instance are unreachable to the customer and
			// must not hold the cap.
			below := cap - 1
			seed(t, db, limit, capOrgA, below/2)
			seed(t, db, limit, capInstA, below-below/2)
			seed(t, db, limit, capDeletedA, cap+3)
			// Other organizations' and unattributable rows, far past A's cap.
			seed(t, db, limit, capTenantB, cap+3)
			seed(t, db, limit, capContested, cap+3)
			seed(t, db, limit, capOrphan, cap+3)

			fromInstance := verifiedTenantCtx(capInstA)
			if err := CheckLimit(fromInstance, db, nil, checkFor(limit, capInstA)); err != nil {
				t.Fatalf("A at %d/%d must still create one more: %v", below, cap, err)
			}

			// One more row, under A's own id, fills the cap for every alias.
			seed(t, db, limit, capOrgA, 1)
			for _, alias := range []string{capOrgA, capInstA} {
				err := CheckLimit(verifiedTenantCtx(alias), db, nil, checkFor(limit, alias))
				var le *LimitError
				if !errors.As(err, &le) {
					t.Fatalf("A via %s at cap: err = %v, want *LimitError", alias, err)
				}
				if le.Used != int64(cap) || le.Limit != int64(cap) {
					t.Fatalf("A via %s: used/limit = %d/%d, want %d/%d (B, contested and orphan rows must not count)",
						alias, le.Used, le.Limit, cap, cap)
				}
			}

			// B is far over its cap, which proves B's count is B's alone and
			// that grandfathered rows are only ever refused new creation.
			err := CheckLimit(verifiedTenantCtx(capTenantB), db, nil, checkFor(limit, capTenantB))
			var le *LimitError
			if !errors.As(err, &le) || le.Used != int64(cap+3) {
				t.Fatalf("B: err = %v, want a refusal counting exactly B's %d rows", err, cap+3)
			}

			// A contested tenant resolves no organization. It gets Starter
			// caps counted over its own rows only: never someone else's plan,
			// never counted into another organization, never unlimited.
			err = CheckLimit(verifiedTenantCtx(capContested), db, nil, checkFor(limit, capContested))
			if !errors.As(err, &le) || le.Used != int64(cap+3) {
				t.Fatalf("contested tenant: err = %v, want Starter refusal counting its own %d rows", err, cap+3)
			}
		})
	}
}

// The tier itself must resolve through an instance id: before this change the
// resolver only matched organizations.id, so every tenant running under an
// instance id resolved no plan and ran unlimited even with enforcement on.
func TestPGPlanTierResolvesThroughManagedInstanceIDs(t *testing.T) {
	db := capScratch(t)
	withEnforcedOrgScopes(t, db)
	pgscratch.MustExec(t, db, `UPDATE everstack.organizations SET plan_tier = ' Basic ' WHERE id = '`+capOrgA+`'`)

	ent := ResolveEntitlements(verifiedTenantCtx(capInstA), nil)
	if ent.Source != "managed-plan" || ent.Tier != "basic" {
		t.Fatalf("instance tenant resolved %q/%q, want managed-plan/basic", ent.Source, ent.Tier)
	}
	if _, capped := ent.Limit(UsageTypeAgents); !capped {
		t.Fatal("an enforced basic plan must cap AGENTS")
	}
	// A verified tenant with no live organization (a deleted instance whose
	// key still authenticates, an orphan) gets Starter caps, never unlimited.
	for _, tenant := range []string{capDeletedA, capOrphan} {
		ent := ResolveEntitlements(verifiedTenantCtx(tenant), nil)
		if ent.Source != "managed-unresolved" || ent.Tier != "free" {
			t.Fatalf("%s resolved %q/%q, want managed-unresolved/free", tenant, ent.Source, ent.Tier)
		}
		if limit, capped := ent.Limit(UsageTypeAgents); !capped || limit != 3 {
			t.Fatalf("%s AGENTS = %d %v, want the Starter cap 3", tenant, limit, capped)
		}
	}
}

// Two creators racing for an organization's last slot through two different
// aliases share one advisory lock: exactly one wins.
func TestPGLastSlotIsSerializedAcrossAliases(t *testing.T) {
	db := capScratch(t)
	withEnforcedOrgScopes(t, db)
	free, _ := planLimitsForTier("free")
	seed(t, db, UsageTypeAgents, capOrgA, int(free[UsageTypeAgents])-1)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
		refused int
	)
	start := make(chan struct{})
	for _, alias := range []string{capOrgA, capInstA, capOrgA, capInstA} {
		wg.Add(1)
		go func(alias string) {
			defer wg.Done()
			<-start
			ctx := verifiedTenantCtx(alias)
			slot, err := ReserveLimitSlot(ctx, db, nil, AgentsLimit(alias))
			if err != nil {
				if !errors.Is(err, ErrResourceLimit) {
					t.Errorf("alias %s: unexpected error %v", alias, err)
				}
				mu.Lock()
				refused++
				mu.Unlock()
				return
			}
			defer slot.Release()
			if _, err := db.Exec(`INSERT INTO agent_definitions (tenant_id) VALUES ($1)`, alias); err != nil {
				t.Errorf("insert: %v", err)
			}
			slot.Confirm(ctx)
			mu.Lock()
			granted++
			mu.Unlock()
		}(alias)
	}
	close(start)
	wg.Wait()
	if granted != 1 || refused != 3 {
		t.Fatalf("granted=%d refused=%d, want exactly one creator through the last slot", granted, refused)
	}
}

// A detached path (channel inbound, scheduler, auto-provision) carries the
// tenant the server resolved itself; it must get the organization's plan, not
// Community Edition limits.
func TestPGServerResolvedTenantGetsTheOrganizationPlan(t *testing.T) {
	db := capScratch(t)
	withEnforcedOrgScopes(t, db)

	ctx := WithServerResolvedTenant(context.Background(), capInstA)
	ent := ResolveEntitlements(ctx, nil)
	if ent.Source != "managed-plan" || ent.Tier != "free" {
		t.Fatalf("server-resolved tenant resolved %q/%q, want managed-plan/free", ent.Source, ent.Tier)
	}
}

// An internal organization at (or past) its cap still creates, through every
// alias, while a customer organization at its cap is refused. The flag is
// the organizations column alone.
func TestPGExemptOrganizationCreatesPastItsCap(t *testing.T) {
	db := capScratch(t)
	withEnforcedOrgScopes(t, db)
	pgscratch.MustExec(t, db,
		`ALTER TABLE everstack.organizations ADD COLUMN plan_limit_exempt BOOLEAN NOT NULL DEFAULT FALSE`,
		`UPDATE everstack.organizations SET plan_limit_exempt = TRUE WHERE id = '`+capOrgA+`'`)
	free, _ := planLimitsForTier("free")
	seed(t, db, UsageTypeAgents, capInstA, int(free[UsageTypeAgents])+4)
	seed(t, db, UsageTypeAgents, capTenantB, int(free[UsageTypeAgents]))

	for _, alias := range []string{capOrgA, capInstA} {
		if err := CheckLimit(verifiedTenantCtx(alias), db, nil, AgentsLimit(alias)); err != nil {
			t.Fatalf("exempt org via %s refused: %v", alias, err)
		}
	}
	if err := CheckLimit(verifiedTenantCtx(capTenantB), db, nil, AgentsLimit(capTenantB)); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("customer org at cap: err = %v, want a refusal", err)
	}
}

// Racing creators at cap minus one: with the per-organization advisory lock
// exactly one wins, for every capped resource. The unlocked control (the old
// count-then-insert) lets more than one through, which is what this test
// exists to catch.
func TestPGLastSlotIsSerializedForEveryCap(t *testing.T) {
	for _, limit := range []UsageType{
		UsageTypeAgents, UsageTypePersistentAgents, UsageTypeConcurrentRunning, UsageTypeDatasetItems,
		UsageTypeEvalRunsMonthly, UsageTypeAnnotationQueues, UsageTypeChannels, UsageTypeChannelBindings,
	} {
		t.Run(string(limit), func(t *testing.T) {
			if got := raceLastSlot(t, limit, true); got != 1 {
				t.Fatalf("locked: %d creators got through the last slot, want 1", got)
			}
			if got := raceLastSlot(t, limit, false); got < 2 {
				t.Fatalf("unlocked control: %d got through; the race harness no longer reproduces the race", got)
			}
		})
	}
}

// raceLastSlot starts four creators (two per alias of org A) at cap minus
// one and returns how many were admitted. locked uses ReserveLimitSlot;
// unlocked uses CheckLimit with a pause before the insert, as a handler that
// counts then writes would.
func raceLastSlot(t *testing.T, limit UsageType, locked bool) int {
	t.Helper()
	db := capScratch(t)
	withEnforcedOrgScopes(t, db)
	free, _ := planLimitsForTier("free")
	seed(t, db, limit, capOrgA, int(free[limit])-1)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
	)
	start := make(chan struct{})
	for _, alias := range []string{capOrgA, capInstA, capOrgA, capInstA} {
		wg.Add(1)
		go func(alias string) {
			defer wg.Done()
			<-start
			ctx := verifiedTenantCtx(alias)
			check := checkFor(limit, alias)
			if locked {
				slot, err := ReserveLimitSlot(ctx, db, nil, check)
				if err != nil {
					return
				}
				defer slot.Release()
				seed(t, db, limit, alias, 1)
				slot.Confirm(ctx)
			} else {
				if err := CheckLimit(ctx, db, nil, check); err != nil {
					return
				}
				time.Sleep(150 * time.Millisecond)
				seed(t, db, limit, alias, 1)
			}
			mu.Lock()
			granted++
			mu.Unlock()
		}(alias)
	}
	close(start)
	wg.Wait()
	return granted
}

// Fill instance X to the Starter cap, delete X: a new instance of the same
// organization counts only live objects (X's are excluded because X no
// longer runs; see the cmd/serve deleted-instance sweep test), while X
// itself, if anything still reaches it, resolves no organization and is
// held to Starter caps on its own rows, so it can never create again.
func TestPGDeletedInstanceFreesTheCapButCannotCreate(t *testing.T) {
	db := capScratch(t)
	withEnforcedOrgScopes(t, db)
	free, _ := planLimitsForTier("free")
	cap := int(free[UsageTypeAgents])
	seed(t, db, UsageTypeAgents, capDeletedA, cap) // X, now deleted, holds a full cap

	err := CheckLimit(verifiedTenantCtx(capInstA), db, nil, AgentsLimit(capInstA))
	if err != nil {
		t.Fatalf("Y (live) after X's deletion: %v, want room (X's agents no longer run or count)", err)
	}
	var le *LimitError
	if err := CheckLimit(verifiedTenantCtx(capDeletedA), db, nil, AgentsLimit(capDeletedA)); !errors.As(err, &le) || le.Used != int64(cap) {
		t.Fatalf("deleted X creating: err = %v, want a Starter refusal at %d", err, cap)
	}
}
