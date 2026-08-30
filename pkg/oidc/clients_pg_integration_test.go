package oidc

import (
	"context"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// Runs only with EVS_TEST_PG_DSN. Validates the oidc_clients DDL + the
// PostgresClientStore against a real Postgres.
func TestPGClientStore(t *testing.T) {
	dsn := os.Getenv("EVS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EVS_TEST_PG_DSN not set; skipping real-Postgres integration test")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	ddl := []string{
		`SET search_path TO everstack, public`,
		`CREATE TABLE IF NOT EXISTS oidc_clients (
			client_id TEXT PRIMARY KEY,
			client_kind TEXT NOT NULL DEFAULT 'instance',
			org_id TEXT NOT NULL,
			instance_id TEXT NOT NULL,
			redirect_uris JSONB NOT NULL DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`TRUNCATE oidc_clients`,
	}
	for _, s := range ddl {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("ddl %q: %v", s, err)
		}
	}

	store := NewPostgresClientStore(db, "oidc_clients")
	want := Client{
		ID:           "inst_acme",
		OrgID:        "org-acme",
		InstanceID:   "inst-1",
		RedirectURIs: []string{"https://acme.everstack.ai/auth/callback"},
	}
	if err := store.Register(ctx, want); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Idempotent re-register (upsert) with an added redirect.
	want.RedirectURIs = []string{"https://acme.everstack.ai/auth/callback", "http://localhost:3000/auth/callback"}
	if err := store.Register(ctx, want); err != nil {
		t.Fatalf("re-register: %v", err)
	}

	got, ok, err := store.Get(ctx, "inst_acme")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.OrgID != "org-acme" || got.InstanceID != "inst-1" || len(got.RedirectURIs) != 2 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if !got.ValidRedirect("http://localhost:3000/auth/callback") {
		t.Error("redirect uri not persisted/loaded correctly")
	}

	if _, ok, _ := store.Get(ctx, "missing"); ok {
		t.Error("missing client should return ok=false")
	}
}
