package v1

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/everstacklabs/everstack/internal/hosting"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

const (
	takedownInstX = "aaaaaaaa-0000-4000-8000-0000000000a1" // deleted instance
	takedownOrgB  = "bbbbbbbb-0000-4000-8000-00000000000b" // another organization
)

func takedownScratch(t *testing.T) *Server {
	t.Helper()
	db := pgscratch.New(t, "hosting_takedown")
	for _, glob := range []string{"hosting_sites_init_*", "hosting_moderation_*"} {
		matches, _ := filepath.Glob(filepath.Join("..", "..", "..", "..", "database", "migrations", "sql", "postgres", glob, "up.sql"))
		if len(matches) != 1 {
			t.Fatalf("migration %s: found %v", glob, matches)
		}
		body, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		pgscratch.MustExec(t, db, string(body))
	}
	pgscratch.MustExec(t, db, `INSERT INTO sites (slug, tenant_id, status, current_version, manifest_key) VALUES
		('x-one', '`+takedownInstX+`', 'active', 1, 'sites/x-one/manifest.json'),
		('x-two', '`+takedownInstX+`', 'active', 1, 'sites/x-two/manifest.json'),
		('x-gone', '`+takedownInstX+`', 'deleted', NULL, NULL),
		('b-one', '`+takedownOrgB+`', 'active', 1, 'sites/b-one/manifest.json')`)
	return &Server{db: db, cfg: Config{Bucket: "sites"}, purger: hosting.NoopPurger{}}
}

func siteStatuses(t *testing.T, s *Server) map[string]string {
	t.Helper()
	var rows []struct {
		Slug   string `db:"slug"`
		Status string `db:"status"`
	}
	if err := s.db.Select(&rows, `SELECT slug, status FROM sites`); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Slug] = r.Status
	}
	return out
}

// Deleting an instance takes its hosted sites off the edge (the manifest the
// Worker serves is removed) through the normal site deletion path, and leaves
// every other organization's sites alone.
func TestPGTakeDownTenantSitesUnpublishesOnlyThatInstance(t *testing.T) {
	s := takedownScratch(t)
	store := &cleanupObjectStore{}
	s.store = store

	n, err := s.TakeDownTenantSites(context.Background(), takedownInstX)
	if err != nil || n != 2 {
		t.Fatalf("took down %d, %v; want X's 2 live sites", n, err)
	}
	got := siteStatuses(t, s)
	if got["x-one"] != "deleted" || got["x-two"] != "deleted" || got["b-one"] != "active" {
		t.Fatalf("statuses = %v; want X's sites deleted and B's untouched", got)
	}
	removed := map[string]bool{}
	for _, k := range store.deleted {
		removed[k] = true
	}
	if !removed[hosting.ManifestKey("x-one")] || !removed[hosting.ManifestKey("x-two")] || removed[hosting.ManifestKey("b-one")] {
		t.Fatalf("manifests removed = %v; want X's two only", store.deleted)
	}

	// Idempotent: a second sweep finds nothing to do.
	if n, err := s.TakeDownTenantSites(context.Background(), takedownInstX); err != nil || n != 0 {
		t.Fatalf("second takedown = %d, %v; want 0", n, err)
	}
}

// A failed unpublish leaves the site in the durable deleting state, which the
// hosting quota still counts, and the next sweep finishes it.
func TestPGFailedTakedownKeepsTheSiteCountedUntilRetried(t *testing.T) {
	s := takedownScratch(t)
	s.store = &cleanupObjectStore{failKey: hosting.ManifestKey("x-two")}

	if _, err := s.TakeDownTenantSites(context.Background(), takedownInstX); err == nil {
		t.Fatal("a failed manifest removal must be reported for retry")
	}
	if got := siteStatuses(t, s); got["x-one"] != "deleted" || got["x-two"] != "deleting" {
		t.Fatalf("statuses = %v; want x-one deleted, x-two still deleting", got)
	}
	var counted int
	if err := s.db.Get(&counted, `SELECT COUNT(*) FROM sites WHERE tenant_id::text = $1 AND status <> 'deleted'`, takedownInstX); err != nil {
		t.Fatal(err)
	}
	if counted != 1 {
		t.Fatalf("sites still counted for X = %d, want 1 (the failed takedown)", counted)
	}

	s.store = &cleanupObjectStore{}
	if n, err := s.TakeDownTenantSites(context.Background(), takedownInstX); err != nil || n != 1 {
		t.Fatalf("retry = %d, %v; want the remaining site taken down", n, err)
	}
	if got := siteStatuses(t, s); got["x-two"] != "deleted" || got["b-one"] != "active" {
		t.Fatalf("after retry statuses = %v", got)
	}
}
