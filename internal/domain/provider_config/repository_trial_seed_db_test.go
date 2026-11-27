package provider_config

import (
	"context"
	"testing"
)

// The boot seed writes exactly one trial row, rewrites its key on a later
// boot, and never lets it reach the platform pool.
func TestUpsertTrialSeedsOneRow(t *testing.T) {
	repo, db := newTrialRepoDB(t)
	ctx := context.Background()
	if err := repo.UpsertTrial(ctx, "sk-test-one"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertTrial(ctx, "sk-test-two"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM provider_configurations WHERE is_trial`); err != nil || n != 1 {
		t.Fatalf("trial rows = %d (%v), want 1", n, err)
	}
	rows, err := repo.ListTrialOwned(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTrialOwned = %d (%v)", len(rows), err)
	}
	if rows[0].APIKeyEncrypted != "sk-test-two" || rows[0].ProviderName != TrialProviderName || rows[0].OrganizationID != nil {
		t.Fatal("seeded row has the wrong shape")
	}
	platform, err := repo.ListPlatformOwned(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range platform {
		if c.ID == rows[0].ID {
			t.Fatal("the seeded trial row is in the platform pool")
		}
	}
	if err := repo.UpsertTrial(ctx, ""); err == nil {
		t.Fatal("an empty key was seeded")
	}
}
