package channels

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// TestChannelMessageMeterAgainstPostgres drives the real migration file and
// the real store queries against a local Postgres, in a throwaway schema.
// Skipped when Postgres is unreachable, matching creation_slot_test.go.
//
// It exists because the bug it guards against was invisible to the compiler:
// resource_counts.go counted a channel_messages table that no migration ever
// created, and its count() helper swallows query errors, so the meter read 0
// forever while the pricing page sold monthly message allowances against it.
// Only executing the SQL catches that class of failure.
func TestChannelMessageMeterAgainstPostgres(t *testing.T) {
	db, err := sqlx.Connect("postgres", "postgres://postgres:postgres@localhost:5432/everstack?sslmode=disable")
	if err != nil {
		t.Skipf("no local postgres: %v", err)
	}
	defer db.Close()

	schema := "meter_test_" + uuid.New().String()[:8]
	if _, err := db.Exec(fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer db.Exec(fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema))
	if _, err := db.Exec(fmt.Sprintf(`SET search_path TO %s, public`, schema)); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	db.SetMaxOpenConns(1) // keep search_path on one session

	if _, err := db.Exec(`CREATE TABLE channel_configs (id UUID PRIMARY KEY, tenant_id UUID NOT NULL)`); err != nil {
		t.Fatalf("stub channel_configs: %v", err)
	}

	matches, _ := filepath.Glob("../database/migrations/sql/postgres/channel_messages_*/up.sql")
	if len(matches) != 1 {
		t.Fatalf("expected exactly one channel_messages migration, got %v", matches)
	}
	migration, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	// Pass 1: no everstack.tenant_matches, which is the services-side database.
	// The DO block must skip policy creation rather than abort the migration.
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("migration failed without everstack.tenant_matches: %v", err)
	}
	t.Log("pass 1 ok: migration is a no-op for policies when the RLS helper is absent")

	// Pass 2: with the helper present, the policy must be created, and the
	// whole migration must stay re-runnable.
	db.Exec(`CREATE SCHEMA IF NOT EXISTS everstack`)
	if _, err := db.Exec(`CREATE OR REPLACE FUNCTION everstack.tenant_matches(row_tenant_id text)
		RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$`); err != nil {
		t.Fatalf("create helper: %v", err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
	var policies int
	if err := db.Get(&policies, `SELECT COUNT(*) FROM pg_policies WHERE tablename = 'channel_messages' AND policyname = 'tenant_isolation'`); err != nil {
		t.Fatalf("policy lookup: %v", err)
	}
	if policies != 1 {
		t.Errorf("expected 1 tenant_isolation policy, got %d", policies)
	}
	var indexes int
	db.Get(&indexes, fmt.Sprintf(`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = '%s' AND tablename = 'channel_messages'`, schema))
	t.Logf("pass 2 ok: policy created, %d indexes present", indexes)

	// Round trip through the store, two tenants sharing the schema.
	store := &PostgresStore{db: db}
	ctx := context.Background()
	tenantA, tenantB := uuid.New().String(), uuid.New().String()
	cfgA, cfgB := uuid.New().String(), uuid.New().String()
	db.Exec(`INSERT INTO channel_configs (id, tenant_id) VALUES ($1, $2), ($3, $4)`, cfgA, tenantA, cfgB, tenantB)

	for i := 0; i < 3; i++ {
		if err := store.RecordChannelMessage(ctx, &ChannelMessageRecord{
			TenantID: tenantA, ChannelConfigID: cfgA, Platform: "slack", PlatformUserID: "U1",
		}); err != nil {
			t.Fatalf("record A: %v", err)
		}
	}
	if err := store.RecordChannelMessage(ctx, &ChannelMessageRecord{
		TenantID: tenantB, ChannelConfigID: cfgB, Platform: "discord", PlatformUserID: "U2",
	}); err != nil {
		t.Fatalf("record B: %v", err)
	}

	gotA, err := store.CountChannelMessagesThisMonth(ctx, tenantA)
	if err != nil {
		t.Fatalf("count A: %v", err)
	}
	gotB, err := store.CountChannelMessagesThisMonth(ctx, tenantB)
	if err != nil {
		t.Fatalf("count B: %v", err)
	}
	if gotA != 3 || gotB != 1 {
		t.Fatalf("counts leaked across tenants: A=%d (want 3), B=%d (want 1)", gotA, gotB)
	}
	t.Logf("tenant-scoped counts correct: A=%d B=%d", gotA, gotB)

	var instanceWide int64
	if err := db.Get(&instanceWide, `SELECT COUNT(*) FROM channel_messages WHERE created_at >= date_trunc('month', NOW())`); err != nil {
		t.Fatalf("resource_counts.go query still fails: %v", err)
	}
	t.Logf("resource_counts.go query returns %d instead of a swallowed 0", instanceWide)

	db.Exec(`DELETE FROM channel_configs WHERE id = $1`, cfgA)
	afterA, _ := store.CountChannelMessagesThisMonth(ctx, tenantA)
	if afterA != 0 {
		t.Errorf("FK cascade left %d orphan meter rows", afterA)
	}
	t.Log("cascade delete removes the deleted channel's meter rows")
}
