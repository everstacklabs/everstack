package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

const workspaceIncarnationMigration = "sql/postgres/sandbox_workspace_incarnation_20261013141411"

// The incarnation migration runs against a populated sandbox_instances:
// every existing row must get its own incarnation and run id, re-running
// it must be a no-op, and down must undo it.
func TestPGSandboxWorkspaceIncarnationMigration(t *testing.T) {
	db := pgscratch.New(t, "ws_incarnation_migration")
	ctx := context.Background()
	if _, err := db.Exec(`
		CREATE TABLE sandbox_instances (id varchar(255) PRIMARY KEY, tenant_id varchar(255) NOT NULL);
		INSERT INTO sandbox_instances VALUES ('a', 't'), ('b', 't'), ('wks_agent', 't')`); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile(workspaceIncarnationMigration + "/up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(workspaceIncarnationMigration + "/down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := execSQL(ctx, db, string(up)); err != nil {
			t.Fatalf("up (run %d): %v", i+1, err)
		}
	}
	var rows, incarnations, runs int
	if err := db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT workspace_incarnation), COUNT(DISTINCT workspace_run_id) FROM sandbox_instances`).
		Scan(&rows, &incarnations, &runs); err != nil {
		t.Fatal(err)
	}
	if rows != 3 || incarnations != 3 || runs != 3 {
		t.Fatalf("rows=%d incarnations=%d runs=%d, want a distinct incarnation and run per row", rows, incarnations, runs)
	}
	if _, err := db.Exec(`INSERT INTO sandbox_workspace_purges (sandbox_id, incarnation, tenant_id) VALUES ('a', 'x', 't')`); err != nil {
		t.Fatalf("purge outbox: %v", err)
	}
	if err := execSQL(ctx, db, string(down)); err != nil {
		t.Fatalf("down: %v", err)
	}
	var cols int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'sandbox_instances' AND column_name LIKE 'workspace_%'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != 0 {
		t.Fatalf("down left %d workspace columns", cols)
	}
}
