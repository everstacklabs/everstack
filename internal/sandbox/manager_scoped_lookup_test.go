package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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
	return `SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, short_code, agent_target\s+FROM sandbox_instances\s+WHERE \(id = \$1 OR name = \$1 OR short_code = \$1\) AND tenant_id = \$2\s+LIMIT 1`
}
