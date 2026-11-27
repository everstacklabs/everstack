package sandbox

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Same production schema the lifecycle package's PG tests use.
const workspacePGDDL = `
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

type incarnationRow struct {
	Incarnation string         `db:"workspace_incarnation"`
	RunID       string         `db:"workspace_run_id"`
	ArchiveRef  sql.NullString `db:"workspace_archive_ref"`
}

func loadIncarnation(t *testing.T, m *SandboxManager, id string) incarnationRow {
	t.Helper()
	var r incarnationRow
	if err := m.db.Get(&r, `SELECT workspace_incarnation, workspace_run_id, workspace_archive_ref FROM sandbox_instances WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	return r
}

// Persistent agents reuse wks_<agent_id>. Recreating one over a terminated
// row is a new sandbox: new incarnation and run, no inherited workspace
// ref, and the old incarnation queued for deletion. Updating a live row
// changes none of that.
func TestPGReusedSandboxIDStartsANewIncarnation(t *testing.T) {
	db := pgscratch.New(t, "sandbox_rebirth")
	pgscratch.MustExec(t, db, workspacePGDDL)
	m := &SandboxManager{db: db}
	inst := &Instance{
		ID:        "wks_agent",
		Status:    StatusRunning,
		CreatedAt: time.Now(),
		Config:    InstanceConfig{SessionID: "s1", TenantID: "tenant-a", Image: "img"},
		Backend:   "firecracker",
	}
	m.persistInstance(inst)
	first := loadIncarnation(t, m, "wks_agent")

	// Live update: identity unchanged.
	m.persistInstance(inst)
	if got := loadIncarnation(t, m, "wks_agent"); got.Incarnation != first.Incarnation || got.RunID != first.RunID {
		t.Fatalf("updating a live row changed its identity: %+v -> %+v", first, got)
	}

	// The sandbox is terminated, leaving a workspace ref behind.
	if _, err := db.Exec(`UPDATE sandbox_instances SET lifecycle_state = 'terminated', status = 'terminated',
		destroyed_at = NOW(), workspace_archive_ref = 'objstore:v1:old#sha256=a&size=1' WHERE id = 'wks_agent'`); err != nil {
		t.Fatal(err)
	}

	// Recreated with the same id.
	m.persistInstance(inst)
	reborn := loadIncarnation(t, m, "wks_agent")
	if reborn.Incarnation == first.Incarnation || reborn.RunID == first.RunID {
		t.Fatalf("reused id kept the old identity: %+v", reborn)
	}
	if reborn.ArchiveRef.Valid {
		t.Fatalf("reused id inherited the old workspace ref %q", reborn.ArchiveRef.String)
	}
	var queued string
	if err := db.QueryRow(`SELECT incarnation FROM sandbox_workspace_purges WHERE sandbox_id = 'wks_agent'`).Scan(&queued); err != nil {
		t.Fatalf("old incarnation not queued for purge: %v", err)
	}
	if queued != first.Incarnation {
		t.Fatalf("queued incarnation %q, want the old %q", queued, first.Incarnation)
	}
}

func seedStuck(t *testing.T, m *SandboxManager, id, state string, idleFor time.Duration, lockedFor *time.Duration) {
	t.Helper()
	var locked any
	if lockedFor != nil {
		locked = time.Now().Add(-*lockedFor)
	}
	if _, err := m.db.Exec(`INSERT INTO sandbox_instances (id, session_id, tenant_id, backend, image, lifecycle_state, status,
		updated_at, reconcile_locked_by, reconcile_locked_at, workspace_archive_ref)
		VALUES ($1, $1, 'tenant-a', 'firecracker', 'img', $2, $2, $3, CASE WHEN $4::timestamptz IS NULL THEN NULL ELSE 'leader-1' END, $4,
		        'objstore:v1:k#sha256=a&size=1')`,
		id, state, time.Now().Add(-idleFor), locked); err != nil {
		t.Fatal(err)
	}
}

func stateOf(t *testing.T, m *SandboxManager, id string) string {
	t.Helper()
	var s string
	if err := m.db.Get(&s, `SELECT lifecycle_state FROM sandbox_instances WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	return s
}

// A row the reconciler has (re)claimed is being worked on, even when its
// updated_at is old (claiming only stamps the lease). The reaper must not
// treat it as stuck.
func TestPGReclaimedRowIsNotReaped(t *testing.T) {
	db := pgscratch.New(t, "reaper_lease")
	pgscratch.MustExec(t, db, workspacePGDDL)
	m := &SandboxManager{db: db}

	fresh := time.Minute
	stale := time.Hour
	seedStuck(t, m, "sbx_reclaimed", LifecycleReviving, time.Hour, &fresh)
	seedStuck(t, m, "sbx_stuck", LifecycleReviving, time.Hour, &stale)
	seedStuck(t, m, "sbx_never_leased", LifecycleStopping, time.Hour, nil)
	seedStuck(t, m, "sbx_recent", LifecycleStopping, time.Minute, nil)

	got, err := m.listStuckTransitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	if ids["sbx_reclaimed"] || ids["sbx_recent"] || !ids["sbx_stuck"] || !ids["sbx_never_leased"] || len(ids) != 2 {
		t.Fatalf("stuck = %v, want only sbx_stuck and sbx_never_leased", ids)
	}
}

// The wake completes (or the row is reclaimed) between the reaper's scan
// and its update. The guarded update must miss, and the VM must survive.
func TestPGWakeCompletingAfterScanKeepsItsVM(t *testing.T) {
	for _, tc := range []struct {
		name  string
		race  string
		state string
	}{
		{name: "wake completed", race: `UPDATE sandbox_instances SET lifecycle_state = 'running', status = 'running', updated_at = NOW() WHERE id = 'sbx_w'`, state: LifecycleRunning},
		{name: "row reclaimed by the reconciler", race: `UPDATE sandbox_instances SET reconcile_locked_by = 'leader-2', reconcile_locked_at = NOW() WHERE id = 'sbx_w'`, state: LifecycleReviving},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := pgscratch.New(t, "reaper_race")
			pgscratch.MustExec(t, db, workspacePGDDL)
			log := &callLog{}
			backend := &workspaceFakeBackend{log: log}
			m := newWorkspaceTestManager(t, backend)
			m.db = db
			setTestWorkspaceStore(m, newFakeWorkspaceStore(log))

			seedStuck(t, m, "sbx_w", LifecycleReviving, time.Hour, nil)
			cands, err := m.listStuckTransitions(context.Background())
			if err != nil || len(cands) != 1 {
				t.Fatalf("scan: %v %v", cands, err)
			}
			if _, err := db.Exec(tc.race); err != nil {
				t.Fatal(err)
			}

			if !m.holdForAttentionIfWorkspaceStored(context.Background(), cands[0]) {
				t.Fatal("candidate with a stored workspace was not held")
			}
			if backend.destroyed != 0 {
				t.Fatal("reaper destroyed the VM of a wake that was no longer stuck")
			}
			if got := stateOf(t, m, "sbx_w"); got != tc.state {
				t.Fatalf("state = %q, want %q (row untouched)", got, tc.state)
			}
		})
	}
}

// Without a race the stalled wake is parked first, and only then is its
// half-created VM destroyed.
func TestPGStalledWakeParksThenDestroys(t *testing.T) {
	db := pgscratch.New(t, "reaper_stalled_wake")
	pgscratch.MustExec(t, db, workspacePGDDL)
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log}
	m := newWorkspaceTestManager(t, backend)
	m.db = db
	setTestWorkspaceStore(m, newFakeWorkspaceStore(log))

	seedStuck(t, m, "sbx_w", LifecycleReviving, time.Hour, nil)
	cands, err := m.listStuckTransitions(context.Background())
	if err != nil || len(cands) != 1 {
		t.Fatalf("scan: %v %v", cands, err)
	}
	if !m.holdForAttentionIfWorkspaceStored(context.Background(), cands[0]) {
		t.Fatal("not held")
	}
	if backend.destroyed != 1 {
		t.Fatalf("destroyed = %d, want the leaked VM destroyed", backend.destroyed)
	}
	if got := stateOf(t, m, "sbx_w"); got != "error" {
		t.Fatalf("state = %q, want error", got)
	}
}
