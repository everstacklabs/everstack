package serve

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"github.com/everstacklabs/everstack/internal/lib/tenantstatus"
)

// A deleted instance's quota is freed (its rows stop counting), so its work
// must stop too: otherwise a Starter org could fill instance X, delete it,
// and keep X serving at 0 of 3 while filling Y. The sweep stops every
// workload of a deleted instance (bots, parked agents, sandboxes), whether
// its tenant_config row is gone or left behind as active, and never
// restores it; every loop's liveness gate reports it inactive.
func TestDeletedInstanceWorkStopsAndNeverResumes(t *testing.T) {
	tenantlive.SetDeletedCheck(func(id string) bool { return id == "inst-x" || id == "inst-z" })
	tenantlive.SetDeletedLister(func() []string { return []string{"inst-x", "inst-z"} })
	t.Cleanup(func() { tenantlive.SetDeletedCheck(nil); tenantlive.SetDeletedLister(nil) })

	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// inst-x has no tenant_config row any more; inst-z's row was left behind
	// as active; inst-y is a live instance.
	rows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"instance_id", "status"}).AddRow("inst-z", "active").AddRow("inst-y", "active")
	}
	mock.ExpectQuery(`FROM everstack\.tenant_config`).WillReturnRows(rows())
	checker := tenantstatus.New(sqlx.NewDb(raw, "sqlmock"), 0)
	w := &fakePausedWorkloads{}
	c := &fakePausedConnectors{}
	sw := newPausedTenantSweeper(checker, w, c)
	sw.sweep(context.Background())

	for name, got := range map[string][]string{"sandboxes": w.stopped, "agents": w.suspended, "channels": c.stopped} {
		if len(got) != 2 || got[0] != "inst-x" || got[1] != "inst-z" {
			t.Fatalf("%s stopped for %v, want both deleted instances and nothing else", name, got)
		}
	}

	// The gate every loop consults (channels, triggers, cron, agent turns,
	// eval scheduler) says no for the deleted instances, yes for the live one.
	active := checker.ActiveFn()
	for id, want := range map[string]bool{"inst-x": false, "inst-z": false, "inst-y": true} {
		if got := active(context.Background(), id); got != want {
			t.Fatalf("active(%s) = %v, want %v", id, got, want)
		}
	}

	// A later sweep never brings a deleted instance back.
	mock.ExpectQuery(`FROM everstack\.tenant_config`).WillReturnRows(rows())
	sw.sweep(context.Background())
	if len(w.resumed) != 0 || len(c.started) != 0 {
		t.Fatalf("resumed=%v started=%v; a deleted instance must never be restored", w.resumed, c.started)
	}
}
