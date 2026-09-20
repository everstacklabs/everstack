package openaiapi

import (
	"context"
	"os"
	"testing"
)

// A deleted session is a tombstone: operational reads must not surface it in
// the session list, while a direct read must still resolve it so the delete
// and cleanup paths can observe their own outcome.
func TestPostgresListSessionsExcludesDeletedAndGetStillReadsTombstone(t *testing.T) {
	dsn := os.Getenv("EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EVS_OPENAI_AGENTS_TEST_POSTGRES_DSN for disposable Postgres integration")
	}
	db := openIsolatedHADB(t, dsn)
	for _, name := range []string{
		"../../database/migrations/sql/postgres/openai_agents_api_20260911000000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_session_leases_20260911170000/up.sql",
		"../../database/migrations/sql/postgres/openai_agent_environment_cleanup_fence_20260911220000/up.sql",
	} {
		migration, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := NewPostgresStore(db)
	ctx := context.Background()
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	if _, err := store.CreateConnection(ctx, o, Connection{ID: "connection", Name: "primary", Provider: Provider}, "ciphertext", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"live", "gone"} {
		if _, _, err := store.BeginSessionCreate(ctx, o, Session{ID: id, ConnectionID: "connection", Provider: Provider, Model: "gpt-6", Environment: EnvironmentSpec{Type: "none"}, IdempotencyKey: id, RequestFingerprint: id}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompleteSessionCreate(ctx, o, id, UpstreamSession{ID: "upstream-" + id, Status: "idle"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkCleanup(ctx, o, "gone", "deleted", ""); err != nil {
		t.Fatal(err)
	}

	listed, err := store.ListSessions(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "live" {
		ids := make([]string, 0, len(listed))
		for _, s := range listed {
			ids = append(ids, s.ID+"/"+s.CleanupStatus)
		}
		t.Fatalf("listed sessions = %v, want only the live session", ids)
	}

	tombstone, err := store.GetSession(ctx, o, "gone")
	if err != nil {
		t.Fatalf("deleted session became unreadable: %v", err)
	}
	if tombstone.CleanupStatus != "deleted" {
		t.Fatalf("tombstone cleanup status = %q, want deleted", tombstone.CleanupStatus)
	}
}
