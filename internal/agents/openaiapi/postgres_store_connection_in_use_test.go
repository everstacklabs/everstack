package openaiapi

import (
	"context"
	"errors"
	"os"
	"testing"
)

// Deleting a connection clears the credential references, and those are the
// only credentials that can tear a session down upstream or stop its sandbox.
// A connection that still backs live sessions must therefore refuse deletion,
// or the sandbox is stranded: running, billed, and unreachable.
func TestPostgresDeleteConnectionRefusesWhileSessionsHoldResources(t *testing.T) {
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
	if _, err := store.CreateConnection(ctx, o, Connection{ID: "connection", Name: "primary", Provider: Provider}, "ciphertext", "executor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginSessionCreate(ctx, o, Session{ID: "live", ConnectionID: "connection", Provider: Provider, Model: "gpt-6", Environment: EnvironmentSpec{Type: "self_hosted"}, IdempotencyKey: "live", RequestFingerprint: "live"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteSessionCreate(ctx, o, "live", UpstreamSession{ID: "upstream-live", Status: "idle"}); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteConnection(ctx, o, "connection"); !errors.Is(err, ErrConnectionInUse) {
		t.Fatalf("delete with a live session = %v, want ErrConnectionInUse", err)
	}
	// The refusal must be non-destructive: the credentials the teardown needs
	// are still there.
	c, err := store.GetConnection(ctx, o, "connection")
	if err != nil {
		t.Fatalf("connection unreadable after a refused delete: %v", err)
	}
	if c.credentialRef == "" || c.executorRef == "" {
		t.Fatalf("refused delete still cleared credentials: app=%q executor=%q", c.credentialRef, c.executorRef)
	}

	// Retiring the session releases the connection.
	if err = store.MarkCleanup(ctx, o, "live", "deleted", ""); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteConnection(ctx, o, "connection"); err != nil {
		t.Fatalf("delete after the session was retired = %v, want success", err)
	}
	// A second delete is a genuine not-found, not a false "in use".
	if err = store.DeleteConnection(ctx, o, "connection"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

// A session belonging to another tenant must not block this tenant's delete,
// and must not be counted by the guard.
func TestPostgresDeleteConnectionIgnoresForeignTenantSessions(t *testing.T) {
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
	mine := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	theirs := Ownership{OrganizationID: "other-org", InstanceID: "other-instance", PrincipalID: "user", Role: "admin"}
	// Connection ids are globally unique, so each tenant gets its own.
	for id, o := range map[string]Ownership{"mine": mine, "theirs": theirs} {
		if _, err := store.CreateConnection(ctx, o, Connection{ID: id, Name: "primary", Provider: Provider}, "ciphertext", "executor"); err != nil {
			t.Fatal(err)
		}
	}
	// Only the foreign tenant has a live session, on its own connection.
	if _, _, err := store.BeginSessionCreate(ctx, theirs, Session{ID: "live", ConnectionID: "theirs", Provider: Provider, Model: "gpt-6", Environment: EnvironmentSpec{Type: "none"}, IdempotencyKey: "live", RequestFingerprint: "live"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteConnection(ctx, mine, "mine"); err != nil {
		t.Fatalf("a foreign tenant's session blocked this tenant's delete: %v", err)
	}
	if err := store.DeleteConnection(ctx, theirs, "theirs"); !errors.Is(err, ErrConnectionInUse) {
		t.Fatalf("the owning tenant's delete = %v, want ErrConnectionInUse", err)
	}
}
