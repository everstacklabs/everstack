package database

import (
	"os"
	"strings"
	"testing"
)

const catalogProjectionMigration = "migrations/sql/postgres/catalog_projection_releases_20260821000000"

func TestCatalogProjectionMigrationShipsDurableJournalAndDeliveryLeases(t *testing.T) {
	up, err := os.ReadFile(catalogProjectionMigration + "/up.sql")
	if err != nil {
		t.Fatalf("read catalog projection up migration: %v", err)
	}
	normalizedUp := strings.Join(strings.Fields(string(up)), " ")
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS catalog_projection_releases",
		"version",
		"bundle_sha256",
		"events JSONB",
		"events_persisted_at",
		"events_published_at",
		"publication_claim_id",
		"publication_claim_at",
	} {
		if !strings.Contains(normalizedUp, required) {
			t.Errorf("catalog projection up migration is missing %q", required)
		}
	}

	down, err := os.ReadFile(catalogProjectionMigration + "/down.sql")
	if err != nil {
		t.Fatalf("read catalog projection down migration: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS catalog_projection_releases") {
		t.Fatal("catalog projection down migration does not remove the journal")
	}
}
