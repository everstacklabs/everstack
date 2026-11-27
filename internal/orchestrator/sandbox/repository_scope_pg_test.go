package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
	runtimesandbox "github.com/everstacklabs/everstack/internal/sandbox"
)

// sandbox_instances as on the production gateway DB (pg_dump --schema-only,
// 2026-10-06), minus the notify trigger and the snapshot foreign key, plus
// the sandbox_workspace_incarnation migration.
const sandboxInstancesDDL = `
CREATE TABLE sandbox_instances (
    id character varying(255) NOT NULL PRIMARY KEY,
    session_id character varying(255) NOT NULL,
    tenant_id character varying(255) NOT NULL,
    backend character varying(50) NOT NULL,
    container_id character varying(255),
    image character varying(255) NOT NULL,
    status character varying(50) DEFAULT 'pending' NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    instance_id character varying(255),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone,
    destroyed_at timestamp with time zone,
    error text,
    last_used_at timestamp with time zone,
    idle_retention_secs integer,
    destroy_reason character varying(50) DEFAULT 'manual',
    name text DEFAULT '' NOT NULL,
    git_repo_url character varying(500),
    git_branch character varying(255),
    git_commit_sha character varying(64),
    git_installation_id bigint,
    git_cloned_at timestamp with time zone,
    lifecycle_state character varying(50) DEFAULT 'running' NOT NULL,
    workspace_snapshot_ref text,
    revivable_until timestamp with time zone,
    stopped_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    keep_warm boolean DEFAULT false,
    agent_id character varying(255),
    persistent boolean DEFAULT false NOT NULL,
    desired_state text DEFAULT 'running' NOT NULL,
    reconcile_after timestamp with time zone DEFAULT now() NOT NULL,
    reconcile_attempts integer DEFAULT 0 NOT NULL,
    reconcile_locked_by text,
    reconcile_locked_at timestamp with time zone,
    agent_target text,
    short_code character varying(16),
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    auto_archive_after_days integer DEFAULT 7 NOT NULL,
    auto_delete_after_days integer DEFAULT '-1'::integer NOT NULL,
    archived_at timestamp with time zone,
    snapshot_id character varying(255),
    error_reason text,
    error_at timestamp with time zone,
    auto_stop_minutes integer,
    auto_archive_minutes integer,
    auto_delete_minutes integer,
    workspace_archive_ref text,
    recovery_attempts integer DEFAULT 0 NOT NULL,
    billing_started_at timestamp with time zone,
    billing_ended_at timestamp with time zone,
    workspace_incarnation text DEFAULT gen_random_uuid()::text NOT NULL,
    workspace_run_id text DEFAULT gen_random_uuid()::text NOT NULL
);
CREATE TABLE sandbox_workspace_purges (
    sandbox_id text NOT NULL,
    incarnation text NOT NULL,
    tenant_id text NOT NULL,
    enqueued_at timestamp with time zone DEFAULT now() NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text,
    PRIMARY KEY (sandbox_id, incarnation)
);
CREATE UNIQUE INDEX sandbox_instances_short_code_unique_idx ON sandbox_instances (short_code) WHERE short_code IS NOT NULL;
`

const (
	sbxOrgA  = "aaaaaaaa-0000-4000-8000-00000000000a"
	sbxInstA = "aaaaaaaa-0000-4000-8000-0000000000a1"
	sbxOrgB  = "bbbbbbbb-0000-4000-8000-00000000000b"
)

var sbxScopeA = runtimesandbox.TenantScope{Key: "org:" + sbxOrgA, Aliases: []string{sbxOrgA, sbxInstA}}

func seedSandbox(t *testing.T, repo *Repository, id, tenant, state string) {
	t.Helper()
	if _, err := repo.db.Exec(`INSERT INTO sandbox_instances (id, session_id, tenant_id, backend, image, lifecycle_state)
		VALUES ($1, $1, $2, 'firecracker', 'img', $3)`, id, tenant, state); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}
}

func pendingRow(id, tenant string) Row {
	return Row{ID: id, TenantID: tenant, SessionID: id, Status: StatePending, LifecycleState: StatePending,
		DesiredState: DesireRunning, Backend: "firecracker", Image: "img", Config: []byte("{}")}
}

// CONCURRENT_SANDBOXES counts the organization, not one of its ids: running
// sandboxes under the organization id and under its instance fill one cap
// together, while another organization's sandboxes never count.
func TestPGInsertPendingWithLimitCountsTheWholeOrganization(t *testing.T) {
	db := pgscratch.New(t, "sandbox_scope")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	ctx := context.Background()

	seedSandbox(t, repo, "sbx_a1", sbxOrgA, "running")
	seedSandbox(t, repo, "sbx_a2", sbxInstA, "running")
	seedSandbox(t, repo, "sbx_a_sleep", sbxInstA, "sleeping") // holds no slot
	for _, id := range []string{"sbx_b1", "sbx_b2", "sbx_b3", "sbx_b4"} {
		seedSandbox(t, repo, id, sbxOrgB, "running")
	}

	// Cap 3: A uses 2 across two ids, so exactly one more fits.
	if _, err := repo.InsertPendingWithLimit(ctx, pendingRow("sbx_a3", sbxInstA), 3, sbxScopeA); err != nil {
		t.Fatalf("A at 2/3 must fit one more: %v", err)
	}
	_, err := repo.InsertPendingWithLimit(ctx, pendingRow("sbx_a4", sbxOrgA), 3, sbxScopeA)
	if !errors.Is(err, runtimesandbox.ErrConcurrentSandboxLimit) {
		t.Fatalf("A at 3/3 via its own id: err = %v, want ErrConcurrentSandboxLimit", err)
	}

	// B's four running sandboxes are B's alone: with cap 5 B fits one more,
	// and A's three never counted toward it.
	scopeB := runtimesandbox.SingleTenantScope(sbxOrgB)
	if _, err := repo.InsertPendingWithLimit(ctx, pendingRow("sbx_b5", sbxOrgB), 5, scopeB); err != nil {
		t.Fatalf("B at 4/5 must fit one more: %v", err)
	}

	// Reviving A's sleeping sandbox needs a new slot and A is full.
	err = repo.SetDesiredStateWithLimit(ctx, "sbx_a_sleep", DesireRunning, 3, sbxScopeA)
	if !errors.Is(err, runtimesandbox.ErrConcurrentSandboxLimit) {
		t.Fatalf("revive at 3/3: err = %v, want ErrConcurrentSandboxLimit", err)
	}
	// A zero scope (callers without a resolver) counts the sandbox's own
	// tenant only, as before: sbx_a_sleep's tenant (the instance) has 2.
	if err := repo.SetDesiredStateWithLimit(ctx, "sbx_a_sleep", DesireRunning, 3, runtimesandbox.TenantScope{}); err != nil {
		t.Fatalf("tenant-only revive at 2/3: %v", err)
	}
}

// Two ids of one organization racing for its last slot share one advisory
// lock across replicas: exactly one wins.
func TestPGLastSandboxSlotIsSerializedAcrossOrganizationIDs(t *testing.T) {
	db := pgscratch.New(t, "sandbox_race")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	seedSandbox(t, repo, "sbx_a1", sbxOrgA, "running")

	var (
		wg              sync.WaitGroup
		mu              sync.Mutex
		granted, denied int
	)
	start := make(chan struct{})
	for i, tenant := range []string{sbxOrgA, sbxInstA, sbxOrgA, sbxInstA} {
		wg.Add(1)
		go func(i int, tenant string) {
			defer wg.Done()
			<-start
			row := pendingRow("sbx_race_"+string(rune('a'+i)), tenant)
			row.ShortCode = sql.NullString{String: "race" + string(rune('a'+i)), Valid: true}
			_, err := repo.InsertPendingWithLimit(context.Background(), row, 2, sbxScopeA)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				granted++
			case errors.Is(err, runtimesandbox.ErrConcurrentSandboxLimit):
				denied++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i, tenant)
	}
	close(start)
	wg.Wait()
	if granted != 1 || denied != 3 {
		t.Fatalf("granted=%d denied=%d, want exactly one through the last slot", granted, denied)
	}
}

// A shadow-only scope locks and counts the sandbox's own tenant, as the
// pre-existing gate did.
func TestPGShadowScopeCountsTheTenantAlone(t *testing.T) {
	db := pgscratch.New(t, "sandbox_shadow")
	pgscratch.MustExec(t, db, sandboxInstancesDDL)
	repo := NewRepository(db)
	seedSandbox(t, repo, "sbx_a1", sbxOrgA, "running")
	seedSandbox(t, repo, "sbx_a2", sbxOrgA, "running")
	shadow := sbxScopeA
	shadow.ShadowOnly = true
	if _, err := repo.InsertPendingWithLimit(context.Background(), pendingRow("sbx_i1", sbxInstA), 2, shadow); err != nil {
		t.Fatalf("shadow: the instance has no sandboxes of its own and must fit: %v", err)
	}
}
