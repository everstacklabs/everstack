package provider_config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/database/migrations"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Real-Postgres tests for the model trial row
// (docs/design/free-trial-onboarding.md 3.1, 5). They run the gateway
// migrations into a scratch database. Set EVS_TEST_PG_DSN to a superuser DSN
// ending in /postgres; skipped otherwise.

func newTrialRepoDB(t *testing.T) (*Repository, *sqlx.DB) {
	t.Helper()
	db := pgscratch.New(t, "provider_trial")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(wd, "..", "..", "..")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := migrations.Ensure(context.Background(), db, "postgres"); err != nil {
		t.Fatalf("gateway migrations: %v", err)
	}
	return NewRepository(db), db
}

func insertTrialRow(t *testing.T, db *sqlx.DB) string {
	t.Helper()
	var id string
	if err := db.GetContext(context.Background(), &id, `INSERT INTO provider_configurations
			(id, provider_name, api_key_encrypted, enabled_models, custom_settings, is_active, is_trial)
		VALUES (gen_random_uuid(), $1, 'sk-test-trial', '[]'::jsonb, '{}'::jsonb, true, true)
		RETURNING id`, TrialProviderName); err != nil {
		t.Fatalf("insert trial row: %v", err)
	}
	return id
}

// The trial row never enters the platform key pool, nor any unscoped
// instance-level read or write; ListTrialOwned is its only reader.
func TestTrialRowIsExcludedFromEveryUnscopedPath(t *testing.T) {
	repo, db := newTrialRepoDB(t)
	ctx := context.Background()

	// A platform openai row (catalog rows look the same) beside the trial row.
	if err := repo.Upsert(ctx, &Configuration{ProviderName: "openai", APIKeyEncrypted: "sk-test-platform",
		EnabledModels: []string{"gpt-4o"}, IsActive: true}); err != nil {
		t.Fatalf("upsert platform row: %v", err)
	}
	trialID := insertTrialRow(t, db)

	platform, err := repo.ListPlatformOwned(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(platform) != 1 || platform[0].ProviderName != "openai" {
		t.Fatalf("ListPlatformOwned = %d rows, want only the platform openai row", len(platform))
	}
	for _, c := range platform {
		if c.ID == trialID {
			t.Fatal("ListPlatformOwned returned the trial row")
		}
	}

	trial, err := repo.ListTrialOwned(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(trial) != 1 || trial[0].ID != trialID || !trial[0].IsTrial || trial[0].OrganizationID != nil {
		t.Fatalf("ListTrialOwned = %+v, want the one trial row", trial)
	}

	lists := map[string]func() ([]*Configuration, error){
		"List":                     func() ([]*Configuration, error) { return repo.List(ctx) },
		"ListActive":               func() ([]*Configuration, error) { return repo.ListActive(ctx) },
		"ListAllWithCatalogStatus": func() ([]*Configuration, error) { return repo.ListAllWithCatalogStatus(ctx) },
	}
	for name, list := range lists {
		rows, err := list()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, c := range rows {
			if c.ID == trialID {
				t.Fatalf("%s returned the trial row", name)
			}
		}
	}
	if _, err := repo.Get(ctx, TrialProviderName); err == nil {
		t.Fatal("Get returned the trial row")
	}
	if ok, err := repo.Exists(ctx, TrialProviderName); err != nil || ok {
		t.Fatalf("Exists(trial) = %v, %v; want false", ok, err)
	}
	if err := repo.Delete(ctx, TrialProviderName); err == nil {
		t.Fatal("Delete removed the trial row")
	}
	// An instance-level upsert under the trial row's name never rewrites it.
	if err := repo.Upsert(ctx, &Configuration{ProviderName: TrialProviderName, APIKeyEncrypted: "sk-test-other",
		EnabledModels: []string{"gpt-5"}, IsActive: true}); err == nil {
		t.Fatal("Upsert overwrote the trial row")
	}
	if err := repo.UpsertFromCatalog(ctx, TrialProviderName, "available"); err != nil {
		t.Fatalf("UpsertFromCatalog: %v", err)
	}
	var key string
	var isTrial, fromCatalog bool
	if err := db.QueryRowxContext(ctx, `SELECT api_key_encrypted, is_trial, COALESCE(is_from_catalog, false)
		FROM provider_configurations WHERE id = $1`, trialID).Scan(&key, &isTrial, &fromCatalog); err != nil {
		t.Fatal(err)
	}
	if key != "sk-test-trial" || !isTrial || fromCatalog {
		t.Fatal("the trial row was modified through an unscoped write")
	}
}

// Rolling the migration back deletes the trial row, so its key can never
// survive as an ordinary instance-level (platform) row.
func TestTrialMigrationDownDropsTheTrialRow(t *testing.T) {
	_, db := newTrialRepoDB(t)
	ctx := context.Background()
	insertTrialRow(t, db)
	if err := migrations.MigrateDown(ctx, db, "postgres", 1, false); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM provider_configurations WHERE provider_name = $1`, TrialProviderName); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the trial row survived the rollback")
	}
	if _, err := db.ExecContext(ctx, `SELECT is_trial FROM provider_configurations LIMIT 1`); err == nil {
		t.Fatal("is_trial survived the rollback")
	}
}

func TestTrialRowConstraints(t *testing.T) {
	_, db := newTrialRepoDB(t)
	ctx := context.Background()
	insertTrialRow(t, db)
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_configurations
			(id, provider_name, api_key_encrypted, enabled_models, custom_settings, is_active, is_trial)
		VALUES (gen_random_uuid(), 'openai-trial-2', 'x', '[]'::jsonb, '{}'::jsonb, true, true)`); err == nil {
		t.Fatal("a second trial row was accepted")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_configurations
			(id, organization_id, provider_name, api_key_encrypted, enabled_models, custom_settings, is_active, is_trial)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'openai', 'x', '[]'::jsonb, '{}'::jsonb, true, true)`); err == nil {
		t.Fatal("a tenant-owned trial row was accepted")
	}
}
