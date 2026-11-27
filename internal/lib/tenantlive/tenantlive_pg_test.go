package tenantlive

import (
	"context"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

const (
	liveInst    = "aaaaaaaa-0000-4000-8000-0000000000a1"
	deletedInst = "aaaaaaaa-0000-4000-8000-0000000000a2"
	orgID       = "aaaaaaaa-0000-4000-8000-00000000000a"
)

func TestPGDeletedInstancesAreRecognised(t *testing.T) {
	db := pgscratch.New(t, "tenantlive")
	pgscratch.MustExec(t, db,
		`CREATE SCHEMA everstack`,
		`CREATE TABLE everstack.managed_instances (id UUID PRIMARY KEY, organization_id UUID NOT NULL, deleted_at TIMESTAMPTZ)`,
		`INSERT INTO everstack.managed_instances VALUES ('`+liveInst+`', '`+orgID+`', NULL), ('`+deletedInst+`', '`+orgID+`', NOW())`)

	set := NewDeletedInstances(db, time.Hour)
	ctx := context.Background()
	if err := set.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !set.Deleted(deletedInst) || !set.Deleted(" "+deletedInst+" ") {
		t.Fatal("a deleted instance must be recognised")
	}
	for _, id := range []string{liveInst, orgID, "", "not-a-uuid"} {
		if set.Deleted(id) {
			t.Fatalf("%q reported deleted", id)
		}
	}

	// A failed refresh keeps the last snapshot rather than forgetting the
	// deletions (which would re-admit the deleted instance's keys).
	db.Close()
	if err := set.Refresh(ctx); err == nil {
		t.Fatal("refresh on a closed DB must fail")
	}
	if !set.Deleted(deletedInst) {
		t.Fatal("a failed refresh dropped the known deletions")
	}
}

func TestDeletedHookDefaultsToNothingDeleted(t *testing.T) {
	SetDeletedCheck(nil)
	if Deleted(deletedInst) {
		t.Fatal("without a check installed (self-hosted) nothing is deleted")
	}
	SetDeletedCheck(func(id string) bool { return id == deletedInst })
	t.Cleanup(func() { SetDeletedCheck(nil) })
	if !Deleted(deletedInst) || Deleted(liveInst) {
		t.Fatal("installed check not consulted")
	}
}
