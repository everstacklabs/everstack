package voice_clone

import (
	"context"
	"testing"
)

// TestPostgresRepository_EmptyOrgGuards locks the defense-in-depth
// behavior of the voice_clone repo: every method that targets a single
// profile or filters by org rejects an empty org id before touching
// SQL. The handler layer also enforces this, but a forgotten check at
// the handler shouldn't be enough to leak — the repo refuses the
// query outright.
//
// Passing a nil *sqlx.DB; reaching SQL would panic, so the assertions
// only succeed if the early-return path runs.
func TestPostgresRepository_EmptyOrgGuards(t *testing.T) {
	r := &PostgresRepository{db: nil}
	ctx := context.Background()

	t.Run("GetByID with empty org returns (nil, nil)", func(t *testing.T) {
		p, err := r.GetByID(ctx, "some-id", "")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if p != nil {
			t.Fatalf("expected nil profile, got %+v", p)
		}
	})

	t.Run("ListByOrg with empty org returns nil", func(t *testing.T) {
		ps, err := r.ListByOrg(ctx, "")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if len(ps) != 0 {
			t.Fatalf("expected empty list, got %d", len(ps))
		}
	})

	t.Run("Update with empty profile org errors before SQL", func(t *testing.T) {
		err := r.Update(ctx, &VoiceCloneProfile{ID: "x", OrgID: ""})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Delete with empty org errors before SQL", func(t *testing.T) {
		err := r.Delete(ctx, "x", "")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
