package channels

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// CountChannelMessagesSince against the real channel_messages migration: it
// sums exactly the given tenant ids (one organization's) from the given time,
// and never another tenant's traffic.
func TestPGCountChannelMessagesSinceCountsExactlyTheGivenTenants(t *testing.T) {
	db := pgscratch.New(t, "channel_msgs")
	pgscratch.MustExec(t, db, `CREATE TABLE channel_configs (id UUID PRIMARY KEY, tenant_id UUID NOT NULL)`)
	matches, _ := filepath.Glob("../database/migrations/sql/postgres/channel_messages_*/up.sql")
	if len(matches) != 1 {
		t.Fatalf("expected one channel_messages migration, got %v", matches)
	}
	migration, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	pgscratch.MustExec(t, db, string(migration))

	const (
		orgA  = "aaaaaaaa-0000-4000-8000-00000000000a"
		instA = "aaaaaaaa-0000-4000-8000-0000000000a1"
		orgB  = "bbbbbbbb-0000-4000-8000-00000000000b"
		cfgA  = "aaaaaaaa-0000-4000-8000-0000000000c1"
		cfgA2 = "aaaaaaaa-0000-4000-8000-0000000000c2"
		cfgB  = "bbbbbbbb-0000-4000-8000-0000000000c1"
	)
	pgscratch.MustExec(t, db, `INSERT INTO channel_configs (id, tenant_id) VALUES
		('`+cfgA+`', '`+orgA+`'), ('`+cfgA2+`', '`+instA+`'), ('`+cfgB+`', '`+orgB+`')`)

	store := &PostgresStore{db: db}
	ctx := context.Background()
	record := func(tenant, cfg string, n int) {
		for i := 0; i < n; i++ {
			if err := store.RecordChannelMessage(ctx, &ChannelMessageRecord{
				TenantID: tenant, ChannelConfigID: cfg, Platform: "slack", PlatformUserID: "U1",
			}); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
	}
	record(orgA, cfgA, 2)
	record(instA, cfgA2, 3)
	record(orgB, cfgB, 11)
	// A message from last month must not count.
	pgscratch.MustExec(t, db, `UPDATE channel_messages SET created_at = NOW() - INTERVAL '40 days'
		WHERE id = (SELECT id FROM channel_messages WHERE tenant_id = '`+orgA+`' LIMIT 1)`)

	since := time.Now().Add(-24 * time.Hour)
	got, err := store.CountChannelMessagesSince(ctx, []string{orgA, instA, "not-a-uuid"}, since)
	if err != nil || got != 4 {
		t.Fatalf("org A count = %d, %v; want 4 (2+3 minus last month's, never B's 11)", got, err)
	}
	if got, _ := store.CountChannelMessagesSince(ctx, []string{orgB}, since); got != 11 {
		t.Fatalf("org B count = %d, want 11", got)
	}
	if _, err := store.CountChannelMessagesSince(ctx, nil, since); err == nil {
		t.Fatal("an empty tenant set must error before SQL")
	}
}
