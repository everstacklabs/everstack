package sandbox

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
	"github.com/jmoiron/sqlx"
)

// TestGetBySandboxIDOrNameScoped_TenantIsolation proves the scoped in-memory
// lookup does not let a caller in one tenant resolve another tenant's sandbox
// by exact id or by a colliding name. In cloud multi-instance mode the tenant
// id is the instance id, so this is the instance-isolation guarantee.
func TestGetBySandboxIDOrNameScoped_TenantIsolation(t *testing.T) {
	t.Parallel()

	// Two tenants/instances each own a running sandbox named "web".
	m := &SandboxManager{
		instancesBySandbox: map[string]*Instance{
			"sbx-a": {
				ID:             "sbx-a",
				Name:           "web",
				LifecycleState: LifecycleRunning,
				Config:         InstanceConfig{TenantID: "tenant-a", SessionID: "sess-a"},
			},
			"sbx-b": {
				ID:             "sbx-b",
				Name:           "web",
				LifecycleState: LifecycleRunning,
				Config:         InstanceConfig{TenantID: "tenant-b", SessionID: "sess-b"},
			},
		},
	}

	t.Run("exact id is scoped to owner", func(t *testing.T) {
		if _, ok := m.GetBySandboxIDOrNameScoped("sbx-b", "tenant-a"); ok {
			t.Fatal("tenant-a must not resolve tenant-b's sandbox by exact id")
		}
		inst, ok := m.GetBySandboxIDOrNameScoped("sbx-b", "tenant-b")
		if !ok || inst.ID != "sbx-b" {
			t.Fatal("tenant-b must resolve its own sandbox by exact id")
		}
	})

	t.Run("colliding name resolves only within the caller tenant", func(t *testing.T) {
		instA, ok := m.GetBySandboxIDOrNameScoped("web", "tenant-a")
		if !ok || instA.ID != "sbx-a" {
			t.Fatalf("tenant-a should resolve its own 'web' (sbx-a), got ok=%v inst=%v", ok, instA)
		}
		instB, ok := m.GetBySandboxIDOrNameScoped("web", "tenant-b")
		if !ok || instB.ID != "sbx-b" {
			t.Fatalf("tenant-b should resolve its own 'web' (sbx-b), got ok=%v inst=%v", ok, instB)
		}
	})

	t.Run("unknown tenant resolves nothing", func(t *testing.T) {
		if _, ok := m.GetBySandboxIDOrNameScoped("web", "tenant-c"); ok {
			t.Fatal("a tenant with no sandboxes must resolve nothing")
		}
		if _, ok := m.GetBySandboxIDOrNameScoped("sbx-a", "tenant-c"); ok {
			t.Fatal("a tenant with no sandboxes must not resolve by exact id either")
		}
	})

	t.Run("empty tenant fails closed", func(t *testing.T) {
		if _, ok := m.GetBySandboxIDOrNameScoped("sbx-a", ""); ok {
			t.Fatal("empty tenant must never match")
		}
		if _, ok := m.GetBySandboxIDOrNameScoped("web", ""); ok {
			t.Fatal("empty tenant must never match by name")
		}
	})

	t.Run("unscoped lookup still crosses tenants (documents the gap the scoped form closes)", func(t *testing.T) {
		// The unscoped form is retained for internal/system callers; confirm it
		// behaves as before so we know the scoped form is the actual guard.
		if _, ok := m.GetBySandboxIDOrName("sbx-b"); !ok {
			t.Fatal("unscoped exact-id lookup should still resolve")
		}
	})
}

func TestLookupInstanceByIDFromDBInScope_UsesScopedOwnerForIdentifierShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		identifier string
		wantID     string
	}{
		{name: "exact id", identifier: "sbx-a", wantID: "sbx-a"},
		{name: "colliding name", identifier: "web", wantID: "sbx-a"},
		{name: "colliding short code", identifier: "abc123", wantID: "sbx-a"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, mock, closeDB := newScopedLookupDB(t)
			defer closeDB()

			mock.ExpectQuery(scopedLookupQueryPattern()).
				WithArgs(tc.identifier, "inst-a").
				WillReturnRows(sqlmock.NewRows(scopedLookupColumns()).AddRow(
					tc.wantID,
					"inst-a",
					"container-a",
					"docker",
					"running",
					LifecycleRunning,
					time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
					nil,
					nil,
					"web",
					nil,
					false,
					[]byte(`{"tenant_id":"inst-a","session_id":"sess-a","image":"alpine"}`),
					"alpine",
					"abc123",
					nil,
				))

			inst, err := m.LookupInstanceByIDFromDBInScope(context.Background(), tc.identifier, TenantInstanceScope{
				OrganizationID: "org-a",
				TenantID:       "workspace-a",
				InstanceID:     "inst-a",
			})
			if err != nil {
				t.Fatalf("lookup failed: %v", err)
			}
			if inst.ID != tc.wantID || inst.InstanceID != "inst-a" || inst.Config.TenantID != "inst-a" || inst.ShortCode != "abc123" {
				t.Fatalf("unexpected instance: %+v", inst)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet sql expectations: %v", err)
			}
		})
	}
}

func TestLookupInstanceByIDFromDBInScope_EmptyScopeFailsClosed(t *testing.T) {
	t.Parallel()

	m, mock, closeDB := newScopedLookupDB(t)
	defer closeDB()

	if _, err := m.LookupInstanceByIDFromDBInScope(context.Background(), "sbx-a", TenantInstanceScope{}); err == nil {
		t.Fatal("expected empty scope to fail before querying DB")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected sql query: %v", err)
	}
}

func TestLookupInstanceByIDFromDBScopedOrdersIdentifierMatches(t *testing.T) {
	m, mock, closeDB := newScopedLookupDB(t)
	defer closeDB()

	mock.ExpectQuery(scopedLookupQueryPattern()).
		WithArgs("shared", "tenant-a").
		WillReturnRows(sqlmock.NewRows(scopedLookupColumns()).AddRow(
			"sbx-exact",
			"tenant-a",
			"container-a",
			"docker",
			"running",
			LifecycleRunning,
			time.Now(),
			nil,
			nil,
			"shared",
			nil,
			false,
			[]byte(`{"tenant_id":"tenant-a","session_id":"sess-a","image":"alpine"}`),
			"alpine",
			"shared",
			nil,
		))

	if _, err := m.LookupInstanceByIDFromDBScoped(context.Background(), "shared", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLookupInstanceByNameFromDBInScopeRequiresRunningSandbox(t *testing.T) {
	m, mock, closeDB := newScopedLookupDB(t)
	defer closeDB()
	query := `SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, short_code, agent_target\s+FROM sandbox_instances\s+WHERE name = \$1 AND tenant_id = \$2\s+AND \(\s*lifecycle_state = 'running'\s+OR \(COALESCE\(lifecycle_state, ''\) = '' AND status = 'running'\)\s*\)\s+AND destroyed_at IS NULL\s+ORDER BY created_at DESC, id DESC\s+LIMIT 1`
	mock.ExpectQuery(query).
		WithArgs("shared", "tenant-a").
		WillReturnRows(sqlmock.NewRows(scopedLookupColumns()).AddRow(
			"sbx-running",
			"tenant-a",
			"container-a",
			"docker",
			"running",
			LifecycleRunning,
			time.Now(),
			nil,
			nil,
			"shared",
			nil,
			false,
			[]byte(`{"tenant_id":"tenant-a","image":"alpine"}`),
			"alpine",
			"code-a",
			nil,
		))
	inst, err := m.LookupInstanceByNameFromDBInScope(context.Background(), "shared", TenantInstanceScope{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.ID != "sbx-running" {
		t.Fatalf("sandbox = %q, want sbx-running", inst.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetBySandboxNameInScopeRequiresRunningSandbox(t *testing.T) {
	m := &SandboxManager{}
	m.SeedInstancesForTest(map[string]*Instance{
		"sbx-stopped": {
			ID:     "sbx-stopped",
			Name:   "shared",
			Status: StatusStopped,
			Config: InstanceConfig{TenantID: "tenant-a"},
		},
	})
	if inst, ok := m.GetBySandboxNameInScope("shared", TenantInstanceScope{TenantID: "tenant-a"}); ok || inst != nil {
		t.Fatalf("stopped sandbox matched name pass: %+v", inst)
	}
}

func TestLookupInstanceByIDOrShortCodeFromDBInScopeUsesTenantPredicate(t *testing.T) {
	m, mock, closeDB := newScopedLookupDB(t)
	defer closeDB()
	query := `SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, short_code, agent_target\s+FROM sandbox_instances\s+WHERE \(id = \$1 OR short_code = \$1\) AND tenant_id = \$2`
	mock.ExpectQuery(query).
		WithArgs("shared", "tenant-a").
		WillReturnError(sql.ErrNoRows)

	_, err := m.LookupInstanceByIDOrShortCodeFromDBInScope(context.Background(), "shared", TenantInstanceScope{TenantID: "tenant-a"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error = %v, want sql.ErrNoRows", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPGLookupInstanceByIDFromDBScopedOrdersIdentifierMatches(t *testing.T) {
	db := pgscratch.New(t, "sandbox_lookup_order")
	pgscratch.MustExec(t, db, workspacePGDDL)
	created := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	insert := func(id, name, shortCode, lifecycle string, destroyedAt interface{}, createdAt time.Time) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO sandbox_instances
				(id, session_id, tenant_id, backend, image, status, config, created_at, destroyed_at, name, lifecycle_state, short_code)
			VALUES ($1, $1, 'tenant-a', 'docker', 'alpine', 'running', $2, $3, $4, $5, $6, NULLIF($7, ''))`,
			id, []byte(`{"tenant_id":"tenant-a","image":"alpine"}`), createdAt, destroyedAt, name, lifecycle, shortCode); err != nil {
			t.Fatal(err)
		}
	}
	insert("shared", "other", "", LifecycleTerminated, created, created.Add(-time.Hour))
	insert("sbx-short-shared", "other", "shared", LifecycleRunning, nil, created.Add(time.Hour))
	insert("sbx-name-shared", "shared", "", LifecycleRunning, nil, created.Add(2*time.Hour))
	insert("sbx-short", "other", "shorty", LifecycleRunning, nil, created)
	insert("sbx-name-short", "shorty", "", LifecycleRunning, nil, created.Add(time.Hour))
	insert("sbx-name-a", "name-only", "", LifecycleRunning, nil, created)
	insert("sbx-name-z", "name-only", "", LifecycleRunning, nil, created)
	insert("sbx-running-name", "running-name", "", LifecycleRunning, nil, created)
	insert("sbx-stopped-name", "running-name", "", LifecycleStopped, nil, created.Add(time.Hour))

	m := &SandboxManager{db: db}
	for _, test := range []struct {
		identifier string
		wantID     string
	}{
		{identifier: "shared", wantID: "shared"},
		{identifier: "shorty", wantID: "sbx-short"},
		{identifier: "name-only", wantID: "sbx-name-z"},
	} {
		inst, err := m.LookupInstanceByIDFromDBScoped(context.Background(), test.identifier, "tenant-a")
		if err != nil {
			t.Fatalf("lookup %q: %v", test.identifier, err)
		}
		if inst.ID != test.wantID {
			t.Fatalf("lookup %q = %q, want %q", test.identifier, inst.ID, test.wantID)
		}
	}
	inst, err := m.LookupInstanceByNameFromDBInScope(context.Background(), "running-name", TenantInstanceScope{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.ID != "sbx-running-name" {
		t.Fatalf("running name lookup = %q, want sbx-running-name", inst.ID)
	}
}

func newScopedLookupDB(t *testing.T) (*SandboxManager, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	return &SandboxManager{db: sqlxDB}, mock, func() { _ = sqlxDB.Close() }
}

func scopedLookupColumns() []string {
	return []string{
		"id",
		"instance_id",
		"container_id",
		"backend",
		"status",
		"lifecycle_state",
		"created_at",
		"billing_started_at",
		"billing_ended_at",
		"name",
		"agent_id",
		"persistent",
		"config",
		"image",
		"short_code",
		"agent_target",
	}
}

func scopedLookupQueryPattern() string {
	return `SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, short_code, agent_target\s+FROM sandbox_instances\s+WHERE \(id = \$1 OR name = \$1 OR short_code = \$1\) AND tenant_id = \$2\s+ORDER BY\s+CASE WHEN id = \$1 THEN 0 WHEN short_code = \$1 THEN 1 ELSE 2 END,\s+CASE WHEN COALESCE\(lifecycle_state, ''\) NOT IN \('terminated', 'failed'\) AND destroyed_at IS NULL THEN 0 ELSE 1 END,\s+created_at DESC,\s+id DESC\s+LIMIT 1`
}
