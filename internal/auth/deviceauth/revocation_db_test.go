package deviceauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/database/migrations"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Real-Postgres test (EVS_TEST_PG_DSN) for refusing CLI tokens issued before
// an account's credentials were revoked.
func TestCredentialsRevokedAgainstPostgres(t *testing.T) {
	ctx := context.Background()

	t.Run("without the platform schema nothing is revoked", func(t *testing.T) {
		db := pgscratch.New(t, "cli_revocation_bare")
		revoked, err := CredentialsRevoked(ctx, db, uuid.NewString(), time.Now())
		if err != nil || revoked {
			t.Fatalf("CredentialsRevoked() = %v, %v; want false, nil", revoked, err)
		}
	})

	t.Run("before the migration nothing is revoked", func(t *testing.T) {
		db := pgscratch.New(t, "cli_revocation_premigration")
		db.MustExec(`CREATE SCHEMA everstack`)
		revoked, err := CredentialsRevoked(ctx, db, uuid.NewString(), time.Now())
		if err != nil || revoked {
			t.Fatalf("CredentialsRevoked() = %v, %v; want false, nil", revoked, err)
		}
	})

	t.Run("refuses tokens issued at or before the revocation", func(t *testing.T) {
		db := pgscratch.New(t, "cli_revocation")
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(filepath.Join(wd, "..", "..", "..")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(wd) })
		if err := migrations.EnsureForService(ctx, db, "postgres", "cloud"); err != nil {
			t.Fatalf("cloud migrations: %v", err)
		}
		user := uuid.New()
		db.MustExec(`INSERT INTO everstack.users (id, external_id, email) VALUES ($1, $2, 'cli@example.org')`, user, "ext-"+user.String())

		issued := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
		check := func(want bool, at time.Time) {
			t.Helper()
			got, err := CredentialsRevoked(ctx, db, user.String(), at)
			if err != nil || got != want {
				t.Fatalf("CredentialsRevoked(iat %s) = %v, %v; want %v", at.Format(time.RFC3339), got, err, want)
			}
		}
		check(false, issued)

		db.MustExec(`INSERT INTO everstack.user_credential_revocations (user_id, revoked_at) VALUES ($1, $2)`, user, issued.Add(500*time.Millisecond))
		check(true, issued.Add(-time.Hour))
		// iat is whole seconds: the same second as the revocation is refused.
		check(true, issued)
		check(false, issued.Add(time.Second))
		// A token with no iat is refused once anything was revoked.
		check(true, time.Time{})
		// Unknown users have nothing revoked.
		if got, err := CredentialsRevoked(ctx, db, uuid.NewString(), time.Time{}); err != nil || got {
			t.Fatalf("unknown user: %v, %v", got, err)
		}
	})
}
