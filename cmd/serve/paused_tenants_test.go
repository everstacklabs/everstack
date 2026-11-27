package serve

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/lib/tenantstatus"
	"github.com/jmoiron/sqlx"
)

type fakePausedWorkloads struct{ stopped, suspended, resumed []string }

func (f *fakePausedWorkloads) ResumeTenantAgents(_ context.Context, id string) (int, error) {
	f.resumed = append(f.resumed, id)
	return 1, nil
}

func (f *fakePausedWorkloads) StopTenantSandboxes(_ context.Context, id string) (int, error) {
	f.stopped = append(f.stopped, id)
	return 1, nil
}

func (f *fakePausedWorkloads) SuspendTenantAgents(_ context.Context, id string) (int, error) {
	f.suspended = append(f.suspended, id)
	return 1, nil
}

type fakePausedConnectors struct{ stopped, started []string }

func (f *fakePausedConnectors) StartTenantConnectors(_ context.Context, id string) int {
	f.started = append(f.started, id)
	return 1
}

func (f *fakePausedConnectors) StopTenantConnectors(_ context.Context, id string) int {
	f.stopped = append(f.stopped, id)
	return 1
}

func TestSweepPausedTenantsStopsEveryWorkloadOfEachPausedTenant(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	mock.ExpectQuery(`FROM everstack\.tenant_config`).
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "status"}).AddRow("inst-a", "paused").AddRow("inst-b", "paused"))
	checker := tenantstatus.New(sqlx.NewDb(raw, "sqlmock"), 0)
	w := &fakePausedWorkloads{}
	c := &fakePausedConnectors{}

	sw := newPausedTenantSweeper(checker, w, c)
	sw.sweep(context.Background())

	for name, got := range map[string][]string{"sandboxes": w.stopped, "agents": w.suspended, "channels": c.stopped} {
		if len(got) != 2 || got[0] != "inst-a" || got[1] != "inst-b" {
			t.Fatalf("%s stopped for %v, want both paused tenants", name, got)
		}
	}
	if len(w.resumed) != 0 || len(c.started) != 0 {
		t.Fatal("nothing is resumed while paused")
	}

	// inst-a is resumed: its agents and channels come back, inst-b stays down.
	mock.ExpectQuery(`FROM everstack\.tenant_config`).
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "status"}).AddRow("inst-a", "active").AddRow("inst-b", "paused"))
	sw.sweep(context.Background())
	if len(w.resumed) != 1 || w.resumed[0] != "inst-a" || len(c.started) != 1 || c.started[0] != "inst-a" {
		t.Fatalf("resumed=%v started=%v, want inst-a only", w.resumed, c.started)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPausedTenantHostGuardIsANoOpOutsideManagedCloud(t *testing.T) {
	prev := pausedTenants
	pausedTenants = nil
	defer func() { pausedTenants = prev }()
	called := false
	h := pausedTenantHostGuard(httpHandlerFunc(func() { called = true }))
	h.ServeHTTP(nil, nil)
	if !called {
		t.Fatal("guard must pass straight through without a checker")
	}
}

type httpHandlerFunc func()

func (f httpHandlerFunc) ServeHTTP(_ http.ResponseWriter, _ *http.Request) { f() }

// gatedWorkloads behaves like the real sandbox manager and channel manager:
// restore calls consult the same checker and silently no-op while it still
// says paused.
type gatedWorkloads struct {
	checker          *tenantstatus.Checker
	resumed, started []string
}

func (g *gatedWorkloads) StopTenantSandboxes(context.Context, string) (int, error) { return 0, nil }
func (g *gatedWorkloads) SuspendTenantAgents(context.Context, string) (int, error) { return 0, nil }
func (g *gatedWorkloads) StopTenantConnectors(context.Context, string) int         { return 0 }
func (g *gatedWorkloads) ResumeTenantAgents(ctx context.Context, id string) (int, error) {
	if !g.checker.Active(ctx, id) {
		return 0, nil
	}
	g.resumed = append(g.resumed, id)
	return 1, nil
}
func (g *gatedWorkloads) StartTenantConnectors(ctx context.Context, id string) int {
	if !g.checker.Active(ctx, id) {
		return 0
	}
	g.started = append(g.started, id)
	return 1
}

func tenantTable(status string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"instance_id", "slug", "org_slug", "status", "paused_reason"}).
		AddRow("11111111-2222-3333-4444-555555a1b2c3", "dev", "acme", status, "")
}

// A resume must restore agents and connectors even while this replica's
// status snapshot still says paused (inside its TTL), and an id is only
// forgotten once the restore has run.
func TestSweepRestoresAResumedTenantDespiteAStaleSnapshot(t *testing.T) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	checker := tenantstatus.New(sqlx.NewDb(raw, "sqlmock"), time.Hour) // nothing expires on its own
	g := &gatedWorkloads{checker: checker}
	sw := newPausedTenantSweeper(checker, g, g)
	ctx := context.Background()
	const id = "11111111-2222-3333-4444-555555a1b2c3"

	mock.ExpectQuery(`FROM everstack\.tenant_config`).WillReturnRows(tenantTable("paused"))
	sw.sweep(ctx)
	if checker.Active(ctx, id) {
		t.Fatal("setup: paused")
	}

	// The user resumes. A reload failure first: nothing may be forgotten.
	mock.ExpectQuery(`FROM everstack\.tenant_config`).WillReturnError(errors.New("db down"))
	sw.sweep(ctx)
	if len(g.resumed)+len(g.started) != 0 {
		t.Fatal("nothing can be restored without a fresh read")
	}

	mock.ExpectQuery(`FROM everstack\.tenant_config`).WillReturnRows(tenantTable("active"))
	sw.sweep(ctx)
	if len(g.resumed) != 1 || len(g.started) != 1 {
		t.Fatalf("resumed=%v started=%v: a resumed tenant must get agents and connectors back", g.resumed, g.started)
	}
	mock.ExpectQuery(`FROM everstack\.tenant_config`).WillReturnRows(tenantTable("active"))
	sw.sweep(ctx)
	if len(g.resumed) != 1 {
		t.Fatal("restore runs once")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
