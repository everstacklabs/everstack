package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Deleting an instance revokes its API keys: the sweep revokes every live key
// scoped to a deleted managed instance (by instance_id or the legacy org_id
// slot) and touches nothing else.
func TestPGRevokeDeletedInstanceKeys(t *testing.T) {
	db := pgscratch.New(t, "deleted_inst_keys")
	pgscratch.MustExec(t, db,
		resourcePlatformSchema,
		`CREATE TABLE api_keys (id TEXT PRIMARY KEY, hash TEXT NOT NULL, org_id TEXT, instance_id TEXT,
			revoked BOOLEAN NOT NULL DEFAULT FALSE, revoked_at TIMESTAMPTZ, updated_at TIMESTAMPTZ)`,
		`INSERT INTO everstack.organizations (id) VALUES ('`+resOrgA+`')`,
		`INSERT INTO everstack.managed_instances (id, organization_id, deleted_at) VALUES
			('`+resInstA+`', '`+resOrgA+`', NOW())`,
		`INSERT INTO api_keys (id, hash, org_id, instance_id) VALUES
			('k-inst', 'h1', NULL, '`+resInstA+`'),
			('k-org-slot', 'h2', '`+resInstA+`', NULL),
			('k-org', 'h3', '`+resOrgA+`', NULL),
			('k-other', 'h4', '`+resOrphan+`', NULL)`,
	)

	n, err := revokeDeletedInstanceKeys(context.Background(), db, db)
	if err != nil || n != 2 {
		t.Fatalf("revoked %d, %v; want the deleted instance's 2 keys", n, err)
	}
	var revoked []string
	if err := db.Select(&revoked, `SELECT id FROM api_keys WHERE revoked AND revoked_at IS NOT NULL ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	if len(revoked) != 2 || revoked[0] != "k-inst" || revoked[1] != "k-org-slot" {
		t.Fatalf("revoked keys = %v, want [k-inst k-org-slot]", revoked)
	}
	if n, err := revokeDeletedInstanceKeys(context.Background(), db, db); err != nil || n != 0 {
		t.Fatalf("second sweep revoked %d, %v; want an idempotent 0", n, err)
	}
}

// The sweep takes down every deleted instance's hosted sites (and only
// theirs), keeps retrying an instance whose takedown failed, and does nothing
// when hosting is not wired.
func TestTakeDownDeletedInstanceSites(t *testing.T) {
	var calls []string
	failing := map[string]bool{"inst-x": true}
	takedown := func(_ context.Context, id string) (int, error) {
		calls = append(calls, id)
		if failing[id] {
			return 0, errors.New("manifest delete failed")
		}
		return 1, nil
	}
	n, err := takeDownDeletedInstanceSites(context.Background(), []string{"inst-x", "inst-z"}, takedown)
	if n != 1 || err == nil {
		t.Fatalf("n=%d err=%v; want one instance done and the failure reported", n, err)
	}
	if len(calls) != 2 || calls[0] != "inst-x" || calls[1] != "inst-z" {
		t.Fatalf("calls = %v; want both deleted instances, nothing else", calls)
	}
	delete(failing, "inst-x")
	calls = nil
	if _, err := takeDownDeletedInstanceSites(context.Background(), []string{"inst-x"}, takedown); err != nil || len(calls) != 1 {
		t.Fatalf("retry: calls=%v err=%v", calls, err)
	}
	if n, err := takeDownDeletedInstanceSites(context.Background(), []string{"inst-x"}, nil); n != 0 || err != nil {
		t.Fatalf("without hosting: n=%d err=%v; want a no-op", n, err)
	}
}
